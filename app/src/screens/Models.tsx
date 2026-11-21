import { useEffect, useRef, useState } from 'react'
import { api, useLoad } from '../api'
import { Problem, Screen } from '../parts'
import type { Config, Endpoint, ModelsView, TierName, TierView } from '../types'

const about: Record<TierName, { title: string; help: string; port: number }> = {
  main: { title: 'Main model', help: 'Plans, feels, writes her replies and checks them. The best model that still answers quickly.', port: 8080 },
  worker: { title: 'Worker model', help: 'Saves memories and summarises sessions. Small and quick.', port: 8081 },
  sleeper: { title: 'Sleep model', help: 'Reflects at night. The largest model you have.', port: 8082 },
  embed: { title: 'Memory model', help: 'Finds memories by what they mean, not only by matching words. A tiny embedding model; leave empty to switch this off.', port: 8083 },
}

const stateWords: Record<TierView['status']['state'], string> = {
  running: 'running',
  starting: 'loading the model…',
  stopped: 'stopped',
  failed: 'failed to start',
  external: 'answering (started outside Cosmos)',
}

function size(bytes: number): string {
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(2)} GB`
  return `${(bytes / 1e6).toFixed(0)} MB`
}

function fileName(path: string): string {
  return path.split('/').pop() ?? path
}

export function Models() {
  const { data, error, reload } = useLoad<ModelsView>('/api/models')
  const cfg = useLoad<Config>('/api/config')
  const [problem, setProblem] = useState('')
  const [openLog, setOpenLog] = useState<TierName | null>(null)

  // Follow a model while it loads; otherwise a slow check is enough.
  const loading = data?.tiers.some((t) => t.status.state === 'starting') ?? false
  useEffect(() => {
    const timer = setInterval(reload, loading ? 1200 : 5000)
    return () => clearInterval(timer)
  }, [loading, reload])

  async function save(tier: TierName, patch: Partial<Endpoint>) {
    if (!cfg.data) return
    setProblem('')
    try {
      await api('/api/config', { method: 'PUT', body: { ...cfg.data, [tier]: { ...cfg.data[tier], ...patch } } })
      cfg.reload()
      reload()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  async function act(tier: TierName, action: 'start' | 'stop' | 'restart') {
    setProblem('')
    try {
      await api(`/api/models/${tier}/${action}`, { method: 'POST' })
      setTimeout(reload, 300)
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  const files = data?.files.filter((f) => !f.name.includes('mmproj')) ?? []

  return (
    <Screen
      eyebrow={data ? (data.binary ? `llama.cpp · ${data.binary}` : 'llama.cpp · not on this Mac\'s path') : 'llama.cpp'}
      title="Models"
      action={
        <button type="button" className="secondary" onClick={reload}>
          Refresh
        </button>
      }
    >
      <Problem message={error || cfg.error || problem} />
      {data && !data.binary && !data.tiers.some((t) => t.endpoint.launch?.binary) && (
        <p className="notice">
          llama-server was not found. Install it with <code>brew install llama.cpp</code>, or give the path to the program on a model below.
        </p>
      )}
      {data && (
        <p className="note">
          Model files are looked for in {data.folders.map((f, i) => (
            <span key={f}>
              {i > 0 && ' and '}
              <code>{f}</code>
            </span>
          ))}
          . Put a <code>.gguf</code> file there and press Refresh.
        </p>
      )}

      <div className="models">
        {data?.tiers.map((t) => {
          const info = about[t.tier]
          const ep = t.endpoint
          const chosen = ep.launch?.model_path ?? ''
          const shared = !!t.shared_with
          const state = t.status.state
          const launch = { binary: '', model_path: '', context_size: 8192, gpu_layers: 99, ...(ep.launch ?? {}) }
          const pick = (path: string) =>
            save(t.tier, {
              base_url: ep.base_url || `http://127.0.0.1:${info.port}/v1`,
              model: ep.model || t.tier,
              launch: { ...launch, model_path: path },
            })
          return (
            <article className="model" key={t.tier}>
              <header>
                <div>
                  <h2>{info.title}</h2>
                  <p className="note">{info.help}</p>
                </div>
                <span className={`state ${shared ? 'shared' : state}`}>
                  <i aria-hidden="true" />
                  {shared ? `uses the ${t.shared_with} model` : stateWords[state]}
                </span>
              </header>

              <div className="picker" role="radiogroup" aria-label={`${info.title} file`}>
                {files.length === 0 && <p className="quiet">No .gguf files found yet.</p>}
                {files.map((f) => (
                  <button type="button" role="radio" aria-checked={chosen === f.path} className="file" key={f.path} onClick={() => void pick(f.path)}>
                    <i aria-hidden="true" />
                    <span className="file-name">{f.name}</span>
                    <span className="sub">{size(f.size)}</span>
                  </button>
                ))}
                {chosen && !files.some((f) => f.path === chosen) && (
                  <p className="quiet">
                    Using <code>{fileName(chosen)}</code> from another folder.
                  </p>
                )}
              </div>

              {!shared && (
                <div className="row">
                  <Text id={`${t.tier}-url`} label="Server address" value={ep.base_url} onCommit={(v) => save(t.tier, { base_url: v })} />
                  <Text id={`${t.tier}-ctx`} label="Context size" numeric value={String(launch.context_size)} onCommit={(v) => save(t.tier, { launch: { ...launch, context_size: Number.parseInt(v, 10) || 8192 } })} />
                  <Text id={`${t.tier}-ngl`} label="GPU layers" numeric value={String(launch.gpu_layers)} onCommit={(v) => save(t.tier, { launch: { ...launch, gpu_layers: Number.parseInt(v, 10) || 0 } })} />
                  <Text id={`${t.tier}-bin`} label="llama-server program (optional)" placeholder={data.binary || 'llama-server'} value={launch.binary} onCommit={(v) => save(t.tier, { launch: { ...launch, binary: v } })} />
                </div>
              )}

              <div className="actions">
                {!shared && (state === 'stopped' || state === 'failed') && (
                  <button type="button" className="primary" disabled={!chosen} onClick={() => void act(t.tier, 'start')}>
                    Start
                  </button>
                )}
                {!shared && (state === 'running' || state === 'starting') && (
                  <>
                    <button type="button" className="secondary" onClick={() => void act(t.tier, 'stop')}>
                      Stop
                    </button>
                    <button type="button" className="secondary" onClick={() => void act(t.tier, 'restart')}>
                      Restart
                    </button>
                  </>
                )}
                {!shared && t.tier !== 'main' && (
                  <button type="button" className="text" onClick={() => void save(t.tier, { base_url: '', model: '', launch: null })}>
                    Use the main model instead
                  </button>
                )}
                {t.status.log && (
                  <button type="button" className="text" aria-expanded={openLog === t.tier} onClick={() => setOpenLog(openLog === t.tier ? null : t.tier)}>
                    {openLog === t.tier ? 'Hide log' : 'Show log'}
                  </button>
                )}
              </div>
              {!shared && !chosen && state !== 'external' && <p className="quiet">Choose a model file above, then Start. She also starts it herself when she needs it.</p>}
              {t.status.error && <Problem message={t.status.error} />}
              {openLog === t.tier && t.status.log && <Log text={t.status.log} />}
            </article>
          )
        })}
      </div>
    </Screen>
  )
}

