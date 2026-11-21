import { useState } from 'react'
import { useLoad, useSignals } from '../api'
import { clock, longDate, when } from '../format'
import { Empty, Meters, Orbit, Problem, Screen } from '../parts'
import type { Memory, Mind as MindData, StreamEvent } from '../types'

// A moment is one exchange: what was said, what she answered, how it
// landed on her and what she kept from it. The stream stores these as
// separate events; a person reads them as one thing.
interface Moment {
  key: number
  at: string
  lines: StreamEvent[]
  feeling?: StreamEvent
  learned: Memory[]
  importance: number
  initiative: boolean
  note: boolean // a standalone entry such as waking up or a journal line
}

function moments(stream: StreamEvent[], learned: Memory[]): Moment[] {
  const out: Moment[] = []
  for (const e of stream) {
    const last = out[out.length - 1]
    const starts = e.kind === 'user_msg' || e.kind === 'initiative'
    // A reply to something she raised herself belongs with it.
    const answersHer = e.kind === 'user_msg' && last?.initiative && last.lines.length === 1
    if (e.kind === 'appraisal') {
      if (last && !last.note) last.feeling = e
      continue
    }
    if (e.kind === 'nova_msg' && last && !last.note) {
      last.lines.push(e)
      continue
    }
    if (starts && answersHer) {
      last.lines.push(e)
      last.importance = Math.max(last.importance, e.importance)
      continue
    }
    out.push({
      key: e.id, at: e.at, lines: [e], learned: [], importance: e.importance,
      initiative: e.kind === 'initiative', note: !starts,
    })
  }
  // A memory belongs to the last exchange that began before it was saved.
  for (const m of learned) {
    const t = new Date(m.created_at).getTime()
    const owner = [...out].reverse().find((x) => !x.note && new Date(x.at).getTime() <= t)
    owner?.learned.push(m)
  }
  return out.reverse()
}

function strength(intensity = 0): string {
  if (intensity >= 0.7) return 'strong'
  if (intensity >= 0.4) return 'clear'
  return 'mild'
}

const filters = {
  all: 'Everything',
  moved: 'What moved her',
  matters: 'What matters most',
  first: 'When she spoke first',
} as const
type Filter = keyof typeof filters

function quiet(m: Moment): boolean {
  return !m.note && !m.feeling && !m.initiative && m.learned.length === 0 && m.importance <= 2
}

