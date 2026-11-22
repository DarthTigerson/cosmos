import { useEffect, useState } from 'react'
import { api, useLoad, useSignals } from './api'
import { clock } from './format'
import { Footer } from './Footer'
import { NavIcon } from './NavIcon'
import { TitleBar } from './TitleBar'
import { Problem } from './parts'
import { Calendar } from './screens/Calendar'
import { Lists } from './screens/Lists'
import { Books } from './screens/Books'
import { Podcasts } from './screens/Podcasts'
import { Memory } from './screens/Memory'
import { Mind } from './screens/Mind'
import { Models } from './screens/Models'
import { Nova } from './screens/Nova'
import { People } from './screens/People'
import { Settings } from './screens/Settings'
import { Sleep } from './screens/Sleep'
import type { SessionsView, State } from './types'

const screens = ['nova', 'mind', 'memory', 'people', 'calendar', 'lists', 'podcasts', 'books', 'sleep', 'models', 'settings'] as const
type ScreenName = (typeof screens)[number]

// Whether the side nav is folded to its icons, kept between launches.
const FOLDED_KEY = 'cosmos-nav-folded'
function readFolded(): boolean {
  try {
    return localStorage.getItem(FOLDED_KEY) === '1'
  } catch {
    return false
  }
}

function fromHash(): ScreenName {
  // A screen may carry a detail after a slash: #people/12.
  const name = location.hash.replace('#', '').split('/')[0]
  return (screens as readonly string[]).includes(name) ? (name as ScreenName) : 'nova'
}

export function App() {
  const [screen, setScreen] = useState<ScreenName>(fromHash)
  const [open, setOpen] = useState(true)
  const [folded, setFolded] = useState(readFolded)
  const [problem, setProblem] = useState('')
  const { data: state, error, reload } = useLoad<State>('/api/state')
  const sessions = useLoad<SessionsView>('/api/sessions')

  useEffect(() => {
    const onHash = () => setScreen(fromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  useEffect(() => {
    try {
      localStorage.setItem(FOLDED_KEY, folded ? '1' : '0')
    } catch {
      // Not kept; it still folds for now.
    }
  }, [folded])

  // Cmd+B folds the nav. In the app it comes from the View menu, so it
  // works while typing too; in a plain browser, from the keyboard.
  useEffect(() => {
    const toggle = () => setFolded((f) => !f)
    if (window.cosmos?.onToggleNav) return window.cosmos.onToggleNav(toggle)
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'b') {
        e.preventDefault()
        toggle()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
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
  const name = state?.name ?? 'Companion'
  const labels: Record<ScreenName, string> = {
    nova: name, mind: 'Mind', memory: 'Memory', people: 'People', calendar: 'Calendar', lists: 'Lists', podcasts: 'Podcasts', books: 'Books', sleep: 'Sleep', models: 'Models', settings: 'Settings',
  }
  const asleep = state?.state === 'asleep'
  const list = sessions.data?.open ?? []
  const current = sessions.data?.current ?? 0

  return (
    <div className="frame">
      <TitleBar title={labels[screen]} asleep={asleep} />
      <div className="app">
        <aside className={folded ? 'nav folded' : 'nav'}>
          <nav aria-label="Sections">
            <div className="nav-nova">
              <a href="#nova" aria-current={screen === 'nova' ? 'page' : undefined} className="nav-link" aria-label={name} data-name={name}>
                <NavIcon name="nova" />
                <span className="nav-word">{name}</span>
              </a>
              <button type="button" className="nav-toggle" aria-expanded={open} aria-label={open ? 'Hide sessions' : 'Show sessions'} onClick={() => setOpen(!open)}>
                <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none" className={open ? 'turned' : ''}>
                  <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
            </div>
            {open && !folded && (
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
              <a key={s} href={`#${s}`} aria-current={screen === s ? 'page' : undefined} className={s === 'models' ? 'nav-link last' : 'nav-link'} aria-label={labels[s]} data-name={labels[s]}>
                <NavIcon name={s} />
                <span className="nav-word">{labels[s]}</span>
                {s === 'sleep' && !!state?.pending_proposals && (
                  <>
                    <span className="badge">{state.pending_proposals} to review</span>
                    <i className="nav-dot" aria-hidden="true" />
                  </>
                )}
              </a>
            ))}
          </nav>
          <button type="button" className="nav-fold" aria-expanded={!folded} aria-label={folded ? 'Unfold the side nav' : 'Fold the side nav'} title={`${folded ? 'Unfold' : 'Fold'} the side nav (⌘B)`} onClick={() => setFolded(!folded)}>
            <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M10 3L5 8l5 5" />
            </svg>
          </button>
        </aside>
        {screen === 'nova' && <Nova state={state} />}
        {screen === 'mind' && <Mind />}
        {screen === 'memory' && <Memory />}
        {screen === 'people' && <People />}
        {screen === 'calendar' && <Calendar />}
        {screen === 'lists' && <Lists />}
        {screen === 'podcasts' && <Podcasts />}
        {screen === 'books' && <Books />}
        {screen === 'sleep' && <Sleep />}
        {screen === 'models' && <Models asleep={state?.state === 'asleep'} />}
        {screen === 'settings' && <Settings onSaved={reload} />}
      </div>
      <Footer state={state} error={error} screen={screen} />
    </div>
  )
}
