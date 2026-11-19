// postcss.config.js — Engancha Tailwind y Autoprefixer al pipeline de Vite.
// ¿Por qué hace falta? Vite procesa CSS, pero no sabe nada de @tailwind hasta que le
// decimos qué plugins de PostCSS correr. Si hacés esto distinto (olvidar el archivo),
// el index.css queda con directivas sin resolver.
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
