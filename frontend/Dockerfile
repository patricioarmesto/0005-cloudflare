# Dockerfile del frontend: compila TypeScript + Vite y entrega el resultado con nginx.
# ¿Por qué multi-stage? El usuario final sólo necesita el HTML/JS estático, no Node ni npm.

FROM node:20-alpine AS build
WORKDIR /app

# Copiamos los manifests primero para cachear `npm ci` mientras cambia el código fuente.
# ¿Por qué npm ci y no npm install? El lockfile fija versiones exactas: dos builds del
# ejercicio en días distintos deben dar el mismo bundle. Si hacés esto distinto (borrar
# el lockfile), una actualización de Recharts puede romper el gráfico sin avisar.
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
