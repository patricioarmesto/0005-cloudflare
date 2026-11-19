// origin.go — Representa el servidor de origen. Es el dueño de las 5 páginas y el único
// que las lee de Postgres. Simula 200 ms de latencia para encarnar "el origen está lejos".
package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// Page es la unidad de contenido que el edge guarda en Redis. ¿Por qué un tipo chico y
// explícito? Porque es el contrato entre origin.go y edge.go: si crece, el JSON cacheado
// crece con él y hay que acordarse de invalidar las keys viejas.
type Page struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Origin existe porque el edge necesita un único lugar al que pedir contenido cuando
// no lo tiene en caché. Separarlo del edge permite que el sleep de 200 ms no se mezcle
// con la lógica de Redis.
type Origin struct {
	db    *sql.DB
	delay time.Duration
}

// NewOrigin existe para inyectar el delay desde la config en vez de hardcodearlo:
// así el mismo código sirve para mostrar 200 ms, 500 ms o 1 s de "distancia".
func NewOrigin(db *sql.DB, delay time.Duration) *Origin {
	return &Origin{db: db, delay: delay}
}

// EnsureSchema existe porque este es el primer despliegue del ejercicio: no queremos un
// sistema de migraciones para dos tablas. IF NOT EXISTS hace que reiniciar sea idempotente.
func (o *Origin) EnsureSchema(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS pages (
    slug       TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cache_logs (
    id         BIGSERIAL PRIMARY KEY,
    slug       TEXT        NOT NULL,
    result     TEXT        NOT NULL CHECK (result IN ('HIT', 'MISS')),
    latency_ms INTEGER     NOT NULL,
    pop        TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cache_logs_created_at ON cache_logs (created_at DESC);
`
	_, err := o.db.ExecContext(ctx, schema)
	return err
}

// Seed existe para que el alumno abra la app y ya tenga contenido. ON CONFLICT DO NOTHING
// es clave: si reiniciás el contenedor, los logs de HIT/MISS se conservan y las páginas no
// se duplican. Si hacés esto distinto (INSERT a secas), el segundo `up` ya falla.
func (o *Origin) Seed(ctx context.Context) error {
	pages := []Page{
		{Slug: "inicio", Title: "Inicio", Body: "Bienvenido al sitio de ejemplo. Esta página viajó desde el origen hasta el edge."},
		{Slug: "precios", Title: "Precios", Body: "Plan gratuito, plan Pro y plan Empresa. Los precios viven en Postgres, no en el browser."},
		{Slug: "blog", Title: "Blog", Body: "Notas sobre redes, cachés y por qué milisegundos importan."},
		{Slug: "docs", Title: "Documentación", Body: "Guías de integración. El primer acceso es MISS; los siguientes son HIT."},
		{Slug: "nosotros", Title: "Nosotros", Body: "Somos un ejemplo didáctico: un edge, un origen y dos almacenes."},
	}

	stmt, err := o.db.PrepareContext(ctx, `INSERT INTO pages (slug, title, body) VALUES ($1, $2, $3) ON CONFLICT (slug) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, page := range pages {
		if _, err := stmt.ExecContext(ctx, page.Slug, page.Title, page.Body); err != nil {
			return err
		}
	}
	return nil
}

// Fetch existe para simular el costo de ir hasta el origen. El sleep va ANTES del SELECT:
// en la vida real ese tiempo es RTT + cola + disco, y queremos que el alumno lo sienta como
// una sola espera. ¿Por qué respetar ctx.Done()? Para que un usuario que corta la request
// no deje goroutines durmiendo 200 ms sin sentido.
func (o *Origin) Fetch(ctx context.Context, slug string) (Page, error) {
	timer := time.NewTimer(o.delay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
		return Page{}, ctx.Err()
	}

	var page Page
	err := o.db.QueryRowContext(ctx, `SELECT slug, title, body FROM pages WHERE slug = $1`, slug).
		Scan(&page.Slug, &page.Title, &page.Body)
	return page, err
}

// List existe para poblar la lista de 5 páginas del frontend sin duplicar el arreglo del Seed.
// Ojo: si agregás una página acá pero no en Seed, el botón propone un slug que el origen no tiene.
func (o *Origin) List(ctx context.Context) ([]Page, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT slug, title, body FROM pages ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := make([]Page, 0, 5)
	for rows.Next() {
		var page Page
		if err := rows.Scan(&page.Slug, &page.Title, &page.Body); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, rows.Err()
}

// ServePages existe para exponer el catálogo al frontend. No pasa por Redis a propósito:
// es data de control, no contenido cacheable, y queremos que el panel muestre las 5 reales.
func (o *Origin) ServePages(w http.ResponseWriter, r *http.Request) {
	pages, err := o.List(r.Context())
	if err != nil {
		http.Error(w, `{"error":"no se pudieron leer las páginas"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pages)
}
