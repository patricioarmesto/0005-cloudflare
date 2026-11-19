// edge.go — Este archivo ES el edge: la capa que está cerca del usuario. En la vida real
// sería uno de los cientos de POP de Cloudflare; acá es un proceso Go que decide entre
// responder desde Redis (HIT, ~5 ms) o mandar a buscar al origen (MISS, ~200 ms).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// cacheLog es un evento de analítica. ¿Por qué no escribirlo directo en Postgres dentro del
// handler? Porque un INSERT sincrónico le suma ~1-2 ms al HIT y nos saca del régimen de 5 ms.
type cacheLog struct {
	slug      string
	result    string
	latencyMS int64
	pop       string
}

// logEvent es lo que viaja por el canal de logs. ¿Por qué un wrapper y no el cacheLog
// directo? Porque ServeStats necesita una barrera: al encontrar un evento con `ack`,
// el worker cierra ese canal y eso garantiza que todos los logs anteriores ya se
// insertaron. Es lo que evita que el hit rate se quede un evento atrás de la respuesta.
type logEvent struct {
	entry cacheLog
	ack   chan struct{}
}

// pageResponse es lo que ve el frontend. El contenido viene del JSON cacheado; el resto
// (cache, pop, latencia) se calcula en CADA request y por eso nunca se guarda en Redis.
type pageResponse struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Cache      string `json:"cache"`
	Pop        string `json:"pop"`
	LatencyMS  int64  `json:"latency_ms"`
	CacheKey   string `json:"cache_key"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// popStat y recentLog alimentan el gráfico y la tabla del panel.
type popStat struct {
	Pop     string  `json:"pop"`
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	HitRate float64 `json:"hit_rate"`
}

type recentLog struct {
	Slug      string `json:"slug"`
	Result    string `json:"result"`
	LatencyMS int64  `json:"latency_ms"`
	Pop       string `json:"pop"`
	CreatedAt string `json:"created_at"`
}

type statsResponse struct {
	Hits    int64       `json:"hits"`
	Misses  int64       `json:"misses"`
	Total   int64       `json:"total"`
	HitRate float64     `json:"hit_rate"`
	ByPop   []popStat   `json:"by_pop"`
	Recent  []recentLog `json:"recent"`
}

// Edge existe para concentrar las tres responsabilidades del POP: buscar en Redis,
// decidir si va al origen y registrar lo que pasó. Mantiene referencias a los tres
// colaboradores en vez de usar globals, para que el test pueda inyectar reemplazos.
type Edge struct {
	rdb       *redis.Client
	db        *sql.DB
	origin    *Origin
	edgeDelay time.Duration
	ttl       time.Duration
	logCh     chan logEvent
	done      chan struct{}
	insert    func(context.Context, cacheLog) error
}

// NewEdge existe porque el canal de logs tiene un tamaño elegido a propósito: 2048 entra
// holgado para 100 requests y, si un día llega un flood, se pierden logs pero no respuestas.
func NewEdge(rdb *redis.Client, db *sql.DB, origin *Origin, edgeDelay, ttl time.Duration) *Edge {
	return &Edge{
		rdb:       rdb,
		db:        db,
		origin:    origin,
		edgeDelay: edgeDelay,
		ttl:       ttl,
		logCh:     make(chan logEvent, 2048),
		done:      make(chan struct{}),
		// ¿Por qué un campo insert y no e.db.ExecContext directo? Para que el test pueda
		// reemplazar la escritura por una función en memoria y verificar el drenaje sin
		// levantar Postgres. Inyectar la dependencia externa es patrón del curso.
		insert: func(ctx context.Context, entry cacheLog) error {
			_, err := db.ExecContext(ctx,
				`INSERT INTO cache_logs (slug, result, latency_ms, pop) VALUES ($1, $2, $3, $4)`,
				entry.slug, entry.result, entry.latencyMS, entry.pop)
			return err
		},
	}
}

// cacheKey existe para respetar la key literal del enunciado y, al mismo tiempo, soportar
// las otras cuatro páginas. cache:/pagina es la home; el resto lleva el slug como sufijo.
func cacheKey(slug string) string {
	if slug == "" || slug == "inicio" {
		return "cache:/pagina"
	}
	return "cache:/pagina/" + slug
}

// popByRegion es el DNS mock: en vez de resolver CNAMEs y anycast, traducimos la región
// del usuario a un POP cercano con un mapa en memoria. Ver ADR-004.
var popByRegion = map[string]string{
	"buenos-aires": "EZE",
	"eze":          "EZE",
	"santiago":     "SCL",
	"scl":          "SCL",
	"mexico":       "MEX",
	"mex":          "MEX",
	"miami":        "MIA",
	"mia":          "MIA",
}

// resolvePOP existe para darle identidad geográfica a cada respuesta. ¿Por qué un default?
// Si un usuario no manda el header, asumimos EZE para que el panel nunca muestre un POP vacío;
// si hacés esto distinto (devolver ""), el gráfico de Recharts queda con una barra sin nombre.
func resolvePOP(region string) string {
	key := strings.ToLower(strings.TrimSpace(region))
	if pop, ok := popByRegion[key]; ok {
		return pop
	}
	return "EZE"
}

// ServePage existe porque es el camino caliente del CDN: una sola función que decide HIT
// o MISS, setea los headers de observabilidad y encola el log sin bloquear la respuesta.
func (e *Edge) ServePage(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	slug := r.PathValue("slug") // Ojo: en la ruta /pagina (sin slug) PathValue devuelve "".
	if slug == "" {
		slug = "inicio"
	}
	pop := resolvePOP(r.Header.Get("X-User-Region"))
	key := cacheKey(slug)

	w.Header().Set("X-Edge-Pop", pop)
	w.Header().Set("Cache-Key", key)

	var page Page
	fromCache := false

	raw, err := e.rdb.Get(r.Context(), key).Bytes()
	switch {
	case err == nil:
		if unmarshalErr := json.Unmarshal(raw, &page); unmarshalErr == nil {
			fromCache = true
		} else {
			log.Printf("JSON corrupto en %s, se vuelve a pedir al origen: %v", key, unmarshalErr)
		}
	case errors.Is(err, redis.Nil):
		// Es el MISS esperado: Redis no tiene la key todavía.
	default:
		// ¿Por qué seguimos en vez de fallar? Redis es una optimización, no la fuente de
		// verdad. Si se cae, el edge debe degradar a MISS y seguir sirviendo contenido.
		log.Printf("Redis no respondió para %s, se sigue sin caché: %v", key, err)
	}

	result := "MISS"
	if fromCache {
		result = "HIT"
		// ¿Por qué dormir 5 ms si Redis local tarda <1 ms? Porque representan el viaje
		// POP→usuario. Sin este delay la diferencia sería 200 ms vs 0,3 ms y el ratio
		// tan grande dejaría de ser didáctico.
		select {
		case <-time.After(e.edgeDelay):
		case <-r.Context().Done():
			return
		}
	} else {
		page, err = e.origin.Fetch(r.Context(), slug)
		if err != nil {
			http.Error(w, `{"error":"el origen no respondió"}`, http.StatusBadGateway)
			return
		}

		payload, marshalErr := json.Marshal(page)
		if marshalErr != nil {
			http.Error(w, `{"error":"no se pudo serializar la página"}`, http.StatusInternalServerError)
			return
		}
		// Ojo: el TTL se aplica acá y en ningún otro lado. Si hacés esto distinto (SET sin
		// EX), la primera página queda para siempre y el botón "Limpiar cache" pierde sentido.
		if setErr := e.rdb.Set(r.Context(), key, payload, e.ttl).Err(); setErr != nil {
			log.Printf("no se pudo poblar la caché %s: %v", key, setErr)
		}
	}

	latencyMS := time.Since(started).Milliseconds()
	w.Header().Set("X-Cache", result)
	w.Header().Set("X-Cache-Latency-Ms", strconv.FormatInt(latencyMS, 10))

	writeJSON(w, http.StatusOK, pageResponse{
		Slug:       page.Slug,
		Title:      page.Title,
		Body:       page.Body,
		Cache:      result,
		Pop:        pop,
		LatencyMS:  latencyMS,
		CacheKey:   key,
		TTLSeconds: int(e.ttl / time.Second),
	})

	e.record(cacheLog{slug: slug, result: result, latencyMS: latencyMS, pop: pop})
}

// record existe para encolar el evento sin bloquear nunca. El `default` del select es el
// circuit breaker: si el canal está lleno, descartamos el log. Si hacés esto distinto
// (envío bloqueante), un Postgres lento se transforma en un HIT lento.
func (e *Edge) record(entry cacheLog) {
	select {
	case e.logCh <- logEvent{entry: entry}:
	default:
		log.Printf("canal de logs lleno, se descarta un evento %s de %s", entry.result, entry.slug)
	}
}

// RunLogWriter existe para mover la escritura de cache_logs fuera del request. Escucha el
// canal hasta el shutdown: de esa manera el INSERT no entra en la latencia que medimos.
func (e *Edge) RunLogWriter(ctx context.Context) {
	defer close(e.done)

	for {
		select {
		case event, ok := <-e.logCh:
			// Ojo: al cerrar el canal, Go devuelve el valor cero de logEvent una y otra vez.
			// Sin este `ok` el worker entra en un loop infinito insertando filas vacías y
			// Close() nunca termina. Eso es exactamente lo que cubre el test de shutdown.
			if !ok {
				return
			}
			if event.ack != nil {
				close(event.ack) // Marca FIFO: todo lo anterior ya se insertó.
				continue
			}
			if err := e.insert(ctx, event.entry); err != nil {
				log.Printf("no se pudo loguear el %s: %v", event.entry.result, err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// Close existe para vaciar el canal antes de que el proceso muera. Cerramos el canal y
// esperamos a que el worker termine; Shutdown(main.go) ya garantiza que no hay handlers
// escribiendo cuando esto pasa. ¿Por qué cerrar y no cancelar? Porque así se drenan los
// eventos pendientes en vez de descartarlos en el apagón.
func (e *Edge) Close() {
	close(e.logCh)
	<-e.done
}

// Flush existe porque los logs son asíncronos: si ServeStats consultara Postgres apenas
// terminan los 100 pedidos, el último INSERT podría estar todavía en vuelo y el panel
// mostraría 98 HIT en vez de 99. Como el canal es FIFO, alcanza con mandar una marca y
// esperar a que el worker la procese: cuando se cierra `ack`, todo lo anterior ya está
// en la base. Ojo: no toca el camino caliente, corre sólo cuando se piden estadísticas.
func (e *Edge) Flush(ctx context.Context) error {
	ack := make(chan struct{})
	select {
	case e.logCh <- logEvent{ack: ack}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ServeClearCache existe para reiniciar el experimento sin reiniciar contenedores. Usamos
// SCAN + DEL en vez de FLUSHDB: así no borramos keys de otros fines que puedan aparecer
// después en el mismo Redis. Los logs NO se tocan: queremos ver el nuevo MISS en el gráfico.
func (e *Edge) ServeClearCache(w http.ResponseWriter, r *http.Request) {
	var deleted int64
	cursor := uint64(0)

	for {
		keys, next, err := e.rdb.Scan(r.Context(), cursor, "cache:/pagina*", 100).Result()
		if err != nil {
			http.Error(w, `{"error":"no se pudo recorrer Redis"}`, http.StatusInternalServerError)
			return
		}
		if len(keys) > 0 {
			n, err := e.rdb.Del(r.Context(), keys...).Result()
			if err != nil {
				http.Error(w, `{"error":"no se pudo borrar la caché"}`, http.StatusInternalServerError)
				return
			}
			deleted += n
		}
		if next == 0 {
			break
		}
		cursor = next
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// ServeStats existe para responder una pregunta de negocio simple ("¿cuánto sirvo desde
// el edge?") con SQL aburrido y legible. Llama a Flush antes de consultar para que el
// número del panel coincida con lo que el browser acaba de recibir.
func (e *Edge) ServeStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Ojo: sin esta barrera el último HIT/MISS todavía puede estar en el canal y el
	// hit rate quedaría desactualizado justo después de la tanda de 100 pedidos.
	if err := e.Flush(ctx); err != nil {
		http.Error(w, `{"error":"no se pudo sincronizar los logs"}`, http.StatusInternalServerError)
		return
	}

	var hits, misses int64
	err := e.db.QueryRowContext(ctx, `SELECT
        COALESCE(SUM(CASE WHEN result = 'HIT'  THEN 1 ELSE 0 END), 0),
        COALESCE(SUM(CASE WHEN result = 'MISS' THEN 1 ELSE 0 END), 0)
        FROM cache_logs`).Scan(&hits, &misses)
	if err != nil {
		http.Error(w, `{"error":"no se pudieron leer las estadísticas"}`, http.StatusInternalServerError)
		return
	}

	total := hits + misses
	overallRate := hitRate(hits, total)

	byPop := make([]popStat, 0, 4) // ¿Por qué 4? Es la cantidad de POPs del mapa de DNS mock.
	popRows, err := e.db.QueryContext(ctx, `SELECT pop,
        SUM(CASE WHEN result = 'HIT'  THEN 1 ELSE 0 END),
        SUM(CASE WHEN result = 'MISS' THEN 1 ELSE 0 END)
        FROM cache_logs GROUP BY pop ORDER BY pop`)
	if err != nil {
		http.Error(w, `{"error":"no se pudo agrupar por POP"}`, http.StatusInternalServerError)
		return
	}
	for popRows.Next() {
		var stat popStat
		if err := popRows.Scan(&stat.Pop, &stat.Hits, &stat.Misses); err != nil {
			popRows.Close()
			http.Error(w, `{"error":"no se pudo leer el POP"}`, http.StatusInternalServerError)
			return
		}
		stat.HitRate = hitRate(stat.Hits, stat.Hits+stat.Misses)
		byPop = append(byPop, stat)
	}
	popRows.Close()
	if err := popRows.Err(); err != nil {
		http.Error(w, `{"error":"error al recorrer los POP"}`, http.StatusInternalServerError)
		return
	}

	recent := make([]recentLog, 0, 12) // ¿Por qué 12? Es el LIMIT de la query de abajo: evita crecer el slice a ciegas.
	recentRows, err := e.db.QueryContext(ctx, `SELECT slug, result, latency_ms, pop, created_at
        FROM cache_logs ORDER BY id DESC LIMIT 12`)
	if err != nil {
		http.Error(w, `{"error":"no se pudo leer el log reciente"}`, http.StatusInternalServerError)
		return
	}
	for recentRows.Next() {
		var item recentLog
		var createdAt time.Time
		if err := recentRows.Scan(&item.Slug, &item.Result, &item.LatencyMS, &item.Pop, &createdAt); err != nil {
			recentRows.Close()
			http.Error(w, `{"error":"no se pudo leer el evento"}`, http.StatusInternalServerError)
			return
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		recent = append(recent, item)
	}
	recentRows.Close()
	if err := recentRows.Err(); err != nil {
		http.Error(w, `{"error":"error al recorrer el log"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, statsResponse{
		Hits:    hits,
		Misses:  misses,
		Total:   total,
		HitRate: overallRate,
		ByPop:   byPop,
		Recent:  recent,
	})
}

// hitRate existe porque el mismo cálculo aparece dos veces en ServeStats: el global y
// el de cada POP. Centralizarlo evita que un día se cambie una mitad y el panel muestre
// dos hit rates distintos para los mismos datos.
func hitRate(hits, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}
