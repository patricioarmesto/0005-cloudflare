# ADR-003: Edge y origen como roles separados en el mismo binario
- Ejercicio: 0005 de 1024 · Fecha: 2026-11-19 · Estado: Aceptado

## Contexto

La idea central de un CDN es que dos lugares distintos atienden la misma request: uno cerca del usuario y otro con el contenido original. Para enseñarlo necesitamos que la diferencia sea medible y que el código muestre dónde está cada responsabilidad. Al mismo tiempo, un ejercicio de nivel inicial no debería exigir gestionar dos servicios desplegables, dos healthchecks de aplicación ni un balanceador.

## Decisión

Un único proceso Go contiene dos roles separados por archivo. `edge.go` decide entre Redis y el origen, setea `X-Cache` y mide latencia; `origin.go` duerme 200 ms y lee la página de Postgres. Los delays son env vars (`EDGE_DELAY_MS=5`, `ORIGIN_DELAY_MS=200`). El header `X-Cache` y los campos `latency_ms` y `cache_key` del body hacen observable la decisión en cada request.

## Consecuencias (A favor / En contra)

A favor:
- `docker compose up` levanta un solo backend; no hay que explicar descubrimiento de servicios entre dos binarios.
- Los 5 ms y los 200 ms son elegidos, no medidos: el contraste es reproducible en cualquier laptop.
- Separar por archivo deja claro qué código es del POP y qué código es del origen.
- El origen puede caer (o demorarse) y el edge mantiene sirviendo HIT desde Redis.

En contra:
- No es una topología distribuida real: los dos roles comparten proceso y red de contenedor.
- Los delays son artificiales; no sirven para evaluar performance de Go ni de Redis.
- Un reinicio del backend reinicia ambos roles a la vez, algo que no pasaría en producción.

## Alternativas consideradas

- **Un solo handler con todo mezclado**: menos archivos, pero el alumno no vería dónde nace la latencia del origen.
- **Dos contenedores Go distintos (edge y origin)**: más fiel a producción, pero agrega configuración de red que no aporta al objetivo de la semana 5.
- **Latencia real de red entre regiones**: imposible de garantizar en local y dependiente del proveedor de cloud.

## Referencias

- Implementación: `backend/edge.go`, `backend/origin.go`, `backend/main.go` (wiring).
- Flujo: [README.md § Flujo](../../README.md).
- Decisión complementaria: [ADR-004 — DNS mock](0004-dns-mock.md).
