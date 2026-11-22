import { useEffect, useRef, useState } from 'react'
import { api, copyText, useLoad } from '../api'
import { Problem, Screen } from '../parts'
import type { Config, Endpoint, MemoryUsage, ModelsView, TierName, TierView } from '../types'

const about: Record<TierName, { title: string; role: string; help: string; port: number }> = {
  main: { title: 'Main model', role: 'Her replies', help: 'Plans, feels, writes her replies and checks them. The best model that still answers quickly.', port: 8080 },
  worker: { title: 'Worker model', role: 'Memories and summaries', help: 'Saves memories and summarises sessions. Small and quick.', port: 8081 },
  sleeper: { title: 'Sleep model', role: 'Night reflection', help: 'Reflects at night. The largest model you have.', port: 8082 },
  embed: { title: 'Memory model', role: 'Search by meaning', help: 'Finds memories by what they mean, not only by matching words. A tiny embedding model; leave empty to switch this off.', port: 8083 },
}

const stateWords: Record<TierView['status']['state'], string> = {
  running: 'running',
  starting: 'loading the model…',
  stopped: 'stopped',
  resting: 'resting',
  failed: 'failed to start',
  external: 'answering (started outside Cosmos)',
}

// When each model is loaded. Awake she uses the main and worker models,
// asleep only the sleep model; the memory model stays either way.
const groups: { side: 'awake' | 'asleep' | 'always'; label: string; sub: string; tiers: TierName[] }[] = [
  { side: 'awake', label: 'While she is awake', sub: 'main and worker load together', tiers: ['main', 'worker'] },
  { side: 'asleep', label: 'While she sleeps', sub: 'loads after the awake side stops', tiers: ['sleeper'] },
  { side: 'always', label: 'Always loaded', sub: 'small enough to stay', tiers: ['embed'] },
]
const sides = { awake: ['main', 'worker', 'embed'], asleep: ['sleeper', 'embed'] } as const

// What macOS and the other apps are counted as holding. A guess; the
// models are the part Cosmos chooses.
const MAC_BYTES = 6 * 1024 ** 3
// Without the Electron shell there is no reading of the Mac: assume 32 GB.
const FALLBACK_TOTAL = 32 * 1024 ** 3

// Sizes in the units macOS uses for memory, so a 32 GB Mac reads 32 GB.
function size(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  return `${(bytes / 1024 ** 2).toFixed(0)} MB`
}

function fileName(path: string): string {
  return path.split('/').pop() ?? path
}

const readMemory = window.cosmos?.memory

