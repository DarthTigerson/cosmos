import { useState } from 'react'
import { api, useLoad, useSignals } from '../api'
import { clock, longDate, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { KeptSession, Proposal, SessionDetail, SleepView } from '../types'

// One session she slept on: what it was, what she kept, and the thread
// itself for anyone who wants to read it back.
function Kept({ s, user, name }: { s: KeptSession; user: string; name: string }) {
  const [thread, setThread] = useState<SessionDetail | null>(null)
  const [open, setOpen] = useState(false)
  const [problem, setProblem] = useState('')
  async function toggle() {
    if (!open && !thread) {
      try {
        setThread(await api<SessionDetail>(`/api/sessions/${s.id}`))
      } catch (e) {
        setProblem((e as Error).message)
        return
      }
    }
    setOpen(!open)
  }
  return (
    <article className="kept-session">
      <header>
        <h3>{s.title || 'Untitled session'}</h3>
        <span className={s.started_by === 'nova' ? 'sub hers-start' : 'sub'}>
          {clock(s.started_at)} to {clock(s.ended_at)} · {s.messages} messages{s.started_by === 'nova' && ' · she started it'}
        </span>
      </header>
      {s.summary && <p className="muted">{s.summary}</p>}
      {s.remembered.length === 0 ? (
        <p className="quiet">Nothing new to keep from this one.</p>
      ) : (
        <ul className="plain">
          {s.remembered.map((m) => (
            <li key={m.id}>
              <span className="kind">{m.kind === 'reflection' ? 'understood ' : 'remembered '}</span>
              {m.content}
            </li>
          ))}
        </ul>
      )}
      {(s.mistakes ?? []).length > 0 && (
        <ul className="plain">
          {s.mistakes.map((m) => (
            <li key={m}>
              <span className="kind wrong">got wrong </span>
              {m}
            </li>
          ))}
        </ul>
      )}
      <button type="button" className="text" aria-expanded={open} onClick={() => void toggle()}>
        {open ? 'Hide the conversation' : 'Read the conversation'}
      </button>
      <Problem message={problem} />
      {open && thread && (
        <ol className="transcript">
          {thread.messages.map((m) => (
            <li key={m.id}>
              <span className="time">{clock(m.at)}</span>
              <span>
                <span className={m.kind === 'user_msg' ? 'muted' : 'nova'}>{m.kind === 'user_msg' ? user : name}: </span>
                {m.text}
              </span>
            </li>
          ))}
        </ol>
      )}
    </article>
  )
}

const kindLabel: Record<Proposal['kind'], string> = { value: 'a value', writing: 'how I write', planning: 'how I plan' }

function planWords(mode: string, intent: string): string {
  if (mode === 'once') return `Plan ${intent} requests in one go, up front.`
  if (mode === 'steps') return `Plan ${intent} requests one step at a time.`
  return `Choose for each ${intent} request.`
}

function sides(p: Proposal): { now: string; next: string } {
  if (p.kind === 'planning') return { now: planWords(p.before, p.target), next: planWords(p.after, p.target) }
  if (p.kind === 'value' && p.target === 'new') return { now: 'Not one of my values yet.', next: p.after }
  if (p.kind === 'writing') return { now: 'No rule about this yet.', next: p.after }
  return { now: p.before, next: p.after }
}

function Stat({ n, label }: { n: number; label: string }) {
  return (
    <div className="stat">
      <span className="stat-n">{n}</span>
      <span className="stat-label">{label}</span>
    </div>
  )
}

function nightLabel(at: string, latest: boolean): string {
  if (latest) return 'Last sleep'
  return new Date(at).toLocaleDateString(undefined, { weekday: 'long' })
}

export function Sleep() {
  // The night being looked at: empty means the latest.
  const [night, setNight] = useState('')
  const { data, error, reload } = useLoad<SleepView>(night ? `/api/sleep?at=${encodeURIComponent(night)}` : '/api/sleep')
  const [problem, setProblem] = useState('')
  const [starting, setStarting] = useState(false)

  useSignals((s) => {
    if (s.kind === 'state' || s.kind === 'proposals') reload()
    if (s.kind === 'sleep') {
      setStarting(false)
      const failed = (s.data as { error?: string; skipped?: string } | null) ?? {}
      setProblem(failed.error ? `She could not sleep: ${failed.error}` : failed.skipped ? `No sleep needed: ${failed.skipped}.` : '')
      setNight('')
      reload()
    }
  })

  async function act(path: string, body?: unknown) {
    setProblem('')
    try {
      await api(path, { method: 'POST', body })
      reload()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  const asleep = data?.state.state === 'asleep' || starting
  const slept = data?.last_sleep_at
  const nights = data?.nights ?? []
  const latest = night === '' || night === nights[0]
  const viewing = data?.viewing && !isNaN(new Date(data.viewing).getTime()) && new Date(data.viewing).getFullYear() > 2000 ? data.viewing : ''
  const remembered = (data?.sessions ?? []).reduce((n, s) => n + s.remembered.length, 0)
  const mistakes = (data?.sessions ?? []).reduce((n, s) => n + (s.mistakes ?? []).length, 0)

  return (
    <Screen
      eyebrow={viewing ? `Slept ${longDate(new Date(viewing))}, ${clock(viewing)}` : slept ? `Last slept ${when(slept)}` : 'Has not slept yet'}
      title="Morning review"
      action={
        <button
          type="button"
          className="secondary"
          disabled={asleep}
          onClick={() => {
            setStarting(true)
            void act('/api/sleep')
          }}
        >
          {asleep ? 'Asleep…' : 'Sleep now'}
        </button>
      }
    >
      <Problem message={error || problem} />
      {nights.length > 1 && (
        <nav className="nights" aria-label="Nights">
          {nights.map((n, i) => (
            <button type="button" key={n} aria-current={(i === 0 && night === '') || n === night ? 'true' : undefined} onClick={() => setNight(i === 0 ? '' : n)}>
              <span>{nightLabel(n, i === 0)}</span>
              <span className="sub">
                {new Date(n).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} · {clock(n)}
              </span>
            </button>
          ))}
        </nav>
      )}
      {data && !slept && <Empty>She reflects on the day when she sleeps. It runs at night, or use Sleep now.</Empty>}
      {data && slept && (
        <div className="morning">
          <div className="morning-night">
            <section className="journal-card">
              <span className="sub strong">her journal</span>
              {data.journal ? <p>{data.journal}</p> : <p className="muted">No journal from this sleep.</p>}
            </section>
            <section className="slept">
              <h2>The conversations I slept on</h2>
              {data.sessions.length === 0 && <Empty>None that night.</Empty>}
              {data.sessions.map((s) => (
                <Kept s={s} user={data.state.user} name={data.state.name} key={s.id} />
              ))}
            </section>
          </div>

          <div className="morning-yours">
            <section className="stats">
              <Stat n={data.sessions.length} label="sessions" />
              <Stat n={remembered} label="remembered" />
              <Stat n={mistakes} label={mistakes === 1 ? 'mistake' : 'mistakes'} />
              <Stat n={latest ? data.questions.length : data.reflections.length} label={latest ? 'questions' : 'understood'} />
            </section>

            {latest && (
              <section className="morning-block">
                <h2>Waiting for your yes</h2>
                {data.pending.length === 0 && <Empty>Nothing she wants to change.</Empty>}
                {data.pending.map((p) => {
                  const { now, next } = sides(p)
                  return (
                    <article className="proposal" key={p.id}>
                      <span className={`kind ${p.kind}`}>{kindLabel[p.kind]}</span>
                      <p className="proposal-next">{next}</p>
                      <p className="muted">Now: {now}</p>
                      {p.reason && <p className="muted">Why: {p.reason}</p>}
                      <div className="actions">
                        <button type="button" className="primary" onClick={() => void act(`/api/proposals/${p.id}`, { approve: true })}>
                          Yes, change it
                        </button>
                        <button type="button" className="secondary" onClick={() => void act(`/api/proposals/${p.id}`, { approve: false })}>
                          No
                        </button>
                      </div>
                    </article>
                  )
                })}
              </section>
            )}

            <section className="morning-block">
              <h2>What I understand now</h2>
              {data.reflections.length === 0 ? (
                <Empty>Nothing new from this sleep.</Empty>
              ) : (
                <ul className="understood">
                  {data.reflections.map((r) => (
                    <li key={r.id}>
                      <p>{r.content}</p>
                      {r.source && (
                        <details>
                          <summary>where this comes from</summary>
                          <p className="muted">{r.source}</p>
                        </details>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>

            {latest && (
              <section className="morning-block">
                <h2>What I want to ask you</h2>
                {data.questions.length === 0 ? (
                  <Empty>Nothing she is wondering about.</Empty>
                ) : (
                  <ul className="questions">
                    {data.questions.map((q) => (
                      <li key={q.id}>
                        <span>{q.text}</span>
                        <span className="sub">{q.status === 'sent' ? 'asked, waiting for your answer' : 'she will ask at a quiet moment'}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            )}

            {latest && data.tidied.length > 0 && (
              <section className="morning-block">
                <h2>What I tidied</h2>
                <p className="note">Done without asking. You can undo any of these.</p>
                <ul className="tidied">
                  {data.tidied.map((t) => (
                    <li key={t.id}>
                      <span className={t.undone ? 'muted struck' : ''}>{t.summary}</span>
                      <button type="button" className="text" disabled={t.undone} onClick={() => void act(`/api/tidy/${t.id}/undo`)}>
                        {t.undone ? 'Undone' : 'Undo'}
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {latest && data.decided.length > 0 && (
              <section className="morning-block">
                <h2>Decided before</h2>
                <ul className="decided">
                  {data.decided.map((p) => (
                    <li key={p.id}>
                      <span className="clamp">{sides(p).next}</span>
                      <span className={p.status === 'approved' ? 'sub hers' : 'sub'}>
                        {p.status} · {kindLabel[p.kind]}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </div>
        </div>
      )}
    </Screen>
  )
}
