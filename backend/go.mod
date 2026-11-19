// go.mod — Declara el módulo Go del ejercicio y fija las dos dependencias del backend:
// lib/pq (driver de Postgres) y go-redis (cliente del edge cache). ¿Por qué va el
// comentario acá? Porque el enunciado pide que cada archivo diga qué hace en el sistema
// y este formato admite comentarios con //. El go.sum complementa este archivo.
module comofunciona/0005-cloudflare

go 1.22

require (
	github.com/lib/pq v1.10.9
	github.com/redis/go-redis/v9 v9.5.1
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)
