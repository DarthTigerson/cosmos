import { useLoad, useSignals } from '../api'
import { when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Memory as MemoryRow } from '../types'

// Each memory shows what it rests on: the thing you said, or for a
// reflection the moments of the day it came from.
function Row({ m }: { m: MemoryRow }) {
  const basis = m.source.split('\n').filter(Boolean)
  return (
    <li className="memory">
      <span>{m.content}</span>
      {basis.length > 0 && (
        <span className="basis">
          {m.kind === 'reflection' ? 'from: ' : 'you said: '}
          {basis.map((b, i) => (
            <q key={i}>{b}</q>
          ))}
        </span>
      )}
      {m.replaced && <span className="basis">before that she believed: {m.replaced}</span>}
      <span className="sub">{when(m.created_at)}</span>
    </li>
  )
}

export function Memory() {
  const { data, error, reload } = useLoad<MemoryRow[]>('/api/memories')
  useSignals((s) => {
    if (s.kind === 'state' || s.kind === 'proposals' || s.kind === 'message') reload()
  })
  const facts = data?.filter((m) => m.kind === 'fact') ?? []
  const reflections = data?.filter((m) => m.kind === 'reflection').reverse() ?? []
  const categories = [...new Set(facts.map((m) => m.category))].sort()

  return (
    <Screen eyebrow="What stays with her" title="Memory">
      <Problem message={error} />
      {data && (
        <div className="columns two">
          <section>
            <h2>What she knows about you</h2>
            <p className="note">Lasting facts, each with the words of yours it came from. When a fact changes, the old one is kept on record.</p>
            {facts.length === 0 && <Empty>Nothing yet. She picks things up as you talk.</Empty>}
            {categories.map((c) => (
              <div key={c} className="group">
                <h3 className="label">{c}</h3>
                <ul className="memories">
                  {facts.filter((m) => m.category === c).map((m) => (
                    <Row m={m} key={m.id} />
                  ))}
                </ul>
              </div>
            ))}
          </section>
          <section>
            <h2>What she has come to understand</h2>
            <p className="note">Reflections from her sleep, newest first, with the moments behind them.</p>
            {reflections.length === 0 ? (
              <Empty>No reflections yet. They come from sleep.</Empty>
            ) : (
              <ul className="memories">
                {reflections.map((m) => (
                  <Row m={m} key={m.id} />
                ))}
              </ul>
            )}
          </section>
        </div>
      )}
    </Screen>
  )
}
