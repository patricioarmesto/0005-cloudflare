// App.tsx — Es el panel del ejercicio: lista las 5 páginas, dispara los pedidos al edge,
// muestra la latencia en vivo, el hit rate por POP con Recharts y el log reciente.
// Nota: package.json y tsconfig.json son JSON y no admiten comentarios; por eso la
// documentación de esas piezas vive acá y en el README.
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

// API_BASE existe porque en Compose nginx sirve el build y hace de proxy: el browser
// llama a rutas relativas. Si hacés `npm run dev`, seteá VITE_API_URL=http://localhost:8083
// para pegarle directo al Go. Ojo: dejar el default vacío SIEMPRE funciona en Docker.
const API_BASE = import.meta.env.VITE_API_URL ?? ''

type CacheResult = 'HIT' | 'MISS'

type PageInfo = {
  slug: string
  title: string
}

type PageResponse = {
  slug: string
  title: string
  body: string
  cache: CacheResult
  pop: string
  latency_ms: number
  cache_key: string
  ttl_seconds: number
}

type PopStat = {
  pop: string
  hits: number
  misses: number
  hit_rate: number
}

type RecentLog = {
  slug: string
  result: CacheResult
  latency_ms: number
  pop: string
  created_at: string
}

type Stats = {
  hits: number
  misses: number
  total: number
  hit_rate: number
  by_pop: PopStat[]
  recent: RecentLog[]
}

type Sample = {
  id: number
  cache: CacheResult
  latency_ms: number
  slug: string
}

// REGIONS alimenta el select que simula la resolución DNS. Los valores coincide con las
// keys de popByRegion en edge.go; si hacés esto distinto (otro string), el edge cae al
// default EZE y el panel mostraría un POP que no elegiste.
const REGIONS = [
  { value: 'buenos-aires', label: 'Buenos Aires → EZE' },
  { value: 'santiago', label: 'Santiago → SCL' },
  { value: 'mexico', label: 'México → MEX' },
  { value: 'miami', label: 'Miami → MIA' },
]

