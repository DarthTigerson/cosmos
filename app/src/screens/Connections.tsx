import { useState } from 'react'
import { api, useLoad } from '../api'
import { Problem } from '../parts'

interface Tool {
  server: string
  name: string
  description: string
  read_only: boolean
}

interface Connection {
  name: string
  command: string
  args: string[]
  enabled: boolean
  allow_changes: boolean
  status: { running: boolean; error: string; tools: Tool[] }
}

// Her connections: tool servers of your own (MCP) that she may use.
// Reading is allowed once a server is switched on; changing things has
// its own switch.
export function Connections() {
  const { data, error, reload } = useLoad<Connection[]>('/api/connections')
  const [problem, setProblem] = useState('')
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState('')

  async function run(work: () => Promise<unknown>) {
    setBusy(true)
    setProblem('')
    try {
      await work()
      reload()
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const save = (next: Connection[]) =>
    run(() => api('/api/connections', { method: 'PUT', body: next.map((c) => ({ name: c.name, enabled: c.enabled, allow_changes: c.allow_changes })) }))
  const change = (name: string, patch: Partial<Connection>) => save((data ?? []).map((c) => (c.name === name ? { ...c, ...patch } : c)))

  return (
    <section className="settings">
      <fieldset>
        <legend>Her connections</legend>
        <p className="note">
          Tool servers of your own that she can use, such as your notes and to-dos. Switched on, she may read from one. She only changes things there if you also allow it.
        </p>
        <Problem message={error || problem} />
        {data?.length === 0 && <p className="muted">None yet.</p>}
        <ul className="connections">
          {(data ?? []).map((c) => {
            const usable = c.status.tools.filter((t) => t.read_only || c.allow_changes).length
            return (
              <li key={c.name}>
                <div className="connection-head">
                  <span className="connection-name">{c.name}</span>
                  <span className="sub">
                    {!c.enabled ? 'off' : c.status.error ? `not working: ${c.status.error}` : c.status.running ? `${usable} of ${c.status.tools.length} tools open to her` : 'starting'}
                  </span>
                </div>
                <div className="connection-switches">
                  <span className="field check">
                    <input id={`on-${c.name}`} type="checkbox" disabled={busy} checked={c.enabled} onChange={(e) => void change(c.name, { enabled: e.target.checked })} />
                    <label htmlFor={`on-${c.name}`}>She may use it</label>
                  </span>
                  <span className="field check">
                    <input id={`write-${c.name}`} type="checkbox" disabled={busy || !c.enabled} checked={c.allow_changes} onChange={(e) => void change(c.name, { allow_changes: e.target.checked })} />
                    <label htmlFor={`write-${c.name}`}>She may change things there</label>
                  </span>
                  {c.status.tools.length > 0 && (
                    <button type="button" className="linkish" aria-expanded={open === c.name} onClick={() => setOpen(open === c.name ? '' : c.name)}>
                      {open === c.name ? 'Hide tools' : 'Show tools'}
                    </button>
                  )}
                  <button type="button" className="linkish" disabled={busy} onClick={() => void save((data ?? []).filter((x) => x.name !== c.name))}>
                    Remove
                  </button>
                </div>
                {open === c.name && (
                  <ul className="connection-tools">
                    {c.status.tools.map((t) => (
                      <li key={t.name}>
                        <span className="sub">{t.read_only ? 'reads' : 'changes'}</span>
                        <span>
                          {t.name}
                          <span className="muted"> · {t.description}</span>
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            )
          })}
        </ul>
        <div className="actions">
          <button type="button" className="secondary" disabled={busy} onClick={() => void run(() => api('/api/connections/import', { method: 'POST' }))}>
            Bring in the ones set up for Claude Code
          </button>
        </div>
      </fieldset>
    </section>
  )
}