// The server's output, with a button to copy all of it. Only rendered
// while the log is shown.
function Log({ text }: { text: string }) {
  const [copied, setCopied] = useState('')
  const box = useRef<HTMLPreElement>(null)
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied('Copied')
    } catch {
      // The clipboard was refused: select the text so Cmd+C copies it.
      const range = document.createRange()
      if (box.current) {
        range.selectNodeContents(box.current)
        const sel = window.getSelection()
        sel?.removeAllRanges()
        sel?.addRange(range)
      }
      setCopied('Selected. Press Cmd+C to copy.')
    }
    setTimeout(() => setCopied(''), 2500)
  }
  return (
    <div className="log-box">
      <div className="log-bar">
        <span className="sub" role="status">{copied}</span>
        <button type="button" className="text" onClick={() => void copy()}>
          Copy log
        </button>
      </div>
      <pre className="log" ref={box}>
        {text.slice(-4000)}
      </pre>
    </div>
  )
}

// A text field that saves when you leave it or press Enter.
function Text({ id, label, value, placeholder, numeric, onCommit }: { id: string; label: string; value: string; placeholder?: string; numeric?: boolean; onCommit: (v: string) => void }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => {
    if (draft.trim() !== value) onCommit(draft.trim())
  }
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        inputMode={numeric ? 'numeric' : undefined}
        placeholder={placeholder}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        }}
      />
    </div>
  )
}
