import { useEffect, useState, type ReactNode } from 'react'
import { api, useLoad, useSignals } from '../api'
import { isZero, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Memory as MemoryRow, Person } from '../types'

type Tab = 'fact' | 'reflection' | 'forgotten'

// Matches store.Pinned: a pinned memory is always offered to her.
const PINNED = 5
const DAY = 86_400_000

// How recently she reached for a memory, as a warmth dot.
function warmth(m: MemoryRow): string {
  if (isZero(m.last_used_at)) return ''
  const days = (Date.now() - Date.parse(m.last_used_at)) / DAY
  return days < 3 ? 'warm' : days < 21 ? 'mid' : ''
}

// Names of people she knows become links to them on the People screen.
function withPeople(text: string, people: Person[]): ReactNode {
  const names = new Map<string, number>()
  for (const p of people) for (const n of [p.name, ...p.aliases]) if (n.length >= 3) names.set(n, p.id)
  if (names.size === 0) return text
  const escaped = [...names.keys()].sort((a, b) => b.length - a.length).map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  const parts = text.split(new RegExp(`\\b(${escaped.join('|')})\\b`))
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      <a key={i} href={`#people/${names.get(part)}`} title={`Open ${part} in People`}>
        {part}
      </a>
    ) : (
      part
    ),
  )
}

const icons = {
  pin: <path d="M8 1.5l1.9 4 4.3.5-3.2 3 .9 4.3L8 11.2 4.1 13.3l.9-4.3-3.2-3 4.3-.5z" />,
  edit: <path d="M10.5 2.5l3 3L6 13H3v-3z" />,
  forget: <path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.7 8.5h5.6l.7-8.5" />,
  restore: <path d="M3 8a5 5 0 1 0 1.5-3.5M3 2.5v2.5h2.5" />,
}

function Icon({ name, filled }: { name: keyof typeof icons; filled?: boolean }) {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill={filled ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" strokeLinecap="round" aria-hidden="true">
      {icons[name]}
    </svg>
  )
}

interface RowProps {
  m: MemoryRow
  people: Person[]
  forgotten: boolean
  onForget(): void
  onRestore(): void
  onPin(pinned: boolean): void
  onCorrect(content: string): void
}

