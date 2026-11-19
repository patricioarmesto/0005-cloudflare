// main.go — Punto de entrada del sistema. Acá se conecta a Postgres y Redis, se inicializan
// las páginas del origen, se registran las rutas HTTP y se deja el servidor escuchando.
// No contiene lógica de caché: esa decisión vive en edge.go y el contenido en origin.go.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq" // Ojo: el driver se registra solo con el import en blanco. Sin esto, sql.Open("postgres", ...) falla en runtime.
	"github.com/redis/go-redis/v9"
)

// config concentra las variables de entorno para que el experimento se pueda tunear sin tocar código.
type config struct {
	port        string
	databaseURL string
	redisAddr   string
	originDelay time.Duration
	edgeDelay   time.Duration
	cacheTTL    time.Duration
}

// loadConfig existe porque los delays SON el experimento didáctico: cambiar ORIGIN_DELAY_MS
// permite mostrar que el MISS crece linealmente con la distancia al origen, sin recompilar.
func loadConfig() config {
	return config{
		port:        envOr("PORT", "8080"),
		databaseURL: envOr("DATABASE_URL", "postgres://app:app@postgres:5432/cloudflare?sslmode=disable"),
		redisAddr:   envOr("REDIS_ADDR", "redis:6379"),
		originDelay: time.Duration(envIntOr("ORIGIN_DELAY_MS", 200)) * time.Millisecond,
		edgeDelay:   time.Duration(envIntOr("EDGE_DELAY_MS", 5)) * time.Millisecond,
		cacheTTL:    time.Duration(envIntOr("CACHE_TTL_SECONDS", 60)) * time.Second,
	}
}

// envOr existe para que el mismo binario ande dentro de Compose y con `go run` en la laptop.
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// envIntOr existe porque queremos números, no strings: un delay mal escrito no debe
// convertirse silenciosamente en 0 nanosegundos y arruinar la demostración.
func envIntOr(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("valor inválido en %s=%q, uso %d: %v", key, value, fallback, err)
		return fallback
	}
	return parsed
}

// openPostgres existe porque sql.Open NO conecta: sólo prepara un pool. Hace falta un Ping
// antes de servir requests. ¿Por qué el retry? Compose ya tiene healthcheck, pero así el
// arranque local con `go run` tampoco depende de un timing perfecto.
func openPostgres(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	// ¿Por qué limitar el pool? El worker de logs y los handlers comparten la misma DB;
	// sin techo, una tanda de 100 pedidos podría abrir 100 conexiones contra Postgres.
	// ¿Por qué renovar cada 30 min? Para no arrastrar conexiones viejas tras un reinicio
	// del contenedor de Postgres durante la demo.
	db.SetMaxOpenConns(12)
	db.SetConnMaxLifetime(30 * time.Minute)

	for attempt := 1; attempt <= 40; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = db.PingContext(ctx)
		cancel()
		if err == nil {
			return db, nil
		}
		log.Printf("esperando Postgres (intento %d/40): %v", attempt, err)
		time.Sleep(500 * time.Millisecond)
	}
	db.Close()
	return nil, fmt.Errorf("Postgres no respondió: %w", err)
}

// newRedisClient existe para centralizar el Ping: un Redis caído se detecta al arrancar,
// no en el primer HIT, que es justo cuando el alumno está mirando la pantalla.
func newRedisClient(ctx context.Context, addr string) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("Redis no respondió: %w", err)
	}
	return client, nil
}

// cors existe porque en `npm run dev` el frontend corre en otro puerto. En Compose nginx hace
// de proxy y este middleware no haría falta. Ojo: exponemos X-Cache y X-Edge-Pop para que se
// vean con curl desde el browser; el body ya trae lo mismo si no se exponen.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Region")
		w.Header().Set("Access-Control-Expose-Headers", "X-Cache, X-Edge-Pop, Cache-Key, X-Cache-Latency-Ms")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSON existe para que todos los handlers respondan con el mismo content-type y el
// mismo formato. Si hacés esto distinto (escribir con fmt.Fprintf a mano), el frontend
// tendría que adivinar el MIME type en cada ruta.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("no se pudo escribir la respuesta JSON: %v", err)
	}
}

// main existe porque es el único lugar que conoce los tres colaboradores: arma el pool
// de Postgres, el cliente de Redis, el origen, el edge y las rutas. Si esta función hiciera
// también lógica de caché, el límite entre POP y origen se diluiría.
func main() {
	// ¿Por qué WithCancel y no Background? Porque el worker de logs necesita una señal
	// de salida. El defer se registra PRIMERO para que se ejecute ÚLTIMO: así Close()
	// drena el canal antes de que se cancele el contexto.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := loadConfig()

	db, err := openPostgres(cfg.databaseURL)
	if err != nil {
		log.Fatalf("arranque abortado: %v", err)
	}
	defer db.Close()

	rdb, err := newRedisClient(ctx, cfg.redisAddr)
	if err != nil {
		log.Fatalf("arranque abortado: %v", err)
	}
	defer rdb.Close()

	origin := NewOrigin(db, cfg.originDelay)
	if err := origin.EnsureSchema(ctx); err != nil {
		log.Fatalf("no se pudo crear el esquema: %v", err)
	}
	if err := origin.Seed(ctx); err != nil {
		log.Fatalf("no se pudieron sembrar las páginas: %v", err)
	}

	edge := NewEdge(rdb, db, origin, cfg.edgeDelay, cfg.cacheTTL)
	go edge.RunLogWriter(ctx)
	// ¿Por qué este defer y no una llamada al final? Porque Shutdown(grace) espera a los
	// handlers, y recién después queremos cerrar el canal y drenar lo que quedó en él.
	defer edge.Close()

	mux := http.NewServeMux()
	// Ojo: Go 1.22 acepta métodos en el patrón. `/pagina` y `/pagina/{slug}` son dos rutas
	// distintas a propósito: la primera usa la key literal cache:/pagina del enunciado;
	// la segunda cubre las otras cuatro páginas con cache:/pagina/<slug>.
	mux.HandleFunc("GET /pagina", edge.ServePage)
	mux.HandleFunc("GET /pagina/{slug}", edge.ServePage)
	mux.HandleFunc("GET /api/pages", origin.ServePages)
	mux.HandleFunc("GET /api/stats", edge.ServeStats)
	mux.HandleFunc("POST /api/clear-cache", edge.ServeClearCache)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           cors(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("edge escuchando en %s (origen=%v, edge=%v, TTL=%v)", srv.Addr, cfg.originDelay, cfg.edgeDelay, cfg.cacheTTL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("el servidor HTTP murió: %v", err)
		}
	}()

	<-shutdown
	grace, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		log.Printf("shutdown con warnings: %v", err)
	}
}
