# ADR-001: Cache en Redis en lugar de Postgres
- Ejercicio: 0005 de 1024 · Fecha: 2026-11-19 · Estado: Aceptado

## Contexto

El edge debe responder en ~5 ms. Postgres guarda las cinco páginas y es la fuente de verdad, pero cada `SELECT` cuesta un round trip, un plan de query y un pool. Si el camino caliente consultara Postgres en cada request, no existiría la diferencia HIT/MISS que queremos enseñar. Además, el contenido cacheable tiene una forma muy simple: tres strings por slug.

## Decisión

Redis guarda el JSON de cada página bajo `cache:/pagina` (home) y `cache:/pagina/<slug>` (resto). `edge.go` hace un `GET` y, si existe, devuelve sin tocar Postgres. Ante `redis.Nil` o ante un error de Redis, va al origen, obtiene la página y la escribe con `SET ... EX 60`. Postgres queda reservado para las páginas y para el log de HIT/MISS.

## Consecuencias (A favor / En contra)

A favor:
- El camino del HIT es una sola operación en memoria, con latencia predecible.
- Redis es un subsistema separado: se puede caer y el edge degrada a MISS sin dejar de responder.
- La clave `cache:/pagina*` es agrupable, así que limpiar la caché es un `SCAN` + `DEL` acotado.
- El JSON guardado es el contrato entre `origin.go` y `edge.go`; no hay serialización especial.

En contra:
- Existe una ventana de hasta 60 s donde Redis y Postgres pueden divergir si cambia una página.
- Aparece un servicio más en Compose y un Ping obligatorio en el arranque.
- Un JSON corrupto en Redis obliga a revalidar contra el origen; hoy se detecta y se repuebla.

## Alternativas consideradas

- **Consultar Postgres en cada request**: elimina Redis, pero HIT y MISS costarían lo mismo y el ejercicio perdería su sentido.
- **Caché en memoria del proceso Go**: más rápido aún, pero se pierde al reiniciar y no representa un POP compartido por muchos usuarios.
- **Un CDN administrado real**: resuelve el problema de producción, pero oculta justo el mecanismo que queremos explicar.

## Referencias

- Implementación: `backend/edge.go` (`ServePage`, `ServeClearCache`).
- Flujo: [README.md § Flujo](../../README.md).
- Decisión complementaria: [ADR-002 — TTL de 60 segundos](0002-ttl-60s.md).
