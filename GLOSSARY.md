# CDN con edge cache (ejercicio 0005)

Contexto de enseñanza que hace observable la decisión de caché de un CDN: un edge sirve a los usuarios desde una copia cercana y un origin entrega el contenido cuando no hay copia válida. El experimento mide esa decisión observando el paso de MISS a HIT, la latencia resultante y el POP que atendió cada petición.

## Language

### Core CDN/cache

**edge**:
Capa del CDN que responde a los usuarios desde una copia cercana del contenido, decidiendo en cada petición entre servirla desde la caché o solicitarla al origin.
_Avoid_: proxy, frontend, POP (cuando se refiere al rol).

**POP**:
Punto de presencia: sitio concreto del edge, identificado por un código, desde el que se atiende una petición. Las copias cacheadas se comparten entre POPs, de modo que el contenido no depende del sitio que responde.
_Avoid_: región, edge (cuando se refiere al sitio).

**origin**:
Rol que entrega el contenido original al edge cuando no existe una copia válida en la caché.
_Avoid_: backend, servidor, source of truth.

**source of truth**:
Almacén autoritativo del contenido del que parte el origin para entregarlo al edge.
_Avoid_: caché, origin, copia cacheada.

**cache key**:
Identificador bajo el cual se guarda una copia del contenido; en este contexto es independiente del POP.
_Avoid_: clave de caché, identificador de página.

**HIT**:
Respuesta servida desde la copia cacheada del edge, sin intervención del origin.
_Avoid_: acierto, cache hit.

**MISS**:
Respuesta para la que el edge no encuentra una copia válida y debe pedir el contenido al origin.
_Avoid_: fallo, cache miss.

**hit rate**:
Proporción de HIT respecto del total de peticiones observadas en un período o en un conjunto de eventos.
_Avoid_: tasa de aciertos, hit_rate.

**TTL**:
Tiempo durante el cual una copia cacheada se considera válida antes de expirar.
_Avoid_: expiración, caducidad.

**freshness**:
Grado en que la copia cacheada coincide con la versión actual del contenido en la source of truth.
_Avoid_: frescura, actualidad.

**invalidation**:
Acción de retirar contenido cacheado que puede haber quedado obsoleto, habitualmente por objeto o mediante eventos.
_Avoid_: limpieza de caché, purge.

**cache clear**:
Operación manual y global que retira todas las copias cacheadas sin distinguir páginas ni POPs.
_Avoid_: invalidación, reset.

**hot path**:
Trayecto de una petición que se resuelve íntegramente en la caché del edge.
_Avoid_: camino caliente, fast path.

**cold path**:
Trayecto de una petición que llega al origin por no existir una copia válida en la caché.
_Avoid_: camino frío, slow path.

### Experiment & observability

**experiment**:
Secuencia acotada de peticiones, partiendo de la caché fría, con la que se observa el paso de MISS a HIT.
_Avoid_: prueba, test, carga.

**burst**:
Tanda de peticiones secuenciales lanzada de una sola vez para poblar la caché.
_Avoid_: ráfaga, ataque, DDoS.

**cache event**:
Registro de una petición que conserva su resultado (HIT o MISS), el POP que la atendió y la latencia observada.
_Avoid_: log, cache_logs.

**observed latency**:
Tiempo que tardó una petición en resolverse, empleado para contrastar el costo del edge y del origin.
_Avoid_: latency_ms, delay.

**per-POP hit rate**:
hit rate calculado únicamente sobre los eventos correspondientes a un POP.
_Avoid_: hit rate por región, tasa por región.

### Simulated geography

**region**:
Origen geográfico declarado por el llamador y utilizado como entrada para resolver un POP.
_Avoid_: POP, ubicación real.

**POP resolution**:
Traducción de una region declarada a un POP concreto.
_Avoid_: resolución DNS, GeoIP, anycast.

**fallback**:
Comportamiento por defecto que asigna el POP por defecto (EZE en este contexto) cuando la region no puede resolverse.
_Avoid_: default, failover.
