# ADR-004: DNS simulado con un header en lugar de resolución real
- Ejercicio: 0005 de 1024 · Fecha: 2026-11-19 · Estado: Aceptado

## Contexto

Parte del valor de Cloudflare viene de su red de POPs: el mismo nombre de host resuelve a un punto cercano al usuario. Reproducir eso en local exigiría un resolver, zonas DNS, anycast o cuando menos un proxy con GeoIP. Ninguna de esas piezas aporta a la idea de "edge cache" que se enseña en esta semana, y todas hacen más difícil correr el ejercicio con un solo comando.

## Decisión

El browser envía el header `X-User-Region` (`buenos-aires`, `santiago`, `mexico`, `miami`). Un mapa en memoria en `edge.go` traduce ese valor a un POP (`EZE`, `SCL`, `MEX`, `MIA`), que se devuelve en el header `X-Edge-Pop` y en el campo `pop` del body. Ante un valor desconocido se usa `EZE` como default. El contenido cacheado NO depende del POP: las keys son las mismas para todos, como ocurre cuando un CDN replica la misma versión de un objeto.

## Consecuencias (A favor / En contra)

A favor:
- Cero infraestructura extra: el DNS vive en tres líneas de Go y se explica en una mirada.
- El panel puede cambiar de región y ver otro POP sin tocar Redis ni Postgres.
- El hit rate por POP sale de `GROUP BY pop` en `cache_logs`, con datos reales del experimento.
- No hay dependencia de una base GeoIP ni de una API externa de localización.

En contra:
- No mide latencia geográfica real: EZE y MIA tienen el mismo delay simulado de 5 ms.
- No hay CNAME, TTL de DNS ni failover entre POPs; son conceptos deliberadamente omitidos.
- Un valor mal escrito en el header cae silenciosamente al default EZE.

## Alternativas consideradas

- **CoreDNS o un resolver local**: fiel a DNS, pero suma un contenedor y configuración de zonas que no se usa en ningún otro punto del ejercicio.
- **GeoIP a partir de la IP del cliente**: más realista, pero requiere una base de datos y no funciona bien con `localhost`.
- **Fijar un único POP sin input del usuario**: más simple, pero se pierde la posibilidad de comparar hit rates por región en el gráfico.

## Referencias

- Implementación: `backend/edge.go` (`popByRegion`, `resolvePOP`, `ServePage`).
- UI: `frontend/src/App.tsx` (selector de región y gráfico por POP).
- Decisión complementaria: [ADR-003 — Edge vs origen](0003-edge-vs-origen.md).
