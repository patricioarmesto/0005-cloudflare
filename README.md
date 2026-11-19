# ¿Cómo funciona Cloudflare?

Ejercicio 0005 de 1024 del curso **¿Cómo funciona...?**. Nivel: Inicial · Semana 5 del ciclo.

Un prototipo funcional de CDN con edge cache donde la diferencia entre un HIT y un MISS se ve, se mide y se puede provocar con un botón.

## Qué resuelve

Si un usuario en Buenos Aires pide una página alojada en un origen en Virginia, cada request cruza el continente: ~200 ms de ida y vuelta. Cloudflare pone una copia de esa página en un POP (*point of presence*) a pocos kilómetros del usuario. La primera request paga el viaje hasta el origen (**MISS**); las siguientes se sirven desde la RAM del edge (**HIT**, ~5 ms).

Este ejercicio hace visible ese mecanismo con cuatro piezas observables:

1. **HIT vs MISS en la respuesta.** Cada `GET /pagina` devuelve el header `X-Cache` y un campo `cache` en el body. El primer pedido es MISS, los 99 siguientes son HIT.
2. **Latencia con dos regímenes.** El edge simulado agrega 5 ms al camino caliente; el origen agrega 200 ms antes de responder. La diferencia es del orden de 40x y se grafica en vivo.
3. **Estado durable de lo efímero.** El contenido cacheado vive en Redis (con TTL de 60 s), pero cada HIT y cada MISS se registran en Postgres para poder calcular el hit rate.
4. **Control manual.** `POST /api/clear-cache` borra las claves `cache:/pagina*` y deja el próximo pedido en MISS, para repetir el experimento las veces que quieras.

No hay DNS real ni DDoS real: se simulan con un header y con un botón. Están documentados en los ADRs.

## Cómo correr

Requiere Docker con Compose v2 y el daemon corriendo. Un solo comando deja todo andando:

```bash
docker compose up
```

La primera vez Compose construye las imágenes del backend y del frontend; en las siguientes reutiliza la caché de Docker.

Cuando los healthchecks estén verdes:

- Frontend: http://localhost:5177
- API / Edge: http://localhost:8083
- Health: http://localhost:8083/health

Los puertos de host están desplazados (5177, 8083, 5436, 6382) para poder correr varios ejercicios del curso al mismo tiempo. Dentro de la red de Compose los servicios usan los puertos estándar (`backend:8080`, `postgres:5432`, `redis:6379`).

Para reiniciar desde cero, incluidos los volúmenes:

```bash
docker compose down -v && docker compose up
```

El backend crea el esquema y siembra las 5 páginas al arrancar (`CREATE TABLE IF NOT EXISTS` + `ON CONFLICT DO NOTHING`). No hay migraciones ni pasos manuales.

## Arquitectura

```
                       ┌──────────────────────────────┐
   Navegador (React)──▶│ frontend (nginx :80 → :5177) │
                       │ /api/* y /pagina/* → backend │
                       └──────────────┬───────────────┘
                                      │
                       ┌──────────────▼───────────────┐
                       │ edge (Go :8080 → :8083)      │
                       │ GET /pagina, /api/stats,     │
                       │ /api/clear-cache             │
                       └───┬──────────────────────┬───┘
                           │ GET cache:/pagina/*  │ 200 ms si no está
                  ┌────────▼────────┐    ┌────────▼──────────────┐
                  │ Redis :6379     │    │ origin (Go, mismo bin)│
                  │ pages en JSON   │    │ sleep 200 ms + SELECT │
                  │ TTL 60 s        │    │ desde Postgres        │
                  └─────────────────┘    └────────┬───────────────┘
                                                 │
                       ┌─────────────────────────▼───────────────┐
                       │ Postgres :5432                          │
                       │ pages (5 filas) + cache_logs (HIT/MISS) │
                       └─────────────────────────────────────────┘
```

Separación de responsabilidades: `edge.go` decide entre Redis y el origen; `origin.go` es el único que lee las páginas y duerme 200 ms; `main.go` arma el wiring. Nginx no cachea nada: sólo sirve el build de Vite y hace de proxy para que el browser use rutas relativas. El log de cada request se escribe en Postgres desde un canal con un worker separado, para que la escritura no contamine la latencia medida del HIT.

## Flujo

