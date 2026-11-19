// edge_test.go — Tests de la lógica del edge que no necesita servicios externos.
// ¿Por qué estos tests y no un mock de Postgres? Porque queremos verificar las reglas
// que se pueden equivocar sin base: el cálculo del hit rate y la sincronización del
// log writer. Las queries se cubren con el smoke test de Compose del README.
package main

import (
	"context"
	"database/sql"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

// TestHitRate existe porque la misma división aparece dos veces en ServeStats (total y
// por POP). Si un día alguien cambia una y no la otra, el panel miente y nadie se entera.
func TestHitRate(t *testing.T) {
	cases := []struct {
		name     string
		hits     int64
		total    int64
		expected float64
	}{
		{name: "sin eventos", hits: 0, total: 0, expected: 0},
		{name: "todo hits", hits: 5, total: 5, expected: 100},
		{name: "uno de tres", hits: 1, total: 3, expected: 100.0 / 3.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hitRate(tc.hits, tc.total)
			if diff := got - tc.expected; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("hitRate(%d, %d) = %v, esperado %v", tc.hits, tc.total, got, tc.expected)
			}
		})
	}
}

// TestLogWriterCloseReturns existe porque el shutdown tenía un bug silencioso: Close()
// cerraba el canal pero el worker no detectaba el cierre y seguía insertando filas
// vacías en un loop infinito. ¿Por qué se ve acá y no en Compose? Porque `docker compose
// down` parecía colgar sin mensaje de error; el test lo vuelve determinista.
func TestLogWriterCloseReturns(t *testing.T) {
	previousOutput := log.Writer()
	log.SetOutput(io.Discard) // Ojo: sin esto el loop del bug imprime miles de errores.
	defer log.SetOutput(previousOutput)

	// ¿Por qué un DSN que apunta a un puerto cerrado? Porque con el bug el worker
	// intenta insertar filas vacías; queremos que falle rápido, no que espere 30 s.
	db, err := sql.Open("postgres", "postgres://app:app@127.0.0.1:1/cloudflare?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("no se pudo abrir la DB de test: %v", err)
	}
	defer db.Close()

	edge := NewEdge(nil, db, nil, 0, 0)
	go edge.RunLogWriter(context.Background())

	finished := make(chan struct{})
	go func() {
		edge.Close()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Close se colgó: el worker nunca terminó al cerrar el canal")
	}
}

// TestCloseDrainsPendingLogs existe porque el apagón no sólo debe terminar: tiene que
// conservar los eventos ya encolados. Si canceláramos el contexto antes de cerrar el
// canal, el último HIT/MISS se perdería justo cuando el alumno aprieta Ctrl-C.
func TestCloseDrainsPendingLogs(t *testing.T) {
	var mu sync.Mutex
	inserted := make([]cacheLog, 0, 2)

	edge := &Edge{
		logCh: make(chan logEvent, 2),
		done:  make(chan struct{}),
		insert: func(_ context.Context, entry cacheLog) error {
			mu.Lock()
			defer mu.Unlock()
			inserted = append(inserted, entry)
			return nil
		},
	}
	go edge.RunLogWriter(context.Background())

	edge.record(cacheLog{slug: "inicio", result: "HIT", latencyMS: 5, pop: "EZE"})
	edge.record(cacheLog{slug: "precios", result: "MISS", latencyMS: 200, pop: "SCL"})

	finished := make(chan struct{})
	go func() {
		edge.Close()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Close no drenó los eventos pendientes")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(inserted) != 2 {
		t.Fatalf("esperaba 2 inserts, hubo %d", len(inserted))
	}
	if inserted[0].result != "HIT" || inserted[1].result != "MISS" {
		t.Fatalf("el orden de los logs cambió: %+v", inserted)
	}
}

// TestFlushWaitsForPendingLogs existe porque /api/stats puede pedirse apenas termina la
// tanda: sin la barrera el último evento todavía estaría en el canal y el hit rate del
// panel quedaría un número abajo. El insert se bloquea a propósito para hacer visible
// que Flush espera, en vez de devolver apenas encola la marca.
func TestFlushWaitsForPendingLogs(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }

	edge := &Edge{
		logCh: make(chan logEvent, 4),
		done:  make(chan struct{}),
		insert: func(_ context.Context, entry cacheLog) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return nil
		},
	}
	go edge.RunLogWriter(context.Background())
	defer func() {
		unblock()
		edge.Close()
	}()

	edge.record(cacheLog{slug: "inicio", result: "MISS", latencyMS: 200, pop: "EZE"})
	<-started // El worker está ocupado: acá es donde Flush NO debe terminar.

	flushed := make(chan error, 1)
	go func() { flushed <- edge.Flush(context.Background()) }()

	select {
	case err := <-flushed:
		t.Fatalf("Flush terminó antes de insertar el log pendiente: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	unblock()
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatalf("Flush devolvió error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Flush no terminó después de drenar el canal")
	}
}
