// vite.config.ts — Configura el bundler del frontend.
// ¿Por qué host 0.0.0.0? En `npm run dev` desde la laptop no hace falta, pero dentro de un
// contenedor Vite con host por defecto (127.0.0.1) no sería alcanzable desde el navegador.
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { host: '0.0.0.0', port: 5173 },
})
