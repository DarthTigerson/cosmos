import { useState } from 'react'
import { Switch } from '../parts'

interface Tool {
  server: string
  name: string
  description: string
  read_only: boolean
}

export interface Connection {
  name: string
  command: string
  args: string[]
  enabled: boolean
  allow_changes: boolean
  status: { running: boolean; error: string; tools: Tool[] }
}

export type ConnectionSwitches = Record<string, { enabled: boolean; allow_changes: boolean }>

// Her connections: tool servers of your own (MCP) that she may use.
// Reading is allowed once a server is switched on; changing things has
// its own switch. The switches wait for the save bar; removing one and
// bringing them in apply at once.
export function Connections({ list, switches, busy, change, remove, bringIn }: { list: Connection[]; switches: ConnectionSwitches; busy: boolean; change: (name: string, patch: Partial<ConnectionSwitches[string]>) => void; remove: (name: string) => void; bringIn: () => void }) {
  const [open, setOpen] = useState('')
  return (
    <div className="set-conns">
      <div className="section-head">
        <h3 className="label">Connections</h3>
        <button type="button" className="linkish" disabled={busy} onClick={bringIn}>
          Bring in the ones set up for Claude Code
        </button>
      </div>
      <p className="note">Tool servers of your own that she can use, such as your notes and to-dos. Switched on, she may read from one. She only changes things there if you also allow it.</p>
      {list.length === 0 ? (
        <p className="muted">None yet.</p>
      ) : (
        <ul className="set-conn-list">
          {list.map((c) => {
            const sw = switches[c.name] ?? { enabled: c.enabled, allow_changes: c.allow_changes }
            const reads = c.status.tools.filter((t) => t.read_only).length
            return (
              <li key={c.name}>
                <div className="set-conn">
                  <div className="who">
                    <b>{c.name}</b>
                    <span className="sub">
                      {!c.enabled ? 'off' : c.status.error ? `not working: ${c.status.error}` : c.status.running ? `${c.status.tools.length} tools · ${reads} read, ${c.status.tools.length - reads} change` : 'starting'}
                    </span>
                  </div>
                  <span className="mini">
                    She may use it
                    <Switch label={`${c.name}: she may use it`} checked={sw.enabled} disabled={busy} onChange={(on) => change(c.name, { enabled: on })} />
                  </span>
                  <span className="mini">
                    She may change things
                    <Switch label={`${c.name}: she may change things there`} checked={sw.allow_changes} disabled={busy || !sw.enabled} onChange={(on) => change(c.name, { allow_changes: on })} />
                  </span>
                  <span className="links">
                    {c.status.tools.length > 0 && (
                      <button type="button" className="linkish" aria-expanded={open === c.name} onClick={() => setOpen(open === c.name ? '' : c.name)}>
                        {open === c.name ? 'Hide tools' : 'Show tools'}
                      </button>
                    )}
                    <button type="button" className="linkish" disabled={busy} onClick={() => remove(c.name)}>
                      Remove
                    </button>
                  </span>
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
      )}
    </div>
  )
}