// App existe porque es el único componente que necesita ver JUNTOS el control del
// experimento, la latencia y el hit rate: separarlos en tres componentes obligaría a
// subir el estado a un store y el alumno perdería de vista dónde nace cada número.
export default function App() {
  const [pages, setPages] = useState<PageInfo[]>([])
  const [slug, setSlug] = useState('inicio')
  const [region, setRegion] = useState('buenos-aires')
  const [last, setLast] = useState<PageResponse | null>(null)
  const [samples, setSamples] = useState<Sample[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [burst, setBurst] = useState({ done: 0, hit: 0, miss: 0 })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const sampleId = useRef(0)

  // loadPages existe porque las 5 páginas viven en Postgres, no hardcodeadas acá. Así,
  // si el origen cambia el catálogo, el frontend se entera sin un deploy.
  const loadPages = useCallback(async () => {
    try {
      const response = await fetch(`${API_BASE}/api/pages`)
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const data = (await response.json()) as PageInfo[]
      setPages(data)
    } catch (err) {
      setError(`No se pudieron cargar las páginas: ${String(err)}`)
    }
  }, [])

  // loadStats existe porque el hit rate se calcula en Postgres, no en el browser: si lo
  // calculáramos acá, al recargar la página perderíamos el histórico.
  const loadStats = useCallback(async () => {
    try {
      const response = await fetch(`${API_BASE}/api/stats`)
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      setStats((await response.json()) as Stats)
    } catch (err) {
      setError(`No se pudieron cargar las estadísticas: ${String(err)}`)
    }
  }, [])

  useEffect(() => {
    void loadPages()
    void loadStats()
    // ¿Por qué no polling? Para mantener el ejercicio legible: el panel se actualiza
    // cuando el usuario aprieta un botón, no en background.
  }, [loadPages, loadStats])

  // requestPage existe porque es el ÚNICO punto que habla con GET /pagina/{slug}. Toda la
  // instrumentación (header X-User-Region, validación de status) queda en un solo lugar.
  // Ojo: el header X-Cache también viene en la respuesta, pero leemos el campo `cache`
  // del body para que el demo no dependa de Access-Control-Expose-Headers en dev.
  const requestPage = useCallback(
    async (pageSlug: string): Promise<PageResponse> => {
      const response = await fetch(
        `${API_BASE}/pagina/${encodeURIComponent(pageSlug)}`,
        { headers: { 'X-User-Region': region } },
      )
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      return (await response.json()) as PageResponse
    },
    [region],
  )

  // addSample existe para alimentar el panel en vivo con un máximo de 30 barras. El id
  // viene de un useRef en vez de Date.now(): en 100 requests rápidos los timestamps
  // se repiten y React reutilizaría claves, rompiendo la animación.
  const addSample = (response: PageResponse) => {
    sampleId.current += 1
    const sample: Sample = {
      id: sampleId.current,
      cache: response.cache,
      latency_ms: response.latency_ms,
      slug: response.slug,
    }
    setSamples((previous) => [...previous, sample].slice(-30))
    setLast(response)
  }

  // askOnce existe para el caso didáctico más simple: un solo request, ver MISS, apretar
  // de nuevo, ver HIT. Sin esto, el alumno tendría que analizar 100 barras para empezar.
  const askOnce = async () => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      addSample(await requestPage(slug))
      await loadStats()
    } catch (err) {
      setError(`El pedido falló: ${String(err)}`)
    } finally {
      setBusy(false)
    }
  }

  // askHundred existe para provocar el fenómeno que queremos enseñar: una sola clave fría
  // se transforma en 99 HIT. ¿Por qué secuencial y no 100 en paralelo? Porque diez requests
  // concurrentes sobre una key vacía dan diez MISS (cache stampede) y el resultado 1-99
  // deja de ser determinista. Acá queremos aislar el efecto de la caché; el stampede queda
  // fuera de alcance a propósito. El `await` por request además hace que las barras de
  // latencia aparezcan de a una y se vea el cambio en vivo.
  const askHundred = async () => {
    setBusy(true)
    setError('')
    setNotice('')
    setSamples([])
    setBurst({ done: 0, hit: 0, miss: 0 })

    let hit = 0
    let miss = 0
    try {
      for (let index = 0; index < 100; index += 1) {
        const response = await requestPage(slug)
        if (response.cache === 'HIT') hit += 1
        else miss += 1
        addSample(response)
        if ((index + 1) % 10 === 0) {
          setBurst({ done: index + 1, hit, miss })
        }
      }
      await loadStats()
      setNotice('Listo: mirá el X-Cache del primer pedido contra los 99 restantes.')
    } catch (err) {
      setError(`La tanda falló: ${String(err)}`)
    } finally {
      setBusy(false)
    }
  }

  // clearCache existe para volver al estado inicial sin `docker compose down`. Ojo: no
  // limpiamos `stats` ni los logs; queremos que el próximo MISS quede al lado de los HIT.
  const clearCache = async () => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const response = await fetch(`${API_BASE}/api/clear-cache`, { method: 'POST' })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const data = (await response.json()) as { deleted: number }
      setSamples([])
      setLast(null)
      setBurst({ done: 0, hit: 0, miss: 0 })
      await loadStats()
      setNotice(`Cache limpiada: ${data.deleted} key(s) borrada(s). El próximo pedido será MISS.`)
    } catch (err) {
      setError(`No se pudo limpiar la caché: ${String(err)}`)
    } finally {
      setBusy(false)
    }
  }

  const chartData = (stats?.by_pop ?? []).map((row) => ({
    pop: row.pop,
    hit_rate: Number(row.hit_rate.toFixed(1)),
  }))

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 bg-slate-900/60">
        <div className="mx-auto max-w-7xl px-6 py-6">
          <h1 className="text-3xl font-semibold tracking-tight">
            ¿Cómo funciona Cloudflare? <span className="text-emerald-400">edge cache</span>
          </h1>
          <p className="mt-2 max-w-3xl text-sm text-slate-400">
            Un HIT se sirve desde Redis en el POP (~5 ms). Un MISS cruza hasta el origen
            (~200 ms) y recién ahí se guarda con TTL de 60 s. Pedí la misma página 100 veces
            para ver cómo cambia el resultado.
          </p>
        </div>
      </header>

      <main className="mx-auto max-w-7xl space-y-5 px-6 py-6">
        {error && (
          <div className="rounded-lg border border-red-800 bg-red-950/50 px-4 py-3 text-sm text-red-200">
            {error}
          </div>
        )}
        {notice && (
          <div className="rounded-lg border border-emerald-800 bg-emerald-950/40 px-4 py-3 text-sm text-emerald-200">
            {notice}
          </div>
        )}

        <div className="grid gap-5 lg:grid-cols-3">
          {/* Columna 1: control del experimento */}
          <section className="rounded-xl border border-slate-800 bg-slate-900/50 p-5">
            <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">
              1. Elegí qué pedir
            </h2>

            <div className="mt-4 space-y-2">
              {pages.map((page) => (
                <button
                  key={page.slug}
                  type="button"
                  onClick={() => setSlug(page.slug)}
                  className={`flex w-full items-center justify-between rounded-lg border px-3 py-2 text-left text-sm transition ${
                    slug === page.slug
                      ? 'border-emerald-500 bg-emerald-500/10 text-emerald-200'
                      : 'border-slate-800 bg-slate-950/60 text-slate-300 hover:border-slate-600'
                  }`}
                >
                  <span>{page.title}</span>
                  <code className="text-xs text-slate-500">/{page.slug}</code>
                </button>
              ))}
            </div>

            <label className="mt-5 block text-xs font-medium text-slate-400" htmlFor="region">
              Región del usuario (DNS mock)
            </label>
            <select
              id="region"
              value={region}
              onChange={(event) => setRegion(event.target.value)}
              className="mt-2 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-200"
            >
              {REGIONS.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </select>

            <div className="mt-5 space-y-2">
              <button
                type="button"
                onClick={() => void askOnce()}
                disabled={busy}
                className="w-full rounded-lg bg-slate-700 px-3 py-2 text-sm font-medium hover:bg-slate-600 disabled:opacity-50"
              >
                Pedir 1 vez
              </button>
              <button
                type="button"
                onClick={() => void askHundred()}
                disabled={busy}
                className="w-full rounded-lg bg-emerald-600 px-3 py-2 text-sm font-semibold hover:bg-emerald-500 disabled:opacity-50"
              >
                Pedir 100 veces
              </button>
              <button
                type="button"
                onClick={() => void clearCache()}
                disabled={busy}
                className="w-full rounded-lg border border-amber-600/60 bg-amber-500/10 px-3 py-2 text-sm font-medium text-amber-200 hover:bg-amber-500/20 disabled:opacity-50"
              >
                Limpiar cache
              </button>
            </div>

            {burst.done > 0 && (
              <p className="mt-4 rounded-lg bg-slate-950 p-3 font-mono text-xs text-slate-400">
                {burst.done}/100 pedidos · HIT {burst.hit} · MISS {burst.miss}
              </p>
            )}
          </section>

          {/* Columna 2: latencia en vivo */}
          <section className="rounded-xl border border-slate-800 bg-slate-900/50 p-5">
            <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">
              2. Latencia en vivo
            </h2>

            <div className="mt-4 flex items-end gap-4">
              <span className="text-5xl font-semibold tabular-nums">
                {last ? last.latency_ms : '—'}
              </span>
              <span className="pb-2 text-sm text-slate-400">ms del último pedido</span>
              {last && (
                <span
                  className={`ml-auto rounded-full px-3 py-1 text-xs font-bold ${
                    last.cache === 'HIT'
                      ? 'bg-emerald-500/20 text-emerald-300'
                      : 'bg-amber-500/20 text-amber-300'
                  }`}
                >
                  {last.cache}
                </span>
              )}
            </div>

            <div className="mt-6 flex h-32 items-end gap-1" aria-label="Muestras de latencia">
              {samples.map((sample) => (
                <div
                  key={sample.id}
                  title={`${sample.cache} · ${sample.latency_ms} ms · ${sample.slug}`}
                  className="flex h-full min-w-[3px] flex-1 items-end"
                >
                  <div
                    className={`w-full rounded-t-sm ${
                      sample.cache === 'HIT' ? 'bg-emerald-400' : 'bg-amber-400'
                    }`}
                    // ¿Por qué 220 y no 200? Es el delay del origen más un margen para
                    // que la barra del MISS no llegue justo al borde superior del panel.
                    style={{
                      height: `${Math.max(4, Math.min(100, (sample.latency_ms / 220) * 100))}%`,
                    }}
                  />
                </div>
              ))}
              {samples.length === 0 && (
                <p className="self-center text-xs text-slate-500">
                  Todavía no: apretá un botón para generar muestras.
                </p>
              )}
            </div>

            <div className="mt-4 space-y-1 border-t border-slate-800 pt-4 font-mono text-xs text-slate-400">
              <p>
                cache_key: <span className="text-slate-200">{last?.cache_key ?? '—'}</span>
              </p>
              <p>
                X-Edge-Pop: <span className="text-slate-200">{last?.pop ?? '—'}</span>
              </p>
              <p>
                ttl: <span className="text-slate-200">{last ? `${last.ttl_seconds}s` : '—'}</span>
              </p>
              <p>
                contenido: <span className="text-slate-200">{last?.body ?? '—'}</span>
              </p>
            </div>
          </section>

          {/* Columna 3: agregados */}
          <section className="rounded-xl border border-slate-800 bg-slate-900/50 p-5">
            <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">
              3. Hit rate desde Postgres
            </h2>

            <div className="mt-4 grid grid-cols-3 gap-2 text-center">
              <div className="rounded-lg bg-slate-950 p-3">
                <p className="text-2xl font-semibold text-emerald-400">{stats?.hits ?? 0}</p>
                <p className="text-xs text-slate-500">HIT</p>
              </div>
              <div className="rounded-lg bg-slate-950 p-3">
                <p className="text-2xl font-semibold text-amber-400">{stats?.misses ?? 0}</p>
                <p className="text-xs text-slate-500">MISS</p>
              </div>
              <div className="rounded-lg bg-slate-950 p-3">
                <p className="text-2xl font-semibold">{(stats?.hit_rate ?? 0).toFixed(1)}%</p>
                <p className="text-xs text-slate-500">hit rate</p>
              </div>
            </div>

            <div className="mt-6 h-52">
              {chartData.length > 0 ? (
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: -20 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#1e293b" />
                    <XAxis dataKey="pop" tick={{ fill: '#94a3b8', fontSize: 12 }} />
                    <YAxis domain={[0, 100]} tick={{ fill: '#94a3b8', fontSize: 12 }} />
                    <Tooltip
                      formatter={(value) => [`${value}%`, 'HIT rate']}
                      contentStyle={{ background: '#0f172a', border: '1px solid #334155' }}
                    />
                    <Bar dataKey="hit_rate" fill="#34d399" radius={[4, 4, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              ) : (
                <p className="flex h-full items-center justify-center text-xs text-slate-500">
                  Todavía no hay eventos en cache_logs.
                </p>
              )}
            </div>
          </section>
        </div>

        {/* Log reciente: para ver que el hit rate sale de filas reales, no de un contador en memoria. */}
        <section className="rounded-xl border border-slate-800 bg-slate-900/50 p-5">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">
           Últimos eventos (cache_logs)
          </h2>
          <div className="mt-4 overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="text-slate-500">
                <tr>
                  <th className="pb-2">Resultado</th>
                  <th className="pb-2">Slug</th>
                  <th className="pb-2">POP</th>
                  <th className="pb-2">Latencia</th>
                  <th className="pb-2">Cuándo</th>
                </tr>
              </thead>
              <tbody className="font-mono text-slate-300">
                {(stats?.recent ?? []).map((item, index) => (
                  <tr key={`${item.created_at}-${index}`} className="border-t border-slate-800">
                    <td className={`py-2 ${item.result === 'HIT' ? 'text-emerald-400' : 'text-amber-400'}`}>
                      {item.result}
                    </td>
                    <td className="py-2">/{item.slug}</td>
                    <td className="py-2">{item.pop}</td>
                    <td className="py-2">{item.latency_ms} ms</td>
                    <td className="py-2 text-slate-500">{item.created_at}</td>
                  </tr>
                ))}
                {(stats?.recent ?? []).length === 0 && (
                  <tr>
                    <td colSpan={5} className="py-4 text-slate-500">
                      Sin eventos todavía.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      </main>
    </div>
  )
}
