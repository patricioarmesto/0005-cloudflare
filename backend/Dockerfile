# Dockerfile del backend: compila un binario Go estático y lo corre en una imagen mínima.
# ¿Por qué dos stages? Para que la imagen final no arrastre el toolchain de Go (≈1 GB menos).

FROM golang:1.22-alpine AS build
WORKDIR /src

# Copiamos el manifest antes que el código para aprovechar la caché de capas de Docker.
# Ojo: el go.sum va junto con el go.mod; sin él, `go mod download` no puede verificar hashes.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /edge .

# CGO_ENABLED=0: lib/pq es puro Go, así que no necesitamos gcc en la imagen final.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /edge /usr/local/bin/edge
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/edge"]
