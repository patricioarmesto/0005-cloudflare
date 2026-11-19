// main.tsx — Arranca React y monta App en #root. Es el pegamento mínimo entre index.html
// y el componente principal. ¿Por qué StrictMode? Doble-renderiza en desarrollo para
// detectar efectos con limpieza faltante; en producción no hace nada.
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('No se encontró #root: revisá index.html')
}

ReactDOM.createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
