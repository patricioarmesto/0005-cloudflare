// vite-env.d.ts — Tipos que Vite inyecta en import.meta.env.
// ¿Por qué existe? TypeScript no conoce VITE_API_URL por defecto; sin esta referencia
// el `import.meta.env.VITE_API_URL` de App.tsx sería un error de compilación.
/// <reference types="vite/client" />
