import { useEffect, useState, type FormEvent } from 'react'
import { api, useLoad, useSignals } from '../api'
import { closenessWord, isZero, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Person, PersonDetail } from '../types'
import { Row } from './Memory'

// The groups the list is sorted into, in order, by relationship word.
const groups = ['Family', 'Friends', 'Work', 'Pets', 'Others', 'Not sure yet'] as const
type Group = (typeof groups)[number]

function groupOf(relationship: string): Group {
  const r = relationship.toLowerCase()
  if (!r.trim()) return 'Not sure yet'
  if (/wife|husband|partner|spouse|fianc|girlfriend|boyfriend|son|daughter|child|baby|kid|sister|brother|mother|father|mum|mom|dad|parent|cousin|aunt|uncle|niece|nephew|grand|family/.test(r)) return 'Family'
  if (/friend/.test(r)) return 'Friends'
  if (/colleague|coworker|boss|manager|client|founder|employee|team/.test(r)) return 'Work'
  if (/dog|cat|pet|puppy|kitten|horse/.test(r)) return 'Pets'
  return 'Others'
}

// One-tap answers to "who is this?".
const answers = ['wife', 'partner', 'son', 'daughter', 'sister', 'brother', 'friend', 'colleague', 'dog', 'cat']

function Avatar({ p, size }: { p: Pick<Person, 'name' | 'relationship'>; size?: 'big' | 'small' }) {
  const group = groupOf(p.relationship).toLowerCase().replace(/ /g, '-')
  return (
    <span className={`pavatar ${group} ${size ?? ''}`} aria-hidden="true">
      {p.name.slice(0, 1)}
    </span>
  )
}