1. `GET /pagina` → el edge calcula la key `cache:/pagina` y hace `GET` a Redis. Es la key literal del enunciado; para las otras cuatro páginas la key es `cache:/pagina/<slug>`.
2. **HIT:** Redis devuelve el JSON. El edge agrega 5 ms (viaje simulado POP→usuario), setea `X-Cache: HIT` y `X-Edge-Pop`, y responde. No toca Postgres para servir el contenido.
3. **MISS:** Redis devuelve `nil`. El edge llama al origen, que duerme 200 ms y lee la página de Postgres. Guarda el JSON en Redis con TTL de 60 s, setea `X-Cache: MISS` y responde.
4. Cada respuesta encola un registro `{slug, HIT|MISS, latency_ms, pop}` en un canal; un worker lo inserta en `cache_logs` fuera del camino caliente.
5. `POST /api/clear-cache` recorre con `SCAN` las claves `cache:/pagina*` y las borra. Los logs históricos quedan intactos: así se ve el nuevo MISS al lado de los HIT anteriores.
6. `GET /api/stats` devuelve `hits`, `misses`, `total`, `hit_rate`, el desglose por POP y los últimos 12 eventos.
7. El botón **Pedir 100 veces** lanza 100 requests secuenciales. El primero es MISS si la caché está fría; los 99 restantes son HIT. El panel va actualizando latencia y contadores de a diez pedidos.

## Stack

Solo tecnologías presentes en `docker-compose.yml`.

| Tecnología | Rol | Servicio en Compose |
| --- | --- | --- |
| Go | Edge, origen, handlers de stats y limpieza de caché | `backend` |
| React | Lista de páginas, panel de latencia y botones | `frontend` |
| Vite | Bundler y servidor de desarrollo del frontend | `frontend` |
| Tailwind CSS | Estilos utilitarios del panel | `frontend` |
| PostgreSQL | Páginas (fuente de verdad) y logs de HIT/MISS | `postgres` |
| Redis | Caché del edge con TTL de 60 s | `redis` |
| nginx | Servidor estático y proxy de `/pagina` y `/api` | `frontend` |
| Docker | Build multi-stage de backend y frontend | builds de `backend` / `frontend` |
| Docker Compose | Orquestación y healthchecks de un solo comando | `docker-compose.yml` |

## Decisiones clave

- **Redis es la caché, Postgres es la fuente de verdad.** El contenido se sirve desde Redis; si Redis falla, el edge va al origen y sigue respondiendo. Ver [ADR-001](docs/adr/0001-cache-en-redis.md).
- **TTL de 60 segundos.** Suficiente para que el hit rate sea alto durante la demo y lo bastante corto para que la expiración sea observable. Ver [ADR-002](docs/adr/0002-ttl-60s.md).
- **Edge y origen son dos roles explícitos en el mismo binario.** Los separamos por archivo y por delays simulados, no por dos contenedores: el alumno ve el contraste sin formalizar una topología distribuida. Ver [ADR-003](docs/adr/0003-edge-vs-origen.md).
- **DNS simulado con un header.** `X-User-Region` se traduce a un POP (EZE, SCL, MEX, MIA) con un mapa en memoria. Ver [ADR-004](docs/adr/0004-dns-mock.md).
- **Sin DDoS real.** "Pedir 100 veces" es carga sintética y determinista, no un ataque. Ver [ADR-005](docs/adr/0005-sin-ddos-real.md).
- **Logs fuera del camino caliente.** Un canal buffered y un worker escriben en Postgres; si el canal se llena se descarta el log, nunca la respuesta. Por eso el HIT da ~5 ms y no ~7 ms.
- **`/api/stats` espera a que se drenen los logs.** Antes de consultar, el edge manda una marca FIFO por el mismo canal y espera a que el worker la procese. Así el hit rate del panel incluye el último pedido sin haber metido un `INSERT` sincrónico en el camino del HIT.
- **Los 100 pedidos son secuenciales.** Si se lanzaran en paralelo sobre una key fría, varios requests verían el MISS antes de que el primero poblara Redis (*cache stampede*) y el reparto 1/99 dejaría de ser determinista. El botón dispara uno por uno para aislar el efecto que se estudia; el coalescing de requests queda fuera de alcance.

## Alcance y decisiones de diseño

El ejercicio corre una sola réplica del edge y una sola del origen, en el mismo proceso Go. La latencia de 5 ms y la de 200 ms son delays artificiales elegidos para hacer visible el contraste, no mediciones de red. El TTL se aplica con `SET ... EX 60` y no existe invalidación por webhook: para forzar un MISS se llama al endpoint de limpieza. El POP se elige con un header, sin GeoIP. El log se escribe de forma asíncrona y el hit rate se calcula con agregaciones simples de Postgres. Todo lo que no figura en esta lista queda fuera de alcance a propósito para mantener el ejercicio legible. No son omisiones por desconocimiento.

## Qué NO implementamos

