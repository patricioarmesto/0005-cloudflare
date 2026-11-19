# ADR-005: Sin DDoS real, WAF ni mitigación de tráfico malicioso
- Ejercicio: 0005 de 1024 · Fecha: 2026-11-19 · Estado: Aceptado

## Contexto

Cloudflare es muy conocido por absorber ataques DDoS y por filtrar tráfico malicioso en el edge. Es tentador querer demostrar eso también, pero un ataque real necesita generación de carga distribuida, rate limiting, challenges y observabilidad de red. Nada de eso cambia lo que ocurre con una key de caché, que es el tema de la semana 5.

## Decisión

La carga del ejercicio es sintética y acotada: el botón **Pedir 100 veces** lanza 100 requests secuenciales. No hay rate limiting, no hay WAF, no hay TLS, no hay challenge JS y no hay inspección de payloads. El único efecto medible es el paso de MISS a HIT en Redis y su reflejo en `cache_logs`.

## Consecuencias (A favor / En contra)

A favor:
- El experimento es determinista: 1 MISS y 99 HIT con la caché fría, siempre.
- No se necesitan herramientas de stress test ni permisos especiales para correrlo.
- El código se concentra en la decisión de caché, que es el objetivo de aprendizaje.
- No hay falsa sensación de seguridad: el README declara explícitamente lo que no existe.

En contra:
- El prototipo no es apto para exponer a Internet: cualquier cliente puede llenar Redis y Postgres.
- No se muestran cabeceras propias de mitigación ni reglas de firewall.
- Un alumno que espere "ver un ataque" no lo encontrará; el README y esta ADR lo aclaran.

## Alternativas consideradas

- **Rate limiting en nginx**: fácil de agregar con `limit_req`, pero desplazaría el foco a 429s y a ventanas de tiempo.
- **WAF con reglas OWASP**: otro servicio y muchas reglas que no interactúan con la caché.
- **Generador de carga distribuido**: útil para medir capacity, no para explicar por qué el segundo request es más rápido.

## Referencias

- Implementación: `frontend/src/App.tsx` (`askHundred`), `backend/edge.go` (`ServePage`).
- Alcance: [README.md § Qué NO implementamos](../../README.md).
- Decisión complementaria: [ADR-001 — Cache en Redis](0001-cache-en-redis.md).