export function Models({ asleep }: { asleep: boolean }) {
  const { data, error, reload } = useLoad<ModelsView>('/api/models')
  const cfg = useLoad<Config>('/api/config')
  const [problem, setProblem] = useState('')
  const [open, setOpen] = useState<TierName | null>(null)
  const [openLog, setOpenLog] = useState<TierName | null>(null)
  const [mac, setMac] = useState<MemoryUsage | null>(null)

  // Follow a model while it loads; otherwise a slow check is enough.
  const loading = data?.tiers.some((t) => t.status.state === 'starting') ?? false
  useEffect(() => {
    const poll = () => {
      reload()
      readMemory?.().then(setMac).catch(() => undefined)
    }
    readMemory?.().then(setMac).catch(() => undefined)
    const timer = setInterval(poll, loading ? 1200 : 5000)
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
  const tier = (k: TierName) => data?.tiers.find((t) => t.tier === k)
  const total = mac?.totalBytes ?? FALLBACK_TOTAL
  const bytesOf = (path: string) => files.find((f) => f.path === path)?.size ?? 0

  // The file a tier loads, or '' when it shares another's server or runs
  // outside Cosmos. `swap` tries a different file for one tier.
  const fileOf = (k: TierName, swap?: { tier: TierName; path: string }) => {
    if (swap?.tier === k) return swap.path
    const t = tier(k)
    if (!t || t.shared_with) return ''
    return t.endpoint.launch?.model_path ?? ''
  }
  // A side's models, each server counted once. A shared sleeper means the
  // main model stays loaded through the night.
  const sideParts = (side: 'awake' | 'asleep', swap?: { tier: TierName; path: string }) => {
    const list: TierName[] = side === 'asleep' && tier('sleeper')?.shared_with && swap?.tier !== 'sleeper' ? ['main', 'embed'] : [...sides[side]]
    const seen = new Set<string>()
    return list
      .map((k) => ({ tier: k, path: fileOf(k, swap) }))
      .filter((p) => p.path && !seen.has(p.path) && seen.add(p.path))
      .map((p) => ({ ...p, bytes: bytesOf(p.path) }))
  }
  const sideTotal = (side: 'awake' | 'asleep', swap?: { tier: TierName; path: string }) => MAC_BYTES + sideParts(side, swap).reduce((n, p) => n + p.bytes, 0)
  // How far over a file would push the side (or sides) its tier belongs to.
  const overBy = (k: TierName, path: string) => {
    const mine: ('awake' | 'asleep')[] = k === 'embed' ? ['awake', 'asleep'] : k === 'sleeper' ? ['asleep'] : ['awake']
    return Math.max(...mine.map((side) => sideTotal(side, { tier: k, path }) - total))
  }

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
          llama-server was not found. Install it with <code>brew install llama.cpp</code>, or give the path to the program under a model's server settings.
        </p>
      )}

      {data && (
        <section className="mdl-plan" aria-label="Memory plan">
          <div className="mdl-bars">
            {(['awake', 'asleep'] as const).map((side) => {
              const parts = sideParts(side)
              const used = sideTotal(side)
              const scale = Math.max(total, used)
              const free = total - used
              const seg = (key: string, cls: string, bytes: number, text: string) =>
                bytes > 0 && (
                  <span key={key} className={cls} style={{ width: `${(bytes / scale) * 100}%` }} title={`${text} · ${size(bytes)}`}>
                    {bytes / scale > 0.08 ? text : ''}
                  </span>
                )
              return (
                <div className="mdl-bar-row" key={side}>
                  <span className="who">
                    {side === 'awake' ? 'Awake' : 'Asleep'}
                    <small>{side === 'awake' ? 'main · worker' : tier('sleeper')?.shared_with ? 'main model' : 'sleep model'}</small>
                  </span>
                  <div className="mdl-bar">
                    {seg('mac', 'mac', MAC_BYTES, 'macOS')}
                    {parts.map((p) => seg(p.tier, p.tier, p.bytes, about[p.tier].title.replace(' model', '')))}
                    {seg('over', 'over', used - total, 'over')}
                  </div>
                  <span className="total">
                    {size(used)} / {size(total)}
                    <small className={free < 0 ? 'bad' : undefined}>{free < 0 ? `${size(-free)} over` : `${size(free)} free`}</small>
                  </span>
                </div>
              )
            })}
          </div>
          <div className="mdl-now">
            <span className="label">Right now</span>
            <span className="mdl-now-state">
              <i className={`mdl-dot ${asleep ? 'resting' : 'running'}`} aria-hidden="true" />
              She is {asleep ? 'asleep' : 'awake'}
            </span>
            {mac && <b>{size(mac.usedBytes)} in use</b>}
            <p className="note">macOS and other apps are counted as {size(MAC_BYTES)}, and context needs a little room on top. A side that goes over is where the system stops models.</p>
          </div>
        </section>
      )}

      {data &&
        groups.map((g) => (
          <section className="mdl-group" key={g.side}>
            <div className="section-head">
              <h3 className="label">{g.label}</h3>
              <span className="sub">{g.sub}</span>
            </div>
            <div className="mdl-list">
              {g.tiers.map((k) => {
                const t = tier(k)
                if (!t) return null
                const info = about[k]
                const ep = t.endpoint
                const chosen = ep.launch?.model_path ?? ''
                const shared = !!t.shared_with
                const state = t.status.state
                const isOpen = open === k
                const launch = { binary: '', model_path: '', context_size: 8192, gpu_layers: 99, ...(ep.launch ?? {}) }
                const pick = (path: string) =>
                  save(k, {
                    base_url: ep.base_url || `http://127.0.0.1:${info.port}/v1`,
                    model: ep.model || k,
                    launch: { ...launch, model_path: path },
                  })
                return (
                  <article className={isOpen ? 'mdl open' : 'mdl'} key={k}>
                    <button type="button" className="mdl-row" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : k)}>
                      <i className={`mdl-dot ${shared ? 'shared' : state}`} aria-hidden="true" />
                      <span className="name">
                        {info.title}
                        <small>{info.role}</small>
                      </span>
                      <span className="file">
                        {shared ? (
                          <span className="none">Uses the {t.shared_with} model</span>
                        ) : chosen ? (
                          <>
                            <span>{fileName(chosen)}</span>
                            <span className="sub">
                              {bytesOf(chosen) ? `${size(bytesOf(chosen))} · ` : ''}
                              {Math.round(launch.context_size / 1024)}k context
                            </span>
                          </>
                        ) : state === 'external' ? (
                          <span className="none">{ep.base_url}</span>
                        ) : (
                          <span className="none">No file chosen</span>
                        )}
                      </span>
                      <span className="st">{shared ? 'shared' : state === 'resting' && k === 'sleeper' ? 'resting until she sleeps' : stateWords[state]}</span>
                      <svg className="chev" width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
                        <path d="M6 3l5 5-5 5" />
                      </svg>
                    </button>

                    {isOpen && (
                      <div className="mdl-detail">
                        <p className="mdl-help">{info.help}</p>
                        <div className="mdl-choose">
                          <span className="label">{shared ? 'Give it a model of its own' : 'Model file'}</span>
                          <div className="mdl-files" role="radiogroup" aria-label={`${info.title} file`}>
                            {files.length === 0 && <p className="quiet">No .gguf files found yet.</p>}
                            {files.map((f) => {
                              const over = overBy(k, f.path)
                              return (
                                <button type="button" role="radio" aria-checked={!shared && chosen === f.path} className="mdl-file" key={f.path} onClick={() => void pick(f.path)}>
                                  <i aria-hidden="true" />
                                  <span className="fn">
                                    <span>{f.name}</span>
                                    <span className="sub">{size(f.size)}</span>
                                  </span>
                                  <span className={over > 0 ? 'fit no' : 'fit ok'}>{over > 0 ? `${size(over)} over` : 'fits'}</span>
                                </button>
                              )
                            })}
                          </div>
                          {chosen && !shared && !files.some((f) => f.path === chosen) && (
                            <p className="quiet">
                              Using <code>{fileName(chosen)}</code> from another folder.
                            </p>
                          )}
                        </div>
                        {!shared && !chosen && state !== 'external' && <p className="quiet">Choose a model file above, then Start. She also starts it herself when she needs it.</p>}
                        {!shared && state === 'resting' && (
                          <p className="mdl-rest">
                            {k === 'sleeper'
                              ? 'Loads when she falls asleep, after the main and worker models have stopped. While she is awake, the main model does its work.'
                              : 'Stopped while she sleeps, to leave room for the sleep model. It loads again when she wakes.'}
                          </p>
                        )}
                        <div className="actions">
                          {!shared && (state === 'stopped' || state === 'failed') && (
                            <button type="button" className="primary" disabled={!chosen} onClick={() => void act(k, 'start')}>
                              Start
                            </button>
                          )}
                          {!shared && (state === 'running' || state === 'starting') && (
                            <>
                              <button type="button" className="secondary" onClick={() => void act(k, 'stop')}>
                                Stop
                              </button>
                              <button type="button" className="secondary" onClick={() => void act(k, 'restart')}>
                                Restart
                              </button>
                            </>
                          )}
                          {!shared && k !== 'main' && k !== 'embed' && (
                            <button type="button" className="text" onClick={() => void save(k, { base_url: '', model: '', launch: null })}>
                              Use the main model instead
                            </button>
                          )}
                          {t.status.log && (
                            <button type="button" className="text" aria-expanded={openLog === k} onClick={() => setOpenLog(openLog === k ? null : k)}>
                              {openLog === k ? 'Hide log' : 'Show log'}
                            </button>
                          )}
                        </div>
                        {t.status.error && <Problem message={t.status.error} />}
                        {openLog === k && t.status.log && <Log text={t.status.log} />}
                        {!shared && (
                          <details className="mdl-adv">
                            <summary>Server settings</summary>
                            <div className="row">
                              <Text id={`${k}-url`} label="Server address" value={ep.base_url} onCommit={(v) => save(k, { base_url: v })} />
                              <Text id={`${k}-ctx`} label="Context size" numeric value={String(launch.context_size)} onCommit={(v) => save(k, { launch: { ...launch, context_size: Number.parseInt(v, 10) || 8192 } })} />
                              <Text id={`${k}-ngl`} label="GPU layers" numeric value={String(launch.gpu_layers)} onCommit={(v) => save(k, { launch: { ...launch, gpu_layers: Number.parseInt(v, 10) || 0 } })} />
                              <Text id={`${k}-bin`} label="llama-server program (optional)" placeholder={data.binary || 'llama-server'} value={launch.binary} onCommit={(v) => save(k, { launch: { ...launch, binary: v } })} />
                              {k === 'main' && (
                                <div className="field">
                                  <label htmlFor="main-mmproj">Vision projector (lets her see pictures)</label>
                                  <select id="main-mmproj" value={launch.mmproj ?? ''} onChange={(e) => save(k, { launch: { ...launch, mmproj: e.target.value } })}>
                                    <option value="">None: she cannot see pictures</option>
                                    {(data.files ?? [])
                                      .filter((f) => f.name.includes('mmproj'))
                                      .map((f) => (
                                        <option key={f.path} value={f.path}>
                                          {f.name}
                                        </option>
                                      ))}
                                  </select>
                                </div>
                              )}
                            </div>
                          </details>
                        )}
                      </div>
                    )}
                  </article>
                )
              })}
            </div>
          </section>
        ))}

      {data && (
        <section className="mdl-disk" aria-label="Model files">
          <div className="section-head">
            <h3 className="label">Files found</h3>
            <span className="sub">{files.length} {files.length === 1 ? 'file' : 'files'}</span>
          </div>
          <p className="note">
            Looked for in{' '}
            {data.folders.map((f, i) => (
              <span key={f}>
                {i > 0 && ' and '}
                <code>{f}</code>
              </span>
            ))}
            . Put a <code>.gguf</code> file there and press Refresh.
          </p>
          {files.length > 0 && (
            <ul className="mdl-disk-list">
              {files.map((f) => {
                const users = data.tiers.filter((t) => !t.shared_with && t.endpoint.launch?.model_path === f.path)
                return (
                  <li key={f.path}>
                    <span className="fn">{f.name}</span>
                    <span className="sub">{size(f.size)}</span>
                    <span className={users.length ? 'used' : 'used unused'}>
                      {users.length
                        ? users.map((t) => (
                            <span key={t.tier} className={`mdl-tag ${t.tier}`}>
                              {about[t.tier].title.replace(' model', '')}
                            </span>
                          ))
                        : 'not used'}
                    </span>
                  </li>
                )
              })}
            </ul>
          )}
        </section>
      )}
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
      await copyText(text)
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