export function Mind() {
  const { data, error, reload } = useLoad<MindData>('/api/mind')
  const [filter, setFilter] = useState<Filter>('all')
  const [opened, setOpened] = useState<Set<number>>(new Set())
  useSignals((s) => {
    if (s.kind === 'message' || s.kind === 'mood' || s.kind === 'proposals' || s.kind === 'state') reload()
  })
  if (!data) {
    return (
      <Screen eyebrow={longDate(new Date())} title="Mind">
        <Problem message={error} />
      </Screen>
    )
  }

  const { state } = data
  const all = moments(data.stream, data.learned)
  const shown = all.filter((m) =>
    filter === 'moved' ? !!m.feeling : filter === 'matters' ? m.importance >= 4 : filter === 'first' ? m.initiative : true,
  )
  const lastFelt = all.find((m) => m.feeling)

  // Runs of small talk fold into one row, but only in the full view.
  const rows: (Moment | Moment[])[] = []
  for (const m of shown) {
    const prev = rows[rows.length - 1]
    if (filter === 'all' && quiet(m)) {
      if (Array.isArray(prev)) prev.push(m)
      else rows.push([m])
    } else {
      rows.push(m)
    }
  }

  const speaker = (e: StreamEvent) =>
    e.kind === 'user_msg' ? <span className="muted">{state.user}: </span> : <span className="nova">{e.kind === 'initiative' ? `${state.name} reached out: ` : `${state.name}: `}</span>

  const one = (m: Moment) => (
    <li className="moment" key={m.key}>
      <span className="time">{clock(m.at)}</span>
      {m.note ? (
        <p className="muted">{m.lines[0].text}</p>
      ) : (
        <div className="moment-body">
          {m.lines.map((e) => (
            <p key={e.id}>
              {speaker(e)}
              {e.text}
            </p>
          ))}
          {(m.feeling || m.learned.length > 0 || m.importance >= 4 || m.initiative) && (
            <div className="chips">
              {m.feeling && <span className={`chip felt-${strength(m.feeling.meta?.intensity)}`}>felt {m.feeling.meta?.emotion}, {strength(m.feeling.meta?.intensity)}</span>}
              {m.feeling?.meta?.touches && <span className="chip">touches: {m.feeling.meta.touches}</span>}
              {m.initiative && m.lines[0].meta?.reason && <span className="chip">her initiative: {m.lines[0].meta.reason}</span>}
              {m.importance >= 4 && <span className="chip matters">matters a lot</span>}
              {m.learned.length > 0 && (
                <span className="chip ok" title={m.learned.map((x) => x.content).join('\n')}>
                  remembered {m.learned.length === 1 ? '1 thing' : `${m.learned.length} things`}
                </span>
              )}
            </div>
          )}
          {m.learned.length > 0 && (
            <ul className="kept">
              {m.learned.map((x) => (
                <li key={x.id}>{x.content}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </li>
  )

  return (
    <Screen eyebrow={longDate(new Date())} title="Mind">
      <Problem message={error} />

      <section className="now" aria-label="Right now">
        <div className="presence">
          <Orbit size={48} asleep={state.state === 'asleep'} />
          <div>
            <span className="presence-state">{state.state === 'asleep' ? 'Asleep' : `Feeling ${state.feeling}`}</span>
            <span className="presence-feeling">
              {lastFelt?.feeling ? `since “${lastFelt.feeling.meta?.about ?? ''}”, ${clock(lastFelt.feeling.at)}` : 'nothing has moved her yet today'}
            </span>
          </div>
        </div>
        <Meters mood={state.mood} />
      </section>

      <div className="mind">
        <section className="timeline">
          <div className="timeline-head">
            <h2>Today, moment by moment</h2>
            <div className="filters" role="group" aria-label="Show">
              {(Object.keys(filters) as Filter[]).map((f) => (
                <button type="button" key={f} aria-pressed={filter === f} onClick={() => setFilter(f)}>
                  {filters[f]}
                </button>
              ))}
            </div>
          </div>
          {rows.length === 0 ? (
            <Empty>{all.length === 0 ? 'Nothing has happened yet today.' : 'Nothing like that today.'}</Empty>
          ) : (
            <ol className="moments">
              {rows.map((row) => {
                if (!Array.isArray(row)) return one(row)
                if (row.length === 1) return one(row[0])
                const id = row[0].key
                const open = opened.has(id)
                return [
                  <li className="fold" key={`fold-${id}`}>
                    <button
                      type="button"
                      aria-expanded={open}
                      onClick={() => {
                        const next = new Set(opened)
                        if (open) next.delete(id)
                        else next.add(id)
                        setOpened(next)
                      }}
                    >
                      <span className="time">{clock(row[row.length - 1].at)}</span>
                      <span>{open ? 'Hide' : 'Show'} {row.length} quieter moments: small talk, nothing that moved her</span>
                    </button>
                  </li>,
                  ...(open ? row.map(one) : []),
                ]
              })}
            </ol>
          )}
        </section>

        <aside className="mind-side">
          <section>
            <h2>On her mind</h2>
            {data.intentions.length === 0 ? (
              <p className="quiet">Nothing she is waiting to bring up.</p>
            ) : (
              <ul className="intentions">
                {data.intentions.map((i) => (
                  <li key={i.id}>
                    <span>{i.text}</span>
                    <span className="sub">{i.status === 'sent' ? 'asked, waiting for an answer' : when(i.trigger_at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section>
            <h2>Values</h2>
            <ul className="values">
              {data.values.map((v) => (
                <li key={v.id}>
                  <span>{v.text}</span>
                  {v.origin === 'hers' && <span className="sub hers">hers · you approved it</span>}
                </li>
              ))}
            </ul>
            {data.pending_proposals > 0 && (
              <a className="link" href="#sleep">
                {data.pending_proposals === 1 ? 'One change is waiting for you' : `${data.pending_proposals} changes are waiting for you`}
              </a>
            )}
          </section>
        </aside>
      </div>
    </Screen>
  )
}
