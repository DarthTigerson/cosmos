import { useLoad, useSignals } from '../api'
import { clock, longDate } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { CalendarItem } from '../types'

export function Calendar() {
  const { data, error, reload } = useLoad<CalendarItem[]>('/api/calendar')
  useSignals((s) => {
    if (s.kind === 'message') reload()
  })
  const days = new Map<string, CalendarItem[]>()
  for (const item of data ?? []) {
    const key = longDate(new Date(item.at))
    days.set(key, [...(days.get(key) ?? []), item])
  }
  return (
    <Screen eyebrow="The next twelve months" title="Calendar">
      <Problem message={error} />
      {data?.length === 0 && <Empty>Nothing coming up. Ask her to add an event or to remind you of something.</Empty>}
      <div className="days">
        {[...days.entries()].map(([day, items]) => (
          <section key={day}>
            <h2 className="label">{day}</h2>
            <ul className="agenda">
              {items.map((item) => (
                <li key={`${item.id}-${item.at}`}>
                  <span className="time">{item.duration_min === 1440 ? 'all day' : clock(item.at)}</span>
                  <span>
                    {item.title}
                    {item.location && <span className="muted"> · {item.location}</span>}
                  </span>
                  <span className="sub">
                    {item.kind}
                    {item.repeat && ` · every ${{ daily: 'day', weekly: 'week', monthly: 'month', yearly: 'year' }[item.repeat]}`}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </Screen>
  )
}
