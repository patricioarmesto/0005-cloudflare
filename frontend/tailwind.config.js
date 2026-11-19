// tailwind.config.js — Le dice a Tailwind dónde buscar las clases utilitarias.
// Ojo: si el content no incluye los .tsx, el build pasa pero la UI sale sin estilos.
// ¿Por qué ESM? El package.json declara "type": "module", así que acá va export default.
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: { extend: {} },
  plugins: [],
}
