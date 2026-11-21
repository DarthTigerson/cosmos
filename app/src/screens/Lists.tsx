import { useState } from 'react'
import { api, useLoad, useSignals } from '../api'
import { Empty, Problem, Screen } from '../parts'
import type { ListItem } from '../types'

// One list: its open items, the ones ticked off today, and a box to add
// to it by hand.
function OneList({ name, items, onChange }: { name: string; items: ListItem[]; onChange: () => void }) {
  const [text, setText] = useState('')
  const [problem, setProblem] = useState('')
  const open = items.filter((i) => !i.done)
  const done = items.filter((i) => i.done)

  async function run(work: () => Promise<unknown>) {
    setProblem('')
    try {
      await work()
      onChange()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  return (
    <section className="list" aria-label={`${name} list`}>
      <header className="list-head">
        <h2>{name}</h2>
        <span className="sub">{open.length === 0 ? 'all done' : `${open.length} to go`}</span>
      </header>
      <ul className="list-items">
        {[...open, ...done].map((item) => (
          <li key={item.id} className={item.done ? 'ticked' : undefined}>
            <input
              id={`item-${item.id}`}
              type="checkbox"
              checked={item.done}
              onChange={(e) => void run(() => api(`/api/lists/items/${item.id}`, { method: 'PUT', body: { done: e.target.checked } }))}
            />
            <label htmlFor={`item-${item.id}`}>{item.text}</label>
            <button type="button" className="list-remove" aria-label={`Remove ${item.text}`} onClick={() => void run(() => api(`/api/lists/items/${item.id}`, { method: 'DELETE' }))}>
              <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none">
                <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
              </svg>
            </button>
          </li>
        ))}
      </ul>
      <form
        className="list-add"
        onSubmit={(e) => {
          e.preventDefault()
          const value = text.trim()
          if (!value) return
          void run(async () => {
            await api('/api/lists', { method: 'POST', body: { list: name, text: value } })
            setText('')
          })
        }}
      >
        <label htmlFor={`add-${name}`} className="visually-hidden">
          Add to {name}
        </label>
        <input id={`add-${name}`} type="text" value={text} placeholder={`Add to ${name}`} onChange={(e) => setText(e.target.value)} />
        <button type="submit" className="secondary" disabled={!text.trim()}>
          Add
        </button>
      </form>
      <Problem message={problem} />
    </section>
  )
}

export function Lists() {
  const { data, error, reload } = useLoad<ListItem[]>('/api/lists')
  const [fresh, setFresh] = useState('')
  const [started, setStarted] = useState<string[]>([])
  useSignals((s) => {
    if (s.kind === 'lists') reload()
  })

  const lists = new Map<string, ListItem[]>()
  for (const name of started) lists.set(name, [])
  for (const item of data ?? []) lists.set(item.list, [...(lists.get(item.list) ?? []), item])

  return (
    <Screen eyebrow="Shopping, to-dos and anything else" title="Lists">
      <Problem message={error} />
      {data && lists.size === 0 && <Empty>No lists yet. Tell her "add milk to the shopping list", or start one below.</Empty>}
      <div className="lists">
        {[...lists.entries()].map(([name, items]) => (
          <OneList key={name} name={name} items={items} onChange={reload} />
        ))}
      </div>
      <form
        className="list-add new-list"
        onSubmit={(e) => {
          e.preventDefault()
          const name = fresh.trim().toLowerCase()
          if (!name) return
          if (!lists.has(name)) setStarted([...started, name])
          setFresh('')
        }}
      >
        <label htmlFor="new-list" className="visually-hidden">
          Name of a new list
        </label>
        <input id="new-list" type="text" value={fresh} placeholder="Start a new list, e.g. packing" onChange={(e) => setFresh(e.target.value)} />
        <button type="submit" className="secondary" disabled={!fresh.trim()}>
          Start list
        </button>
      </form>
    </Screen>
  )
}