// Each memory shows what it rests on: the thing you said, or for a
// reflection the moments of the day it came from. Every wording it
// replaced is kept and can be opened.
function Row({ m, people, forgotten, onForget, onRestore, onPin, onCorrect }: RowProps) {
  const [draft, setDraft] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const basis = m.source.split('\n').filter(Boolean)
  const pinned = m.importance >= PINNED
  const history = m.history ?? []
  const save = () => {
    if (draft !== null && draft.trim() && draft.trim() !== m.content) onCorrect(draft.trim())
    setDraft(null)
  }

  return (
    <li className="mem">
      <span className={`mem-heat ${warmth(m)}`} title={isZero(m.last_used_at) ? 'Never recalled' : `Recalled ${when(m.last_used_at)}`} />
      <div className="mem-body">
        {draft === null ? (
          <span className="mem-content">{withPeople(m.content, people)}</span>
        ) : (
          <textarea
            className="mem-edit"
            aria-label="Correct this memory"
            value={draft}
            rows={2}
            autoFocus
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                save()
              }
              if (e.key === 'Escape') setDraft(null)
            }}
          />
        )}
        {basis.length > 0 && (
          <span className="basis">
            {m.kind === 'reflection' ? 'from: ' : 'you said: '}
            {basis.map((b, i) => (
              <q key={i}>{b}</q>
            ))}
          </span>
        )}
        <span className="mem-meta">
          <span className="sub">learned {when(m.created_at)}</span>
          {isZero(m.last_used_at) ? <span className="sub never">never recalled</span> : <span className="sub strong">recalled {when(m.last_used_at)}</span>}
          {pinned && <span className="sub strong">pinned</span>}
          {history.length > 0 && (
            <button type="button" className="sub mem-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
              {open ? 'hide earlier wording' : `changed ${history.length}×`}
            </button>
          )}
        </span>
        {open && (
          <ul className="mem-history">
            {history.map((h, i) => (
              <li key={i}>
                <s>{h.content}</s> <span className="sub">learned {when(h.since)}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="mem-actions">
        {forgotten ? (
          <button type="button" className="mem-act" title="Bring back" aria-label="Bring back" onClick={onRestore}>
            <Icon name="restore" />
          </button>
        ) : draft !== null ? (
          <>
            <button type="button" className="mem-act text" onClick={save}>
              Save
            </button>
            <button type="button" className="mem-act text quiet" onClick={() => setDraft(null)}>
              Cancel
            </button>
          </>
        ) : (
          <>
            <button type="button" className={pinned ? 'mem-act on' : 'mem-act'} aria-pressed={pinned} title={pinned ? 'Unpin' : 'Pin: always keep this in mind'} aria-label="Pin" onClick={() => onPin(!pinned)}>
              <Icon name="pin" filled={pinned} />
            </button>
            {m.kind === 'fact' && (
              <button type="button" className="mem-act" title="Correct" aria-label="Correct" onClick={() => setDraft(m.content)}>
                <Icon name="edit" />
              </button>
            )}
            <button type="button" className="mem-act" title="Forget" aria-label="Forget" onClick={onForget}>
              <Icon name="forget" />
            </button>
          </>
        )}
      </div>
    </li>
  )
}

export function Memory() {
  const held = useLoad<MemoryRow[]>('/api/memories')
  const put = useLoad<MemoryRow[]>('/api/memories?forgotten=1')
  const people = useLoad<Person[]>('/api/people')
  const [tab, setTab] = useState<Tab>('fact')
  const [query, setQuery] = useState('')
  const [category, setCategory] = useState<string | null>(null)
  const [problem, setProblem] = useState('')
  const [toast, setToast] = useState<{ text: string; undo?: number } | null>(null)
  useSignals((s) => {
    if (s.kind === 'state' || s.kind === 'proposals' || s.kind === 'message' || s.kind === 'memories') {
      held.reload()
      put.reload()
    }
    if (s.kind === 'message' || s.kind === 'state') people.reload()
  })
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 6000)
    return () => clearTimeout(t)
  }, [toast])

  const act = (path: string, method: string, body?: unknown, done?: string, undo?: number) => {
    setProblem('')
    void api(path, { method, body })
      .then(() => {
        held.reload()
        put.reload()
        if (done) setToast({ text: done, undo })
      })
      .catch((e: Error) => setProblem(e.message))
  }

  // Podcast notes are put away from the Podcasts screen and are not shown here.
  const forgotten = (put.data ?? []).filter((m) => m.kind === 'fact' || m.kind === 'reflection')
  const counts = {
    fact: (held.data ?? []).filter((m) => m.kind === 'fact').length,
    reflection: (held.data ?? []).filter((m) => m.kind === 'reflection').length,
    forgotten: forgotten.length,
  }
  const inTab = tab === 'forgotten' ? forgotten : (held.data ?? []).filter((m) => m.kind === tab)
  const categories = tab === 'fact' ? [...new Set(inTab.map((m) => m.category))].sort() : []
  const q = query.trim().toLowerCase()
  const shown = inTab.filter((m) => (!category || m.category === category) && (!q || `${m.content} ${m.source} ${m.category}`.toLowerCase().includes(q)))
  const rows = tab === 'reflection' ? [...shown].reverse() : shown
  const thisWeek = shown.filter((m) => !isZero(m.last_used_at) && Date.now() - Date.parse(m.last_used_at) < 7 * DAY).length
  const never = shown.filter((m) => isZero(m.last_used_at)).length

  const row = (m: MemoryRow) => (
    <Row
      key={m.id}
      m={m}
      people={people.data ?? []}
      forgotten={tab === 'forgotten'}
      onForget={() => act(`/api/memories/${m.id}/forget`, 'POST', undefined, 'Forgotten. She will not use it again.', m.id)}
      onRestore={() => act(`/api/memories/${m.id}/restore`, 'POST', undefined, 'Brought back.')}
      onPin={(pinned) => act(`/api/memories/${m.id}`, 'PUT', { pinned }, pinned ? 'Pinned. She will always keep this in mind.' : 'Unpinned.')}
      onCorrect={(content) => act(`/api/memories/${m.id}`, 'PUT', { content }, 'Corrected. The old wording is kept.')}
    />
  )
  const pick = (next: Tab) => {
    setTab(next)
    setCategory(null)
  }

  return (
    <Screen eyebrow="What stays with her" title="Memory" className="memory-screen">
      <Problem message={problem || held.error || put.error} />
      <div className="mem-toolbar">
        <label className="mem-search">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
            <circle cx="7" cy="7" r="5" />
            <path d="M11 11l3.5 3.5" />
          </svg>
          <input type="search" placeholder="Search what she knows, or what you said" aria-label="Search memories" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <div className="mem-tabs" role="group" aria-label="Which memories">
          {(
            [
              ['fact', 'Facts'],
              ['reflection', 'Reflections'],
              ['forgotten', 'Forgotten'],
            ] as const
          ).map(([t, label]) => (
            <button key={t} type="button" aria-pressed={tab === t} onClick={() => pick(t)}>
              {label}
              <span className="n">{counts[t]}</span>
            </button>
          ))}
        </div>
      </div>
      {categories.length > 1 && (
        <div className="mem-chips" role="group" aria-label="Categories">
          <button type="button" aria-pressed={category === null} onClick={() => setCategory(null)}>
            all
          </button>
          {categories.map((c) => (
            <button key={c} type="button" aria-pressed={category === c} onClick={() => setCategory(c)}>
              {c} {inTab.filter((m) => m.category === c).length}
            </button>
          ))}
        </div>
      )}
      <p className="mem-stats">
        {tab === 'forgotten' ? (
          'Forgotten memories are kept on record but never shown to her. Bring one back to use it again.'
        ) : (
          <>
            <span>
              <b>{shown.length}</b> shown
            </span>
            <span>
              <b>{thisWeek}</b> recalled this week
            </span>
            <span>
              <b>{never}</b> never recalled
            </span>
          </>
        )}
      </p>

      {/* Only the list scrolls; everything above stays put. */}
      <div className="mem-scroll">
        {held.data &&
          (rows.length === 0 ? (
            <Empty>
              {q
                ? `Nothing she knows matches “${query.trim()}”.`
                : tab === 'fact'
                  ? 'Nothing yet. She picks things up as you talk.'
                  : tab === 'reflection'
                    ? 'No reflections yet. They come from sleep.'
                    : 'Nothing forgotten.'}
            </Empty>
          ) : tab === 'fact' && !category ? (
            categories.map((c) => {
              const inCategory = rows.filter((m) => m.category === c)
              return (
                inCategory.length > 0 && (
                  <div key={c} className="group">
                    <h3 className="label">{c}</h3>
                    <ul className="mem-list">{inCategory.map(row)}</ul>
                  </div>
                )
              )
            })
          ) : (
            <ul className="mem-list">{rows.map(row)}</ul>
          ))}
      </div>

      {toast && (
        <div className="mem-toast" role="status">
          <span>{toast.text}</span>
          {toast.undo !== undefined && (
            <button type="button" className="linkish" onClick={() => act(`/api/memories/${toast.undo}/restore`, 'POST', undefined, 'Brought back.')}>
              Undo
            </button>
          )}
        </div>
      )}
    </Screen>
  )
}
