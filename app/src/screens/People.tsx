import { useEffect, useState } from 'react'
import { useLoad, useSignals } from '../api'
import { closenessWord, isZero, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Person, PersonDetail } from '../types'

export function People() {
  const { data, error, reload } = useLoad<Person[]>('/api/people')
  const [picked, setPicked] = useState<number | null>(null)
  useSignals((s) => {
    if (s.kind === 'message' || s.kind === 'state') reload()
  })
  // Open the closest person by default, and recover if the picked one goes.
  useEffect(() => {
    if (data && data.length > 0 && !data.some((p) => p.id === picked)) setPicked(data[0].id)
  }, [data, picked])

  return (
    <Screen eyebrow="The people in your life, as she knows them" title="People">
      <Problem message={error} />
      {data?.length === 0 && <Empty>Nobody yet. She remembers people when you mention them by name.</Empty>}
      {data && data.length > 0 && (
        <div className="people">
          <ul className="people-list" aria-label="People">
            {data.map((p) => (
              <li key={p.id}>
                <button type="button" aria-pressed={p.id === picked} onClick={() => setPicked(p.id)}>
                  <span className="person-name">{p.name}</span>
                  <span className="sub">{[p.relationship, closenessWord(p.closeness)].filter(Boolean).join(' · ')}</span>
                </button>
              </li>
            ))}
          </ul>
          {picked !== null && <Detail id={picked} key={picked} />}
        </div>
      )}
    </Screen>
  )
}

function Detail({ id }: { id: number }) {
  const { data, error, reload } = useLoad<PersonDetail>(`/api/people/${id}`)
  useSignals((s) => {
    if (s.kind === 'message' || s.kind === 'state') reload()
  })
  if (!data) return <Problem message={error} />
  const p = data.person
  const notes = p.notes.split('\n').map((n) => n.trim()).filter(Boolean)
  return (
    <article className="person" aria-label={p.name}>
      <header>
        <h2>{p.name}</h2>
        <span className="muted">{p.relationship || 'relationship not known yet'}</span>
      </header>

      <dl className="facts">
        <dt>Closeness to you</dt>
        <dd>
          <div className="closeness">
            <div className="track" role="meter" aria-label={`How close ${p.name} is`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(p.closeness * 100)}>
              <div className="fill" style={{ width: `${Math.round(p.closeness * 100)}%` }} />
            </div>
            <span>{closenessWord(p.closeness)}</span>
          </div>
        </dd>
        {p.aliases.length > 0 && (
          <>
            <dt>Also called</dt>
            <dd>{p.aliases.join(', ')}</dd>
          </>
        )}
        <dt>Last came up</dt>
        <dd>{isZero(p.last_mentioned) ? 'not recorded' : when(p.last_mentioned)}</dd>
      </dl>

      <section>
        <h3 className="label">What she has noted</h3>
        {notes.length === 0 ? (
          <p className="quiet">Nothing noted yet beyond the relationship.</p>
        ) : (
          <ul className="thoughts">
            {notes.map((n, i) => (
              <li key={i}>{n}</li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h3 className="label">Memories that mention {p.name}</h3>
        {data.memories.length === 0 ? (
          <p className="quiet">None yet.</p>
        ) : (
          <ul className="plain">
            {data.memories.map((m) => (
              <li key={m.id}>
                {m.content}
                <span className="sub"> · {m.kind === 'reflection' ? 'her reflection' : m.category}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h3 className="label">When {p.name} came up</h3>
        {data.moments.length === 0 ? (
          <p className="quiet">No recent mentions in conversation.</p>
        ) : (
          <ol className="stream">
            {data.moments.map((e) => (
              <li key={e.id}>
                <span className="time">{when(e.at)}</span>
                <span>
                  <span className="muted">{e.kind === 'user_msg' ? data.user : 'Nova'}: </span>
                  {e.text}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </article>
  )
}