- **DNS real:** no hay zonas, CNAMEs, anycast ni resolvers. El "DNS" es un `map[string]string` en `edge.go`.
- **DDoS real:** no hay flood, rate limiting, challenge JS ni mitigación de volumetric attacks.
- **WAF:** no hay inspección de body, reglas de SQLi/XSS ni bloqueo por IP.
- **TLS:** no hay certificados ni HTTPS; el tráfico local va por HTTP.
- **Kafka:** no hay cola de eventos; los HIT/MISS se insertan directo en Postgres desde un worker.
- **ClickHouse:** no hay analítica columnar; el hit rate sale de un `GROUP BY` en Postgres.
- **Qdrant:** no hay búsqueda vectorial; no hay embeddings en este ejercicio.
- **Ollama:** no hay modelo local ni inferencia; el contenido son 5 filas de texto sembradas al arrancar.

## Cómo probar

**1. Primer MISS y segundo HIT (con headers):**

```bash
curl -si http://localhost:8083/pagina | grep -i '^X-Cache\|^X-Edge-Pop'
# X-Cache: MISS   (primera vez, ~200 ms)

curl -si http://localhost:8083/pagina | grep -i '^X-Cache\|^X-Edge-Pop'
# X-Cache: HIT    (~5 ms)
```

También podés ver el body completo con `curl -s http://localhost:8083/pagina | python3 -m json.tool`: trae `cache`, `pop`, `latency_ms`, `cache_key` y `ttl_seconds`.

**2. Las 5 páginas y el key por slug:**

```bash
curl -s http://localhost:8083/api/pages | python3 -m json.tool
curl -s http://localhost:8083/pagina/precios | python3 -m json.tool | grep cache_key
# "cache_key": "cache:/pagina/precios"
```

**3. Ver el TTL en Redis:**

```bash
docker compose exec redis redis-cli TTL 'cache:/pagina'
# 60 → baja hasta expirar; después el próximo GET vuelve a ser MISS
```

**4. Limpiar la caché y confirmar el nuevo MISS:**

```bash
curl -s -X POST http://localhost:8083/api/clear-cache
# {"deleted":1}

curl -si http://localhost:8083/pagina | grep -i '^X-Cache'
# X-Cache: MISS
```

**5. Estadísticas desde Postgres:**

```bash
curl -s http://localhost:8083/api/stats | python3 -m json.tool
docker compose exec postgres psql -U app -d cloudflare \
  -c "SELECT result, count(*), round(avg(latency_ms),1) FROM cache_logs GROUP BY result;"
```

**6. Desde la UI:**

Abrí http://localhost:5177, elegí una página y apretá **Pedir 1 vez** para ver el primer MISS. Después apretá **Limpiar cache** y **Pedir 100 veces**: el panel muestra 1 MISS y 99 HIT, barras de latencia en vivo, el hit rate por POP con Recharts y el log reciente. Cambiar la región en el selector cambia el POP devuelto sin cambiar el contenido cacheado.

## ADRs

- [ADR-001 — Cache en Redis](docs/adr/0001-cache-en-redis.md)
- [ADR-002 — TTL de 60 segundos](docs/adr/0002-ttl-60s.md)
- [ADR-003 — Edge vs origen](docs/adr/0003-edge-vs-origen.md)
- [ADR-004 — DNS mock](docs/adr/0004-dns-mock.md)
- [ADR-005 — Sin DDoS real](docs/adr/0005-sin-ddos-real.md)

## 【entity-GitHub¦canonical_name=GitHub】 - Descripción y Topics

**Descripción:**

¿Cómo funciona Cloudflare? CDN y edge cache con Redis. Go + React + Postgres. Ejercicio 0005 de 1024 del curso ¿Cómo funciona...?

**Topics (en este orden):**

`system-design`, `cdn`, `edge`, `cache`, `dns`, `go`, `react`, `vite`, `tailwindcss`, `docker`, `docker-compose`, `postgres`, `redis`, `cloudflare`, `como-funciona`

Comando para aplicarlos:

```bash
gh repo edit \
  --description "¿Cómo funciona Cloudflare? CDN y edge cache con Redis. Go + React + Postgres. Ejercicio 0005 de 1024 del curso ¿Cómo funciona...?" \
  --add-topic system-design \
  --add-topic cdn \
  --add-topic edge \
  --add-topic cache \
  --add-topic dns \
  --add-topic go \
  --add-topic react \
  --add-topic vite \
  --add-topic tailwindcss \
  --add-topic docker \
  --add-topic docker-compose \
  --add-topic postgres \
  --add-topic redis \
  --add-topic cloudflare \
  --add-topic como-funciona
```
