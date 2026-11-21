import { useEffect, useState } from 'react'
import { api, useLoad, useSignals } from './api'
import { clock } from './format'
import { Footer } from './Footer'
import { TitleBar } from './TitleBar'
import { Orbit, Problem } from './parts'
import { Calendar } from './screens/Calendar'
import { Lists } from './screens/Lists'
import { Podcasts } from './screens/Podcasts'
import { Memory } from './screens/Memory'
import { Mind } from './screens/Mind'
import { Models } from './screens/Models'
import { Nova } from './screens/Nova'
import { People } from './screens/People'
import { Settings } from './screens/Settings'
import { Sleep } from './screens/Sleep'
import type { SessionsView, State } from './types'

const screens = ['nova', 'mind', 'memory', 'people', 'calendar', 'lists', 'podcasts', 'sleep', 'models', 'settings'] as const
type ScreenName = (typeof screens)[number]

function fromHash(): ScreenName {
  const name = location.hash.replace('#', '')
  return (screens as readonly string[]).includes(name) ? (name as ScreenName) : 'nova'
}

export function App() {
  const [screen, setScreen] = useState<ScreenName>(fromHash)
  const [open, setOpen] = useState(true)
  const [problem, setProblem] = useState('')
  const { data: state, error, reload } = useLoad<State>('/api/state')
  const sessions = useLoad<SessionsView>('/api/sessions')

  useEffect(() => {
    const onHash = () => setScreen(fromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  useSignals((s) => {
    if (s.kind === 'mood' || s.kind === 'state' || s.kind === 'proposals' || s.kind === 'sleep') reload()
    if (s.kind === 'sessions' || s.kind === 'state' || s.kind === 'sleep') sessions.reload()
  })

  // Starting or reopening a session always lands on her screen.
  async function go(path: string) {
    setProblem('')
    try {
      await api(path, { method: 'POST' })
      sessions.reload()
      location.hash = '#nova'
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  // The first tab carries her name: the other tabs are parts of her.
  const name = state?.name ?? 'Nova'
  const labels: Record<ScreenName, string> = {
    nova: name, mind: 'Mind', memory: 'Memory', people: 'People', calendar: 'Calendar', lists: 'Lists', podcasts: 'Podcasts', sleep: 'Sleep', models: 'Models', settings: 'Settings',
  }
  const asleep = state?.state === 'asleep'
  const list = sessions.data?.open ?? []
  const current = sessions.data?.current ?? 0

  return (
    <div className="frame">
      <TitleBar title={labels[screen]} />
      <div className="app">
        <aside className="nav">
          <div className="brand">
            <Orbit size={26} asleep={asleep} />
            <span>Cosmos Platform</span>
          </div>
          <nav aria-label="Sections">
            <div className="nav-nova">
              <a href="#nova" aria-current={screen === 'nova' ? 'page' : undefined} className="nav-link">
                {name}
              </a>
              <button type="button" className="nav-toggle" aria-expanded={open} aria-label={open ? 'Hide sessions' : 'Show sessions'} onClick={() => setOpen(!open)}>
                <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none" className={open ? 'turned' : ''}>
                  <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
            </div>
            {open && (
              <div className="sessions">
                <span className="sessions-head">Since she last slept</span>
                <button type="button" className="session new" onClick={() => void go('/api/sessions')}>
                  New session
                </button>
                {list.length === 0 && <span className="sessions-none">Nothing open yet.</span>}
                {list.map((s) => (
                  <button type="button" key={s.id} className="session" aria-current={s.id === current ? 'true' : undefined} onClick={() => void go(`/api/sessions/${s.id}/open`)}>
                    <span className="session-title">{s.title || 'New session'}</span>
                    <span className={s.id === current || s.started_by === 'nova' ? 'session-when hers' : 'session-when'}>
                      {s.id === current ? 'now' : s.started_by === 'nova' ? 'hers' : clock(s.started_at)}
                    </span>
                  </button>
                ))}
                <Problem message={problem} />
              </div>
            )}
            {screens.filter((s) => s !== 'nova').map((s) => (
              <a key={s} href={`#${s}`} aria-current={screen === s ? 'page' : undefined} className={s === 'models' ? 'nav-link last' : 'nav-link'}>
                {labels[s]}
                {s === 'sleep' && !!state?.pending_proposals && <span className="badge">{state.pending_proposals} to review</span>}
              </a>
            ))}
          </nav>
        </aside>
        {screen === 'nova' && <Nova state={state} />}
        {screen === 'mind' && <Mind />}
        {screen === 'memory' && <Memory />}
        {screen === 'people' && <People />}
        {screen === 'calendar' && <Calendar />}
        {screen === 'lists' && <Lists />}
        {screen === 'podcasts' && <Podcasts />}
        {screen === 'sleep' && <Sleep />}
        {screen === 'models' && <Models />}
        {screen === 'settings' && <Settings onSaved={reload} />}
      </div>
      <Footer state={state} error={error} screen={screen} />
    </div>
  )
}