export function People() {
  const { data, error, reload } = useLoad<Person[]>('/api/people')
  // #people/12 opens that person, as linked from a memory.
  const [picked, setPicked] = useState<number | null>(() => Number(location.hash.split('/')[1]) || null)
  const [mode, setMode] = useState<'edit' | 'merge' | 'forget' | null>(null)
  const [query, setQuery] = useState('')
  const [problem, setProblem] = useState('')
  const [toast, setToast] = useState('')
  useSignals((s) => {
    if (s.kind === 'message' || s.kind === 'state' || s.kind === 'people') reload()
  })
  // Open the closest person by default, and recover if the picked one goes.
  useEffect(() => {
    if (data && data.length > 0 && !data.some((p) => p.id === picked)) setPicked(data[0].id)
  }, [data, picked])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(''), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const pick = (id: number, next: typeof mode = null) => {
    setPicked(id)
    setMode(next)
  }
  const send = (path: string, method: string, body: unknown, done: string) =>
    api(path, { method, body })
      .then(() => {
        setProblem('')
        setToast(done)
        reload()
      })
      .catch((e: Error) => {
        setProblem(e.message)
        throw e
      })

  const q = query.trim().toLowerCase()
  const matches = (p: Person) => !q || [p.name, ...p.aliases, p.relationship, p.notes].join(' ').toLowerCase().includes(q)
  const unknown = (data ?? []).filter((p) => !p.relationship.trim())
  const people = data ?? []

  return (
    <Screen
      eyebrow="The people in your life, as she knows them"
      title="People"
      className="people-screen"
      action={
        <label className="mem-search people-search">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
            <circle cx="7" cy="7" r="5" />
            <path d="M11 11l3.5 3.5" />
          </svg>
          <input type="search" placeholder="Search names, nicknames, notes" aria-label="Search people" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
      }
    >
      <Problem message={problem || error} />
      {data?.length === 0 && <Empty>Nobody yet. She remembers people when you mention them by name.</Empty>}
      {people.length > 0 && (
        <div className="people">
          <div className="people-list">
            {unknown.length > 0 && !q && (
              <section className="who-card" aria-label="Who is this?">
                <div className="who-head">
                  <h2 className="label">Who is this?</h2>
                  <span className="sub">{unknown.length} to answer</span>
                </div>
                {unknown.slice(0, 2).map((p) => (
                  <div key={p.id} className="who-item">
                    <button type="button" className="who-name" onClick={() => pick(p.id)}>
                      {p.name}
                    </button>
                    {p.heard && <span className="who-heard">you said <q>{p.heard}</q></span>}
                    <div className="quick">
                      {answers.map((a) => (
                        <button key={a} type="button" onClick={() => void send(`/api/people/${p.id}`, 'PUT', { relationship: a }, `${p.name} is your ${a}. She will remember.`).catch(() => {})}>
                          {a}
                        </button>
                      ))}
                      <button type="button" onClick={() => pick(p.id, 'merge')}>
                        same as…
                      </button>
                    </div>
                  </div>
                ))}
              </section>
            )}
            {groups.map((g) => {
              const inGroup = people.filter((p) => groupOf(p.relationship) === g && matches(p))
              if (inGroup.length === 0) return null
              return (
                <section key={g} className="people-group" aria-label={g}>
                  <h2 className="label">
                    <span>{g}</span>
                    <span>{inGroup.length}</span>
                  </h2>
                  <ul>
                    {inGroup.map((p) => (
                      <li key={p.id}>
                        <button type="button" aria-pressed={p.id === picked} onClick={() => pick(p.id)}>
                          <Avatar p={p} />
                          <span className="person-line">
                            <span className="person-name">{p.name}</span>
                            <span className="sub">{[p.relationship || 'not sure yet', isZero(p.last_mentioned) ? '' : when(p.last_mentioned)].filter(Boolean).join(' · ')}</span>
                          </span>
                          <span className="close-dot" title={closenessWord(p.closeness)} style={{ opacity: 0.2 + p.closeness * 0.8 }} />
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              )
            })}
            {q && !people.some(matches) && <Empty>Nobody matches “{query.trim()}”.</Empty>}
          </div>
          <div className="people-detail">
            {picked !== null && <Detail id={picked} key={picked} people={people} mode={mode} setMode={setMode} pick={pick} send={send} onProblem={setProblem} onToast={setToast} />}
          </div>
        </div>
      )}
      {toast && (
        <div className="mem-toast" role="status">
          <span>{toast}</span>
        </div>
      )}
    </Screen>
  )
}

interface DetailProps {
  id: number
  people: Person[]
  mode: 'edit' | 'merge' | 'forget' | null
  setMode(m: DetailProps['mode']): void
  pick(id: number, mode?: DetailProps['mode']): void
  send(path: string, method: string, body: unknown, done: string): Promise<unknown>
  onProblem(message: string): void
  onToast(text: string): void
}

function Detail({ id, people, mode, setMode, pick, send, onProblem, onToast }: DetailProps) {
  const { data, error, reload } = useLoad<PersonDetail>(`/api/people/${id}`)
  const [note, setNote] = useState('')
  useSignals((s) => {
    if (s.kind === 'message' || s.kind === 'state' || s.kind === 'people' || s.kind === 'memories') reload()
  })
  if (!data) return <Problem message={error} />
  const p = data.person
  const notes = p.notes.split('\n').map((n) => n.trim()).filter(Boolean)
  const setByYou = (field: string) => p.locked.includes(field) && <span className="set-by-you">set by you</span>
  const change = (body: unknown, done: string) => send(`/api/people/${p.id}`, 'PUT', body, done).then(reload)
  const memory = (path: string, method: string, body: unknown, done: string) =>
    void api(path, { method, body })
      .then(() => {
        onToast(done)
        reload()
      })
      .catch((e: Error) => onProblem(e.message))
  const addNote = (e: FormEvent) => {
    e.preventDefault()
    if (!note.trim()) return
    void change({ notes: [...notes, note.trim()] }, 'Noted.')
      .then(() => setNote(''))
      .catch(() => {})
  }
  const peak = Math.max(1, ...data.weeks)
  const recent = data.weeks.reduce((a, b) => a + b, 0)

  return (
    <article className="person" aria-label={p.name}>
      <div className="person-top">
        <header className="person-head">
          <Avatar p={p} size="big" />
          <div>
            <h2>{p.name}</h2>
            <span className="muted">
              {p.relationship || 'relationship not known yet'}
              {setByYou('relationship')}
            </span>
          </div>
          <div className="actions">
            <button type="button" className="secondary" aria-pressed={mode === 'edit'} onClick={() => setMode(mode === 'edit' ? null : 'edit')}>
              Edit
            </button>
            <button type="button" className="secondary" aria-pressed={mode === 'merge'} onClick={() => setMode(mode === 'merge' ? null : 'merge')}>
              Merge
            </button>
            <button type="button" className="secondary danger" aria-pressed={mode === 'forget'} onClick={() => setMode(mode === 'forget' ? null : 'forget')}>
              Forget
            </button>
          </div>
        </header>

        {mode === 'edit' && <EditForm p={p} onCancel={() => setMode(null)} onSave={(body) => change(body, 'Saved. She will keep what you set.').then(() => setMode(null))} />}
        {mode === 'merge' && (
          <div className="person-panel">
            <h3 className="label">Merge {p.name} into…</h3>
            <p className="note">
              {p.name} becomes another name for the person you pick. Notes carry over, and memories and moments that name {p.name} show up under them.
            </p>
            <ul className="merge-list">
              {people
                .filter((o) => o.id !== p.id)
                .map((o) => (
                  <li key={o.id}>
                    <button type="button" onClick={() => void send(`/api/people/${p.id}/merge`, 'POST', { into: o.id }, `${p.name} merged into ${o.name}.`).then(() => pick(o.id)).catch(() => {})}>
                      <Avatar p={o} />
                      <span className="person-line">
                        <span className="person-name">{o.name}</span>
                        <span className="sub">{o.relationship || 'not sure yet'}</span>
                      </span>
                    </button>
                  </li>
                ))}
            </ul>
            <div className="actions">
              <button type="button" className="secondary" onClick={() => setMode(null)}>
                Cancel
              </button>
            </div>
          </div>
        )}
        {mode === 'forget' && (
          <div className="person-panel warn">
            <h3 className="label">Forget {p.name}?</h3>
            <p className="note">
              She stops keeping notes on {p.name} and will not add them again when the name comes up. Memories that mention {p.name} stay; forget those on their own if you want.
            </p>
            <div className="actions">
              <button type="button" className="secondary" onClick={() => setMode(null)}>
                Keep {p.name}
              </button>
              <button type="button" className="secondary danger" onClick={() => void send(`/api/people/${p.id}/forget`, 'POST', undefined, `${p.name} forgotten.`).then(() => setMode(null)).catch(() => {})}>
                Forget {p.name}
              </button>
            </div>
          </div>
        )}

        <dl className="person-facts">
          <div>
            <dt className="label">Closeness</dt>
            <dd>
              {closenessWord(p.closeness)}
              {setByYou('closeness')}
            </dd>
          </div>
          <div>
            <dt className="label">Also called</dt>
            <dd>
              {p.aliases.length > 0 ? p.aliases.join(', ') : <span className="muted">no other names</span>}
              {setByYou('aliases')}
            </dd>
          </div>
          <div>
            <dt className="label">Last came up</dt>
            <dd>{isZero(p.last_mentioned) ? <span className="muted">not recorded</span> : when(p.last_mentioned)}</dd>
          </div>
          <div>
            <dt className="label">Mentions</dt>
            <dd>{data.mentions} in all</dd>
          </div>
        </dl>

        <section>
          <div className="section-head">
            <h3 className="label">How often {p.name} comes up</h3>
            <span className="sub">{recent} in 12 weeks</span>
          </div>
          <div className="weeks" role="img" aria-label={`${recent} mentions over the last 12 weeks`}>
            {data.weeks.map((w, i) => (
              <i key={i} className={w === 0 ? 'none' : undefined} style={{ height: w === 0 ? '3px' : `${Math.max(8, (w / peak) * 100)}%` }} title={`${w} ${w === 1 ? 'mention' : 'mentions'}, ${11 - i === 0 ? 'this week' : `${11 - i} weeks ago`}`} />
            ))}
          </div>
          <div className="weeks-axis">
            <span className="sub">12 weeks ago</span>
            <span className="sub">this week</span>
          </div>
        </section>

        {data.connections.length > 0 && (
          <section>
            <h3 className="label">Often mentioned with</h3>
            <div className="person-links">
              {data.connections.map((c) => (
                <button key={c.id} type="button" onClick={() => pick(c.id)}>
                  <Avatar p={c} size="small" />
                  {c.name}
                  <span className="sub">{c.relationship || 'not sure yet'}</span>
                </button>
              ))}
            </div>
          </section>
        )}

        <section>
          <h3 className="label">What she has noted</h3>
          {notes.length === 0 ? (
            <p className="quiet">Nothing noted yet.</p>
          ) : (
            <ul className="person-notes">
              {notes.map((n, i) => (
                <li key={i}>
                  <span>{n}</span>
                  <button type="button" className="mem-act" title="Remove note" aria-label="Remove note" onClick={() => void change({ notes: notes.filter((_, j) => j !== i) }, 'Note removed.').catch(() => {})}>
                    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" aria-hidden="true">
                      <path d="M4 4l8 8M12 4l-8 8" />
                    </svg>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <form className="person-add" onSubmit={addNote}>
            <input value={note} onChange={(e) => setNote(e.target.value)} placeholder={`Add something she should know about ${p.name}`} aria-label="New note" />
            <button type="submit" className="secondary" disabled={!note.trim()}>
              Add
            </button>
          </form>
        </section>
      </div>

      {/* Only memories and moments scroll; everything above stays put. */}
      <div className="person-scroll">
        <section>
          <h3 className="label">Memories that mention {p.name}</h3>
          {data.memories.length === 0 ? (
            <p className="quiet">None yet.</p>
          ) : (
            <ul className="mem-list">
              {data.memories.map((m) => (
                <Row
                  key={m.id}
                  m={m}
                  people={people}
                  forgotten={false}
                  onForget={() => memory(`/api/memories/${m.id}/forget`, 'POST', undefined, 'Memory forgotten.')}
                  onRestore={() => {}}
                  onPin={(pinned) => memory(`/api/memories/${m.id}`, 'PUT', { pinned }, pinned ? 'Pinned.' : 'Unpinned.')}
                  onCorrect={(content) => memory(`/api/memories/${m.id}`, 'PUT', { content }, 'Corrected. The old wording is kept.')}
                />
              ))}
            </ul>
          )}
        </section>

        <section>
          <div className="section-head">
            <h3 className="label">When {p.name} came up</h3>
            {data.mentions > data.moments.length && <span className="sub">latest {data.moments.length} of {data.mentions}</span>}
          </div>
          {data.moments.length === 0 ? (
            <p className="quiet">No mentions in conversation yet.</p>
          ) : (
            <ol className="stream">
              {data.moments.map((e) => (
                <li key={e.id}>
                  <span className="time">{when(e.at)}</span>
                  <span>
                    <span className="muted">{e.kind === 'user_msg' ? data.user : data.companion}: </span>
                    {e.text}
                  </span>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
    </article>
  )
}

// The user's edit. Only fields that changed are sent, since each one sent
// is locked against what she later picks up.
function EditForm({ p, onCancel, onSave }: { p: Person; onCancel(): void; onSave(body: Record<string, unknown>): Promise<unknown> }) {
  const [name, setName] = useState(p.name)
  const [relationship, setRelationship] = useState(p.relationship)
  const [aliases, setAliases] = useState(p.aliases.join(', '))
  const [closeness, setCloseness] = useState(Math.round(p.closeness * 100))
  const save = (e: FormEvent) => {
    e.preventDefault()
    const body: Record<string, unknown> = {}
    if (name.trim() !== p.name) body.name = name.trim()
    if (relationship.trim() !== p.relationship) body.relationship = relationship.trim()
    const list = aliases.split(',').map((a) => a.trim()).filter(Boolean)
    if (list.join(',') !== p.aliases.join(',')) body.aliases = list
    if (closeness !== Math.round(p.closeness * 100)) body.closeness = closeness / 100
    if (Object.keys(body).length === 0) return onCancel()
    void onSave(body).catch(() => {})
  }
  return (
    <form className="person-panel" onSubmit={save}>
      <h3 className="label">Edit {p.name}</h3>
      <div className="person-fields">
        <div className="field">
          <label htmlFor="person-name">Name</label>
          <input id="person-name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="person-rel">Relationship to you</label>
          <input id="person-rel" list="person-rels" value={relationship} placeholder="wife, friend, colleague…" onChange={(e) => setRelationship(e.target.value)} />
          <datalist id="person-rels">
            {answers.map((a) => (
              <option key={a} value={a} />
            ))}
          </datalist>
        </div>
        <div className="field">
          <label htmlFor="person-aliases">Also called</label>
          <input id="person-aliases" value={aliases} placeholder="Nicknames, separated by commas" onChange={(e) => setAliases(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="person-close">Closeness: {closenessWord(closeness / 100)}</label>
          <input id="person-close" type="range" min={0} max={100} value={closeness} onChange={(e) => setCloseness(Number(e.target.value))} />
        </div>
      </div>
      <p className="note">What you set here is yours. She will not overwrite it from conversation.</p>
      <div className="actions">
        <button type="button" className="secondary" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="primary">Save</button>
      </div>
    </form>
  )
}
