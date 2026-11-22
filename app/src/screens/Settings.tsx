import { useEffect, useRef, useState } from 'react'
import { api, useLoad } from '../api'
import { Problem, Screen, Switch } from '../parts'
import type { Config, Voice } from '../types'
import { type Connection, Connections, type ConnectionSwitches } from './Connections'
import { VoicePicker } from './VoicePicker'

const sections = [
  ['you', 'You and her'],
  ['day', 'Her day'],
  ['thinks', 'How she thinks'],
  ['may', 'What she may do'],
  ['ears', 'Her ears'],
  ['voice', 'Her voice'],
] as const
type Section = (typeof sections)[number][0]

// Which settings each section holds, to tell which sections have changed.
const fields: Record<Exclude<Section, 'voice'>, (keyof Config)[]> = {
  you: ['user_name', 'companion_name', 'platform_name', 'location'],
  day: ['quiet_from', 'quiet_to', 'brief_hour', 'sleep_hour', 'initiative_budget', 'cooldown_minutes'],
  thinks: ['planning', 'verify_with_model', 'verify_claims'],
  may: ['web_search', 'podcasts', 'weather_watch'],
  ears: ['name_sounds_like', 'listen_window', 'room_minutes', 'hear_language'],
}

const plans: { value: Config['planning']; title: string; help: string }[] = [
  { value: 'auto', title: 'Let her choose', help: 'She picks per task and learns which works.' },
  { value: 'once', title: 'Plan in one go', help: 'Faster. Fine for simple asks.' },
  { value: 'steps', title: 'One step at a time', help: 'Slower, steadier on long tasks.' },
]

const pad = (h: number) => `${String(h).padStart(2, '0')}:00`

// One on/off setting: what it does, and what it costs.
function SwitchRow({ title, why, checked, onChange }: { title: string; why: string; checked: boolean; onChange: (on: boolean) => void }) {
  return (
    <div className="set-switch">
      <div>
        <b>{title}</b>
        <span>{why}</span>
      </div>
      <Switch label={title} checked={checked} onChange={onChange} />
    </div>
  )
}

function Field({ id, label, children }: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children}
    </div>
  )
}

function HourPicker({ id, value, onChange }: { id: string; value: number; onChange: (h: number) => void }) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(Number(e.target.value))}>
      {Array.from({ length: 24 }, (_, h) => (
        <option key={h} value={h}>
          {pad(h)}
        </option>
      ))}
    </select>
  )
}

// Her day on 24 hours: the quiet hours shaded, the morning note and sleep
// marked, and where the day is now.
function DayStrip({ cfg }: { cfg: Config }) {
  const pct = (h: number) => `${(h / 24) * 100}%`
  const now = new Date()
  const at = now.getHours() + now.getMinutes() / 60
  const from = cfg.quiet_from
  const to = cfg.quiet_to
  return (
    <div className="set-day">
      <div className="set-strip" aria-hidden="true">
        {from > to ? (
          <>
            <span className="set-quiet" style={{ left: 0, width: pct(to) }} />
            <span className="set-quiet" style={{ left: pct(from), right: 0 }} />
          </>
        ) : (
          from !== to && <span className="set-quiet" style={{ left: pct(from), width: pct(to - from) }} />
        )}
        <span className="set-tick set-tick-brief" style={{ left: pct(cfg.brief_hour) }}>
          <span>note {pad(cfg.brief_hour)}</span>
        </span>
        <span className="set-tick set-tick-sleep" style={{ left: pct(cfg.sleep_hour) }}>
          <span>sleep {pad(cfg.sleep_hour)}</span>
        </span>
        <span className="set-tick set-tick-now" style={{ left: pct(at) }}>
          <span>now</span>
        </span>
      </div>
      <div className="set-hours" aria-hidden="true">
        {[0, 3, 6, 9, 12, 15, 18, 21].map((h) => (
          <span key={h}>{String(h).padStart(2, '0')}</span>
        ))}
      </div>
      <div className="set-legend">
        <span>
          <i className="set-swatch-quiet" />
          Quiet: she does not start a conversation
        </span>
        <span>
          <i className="set-swatch-brief" />
          Morning note
        </span>
        <span>
          <i className="set-swatch-sleep" />
          Sleep
        </span>
        <span>
          <i className="set-swatch-now" />
          Now
        </span>
      </div>
    </div>
  )
}

const switchesOf = (list: Connection[]): ConnectionSwitches => Object.fromEntries(list.map((c) => [c.name, { enabled: c.enabled, allow_changes: c.allow_changes }]))

// Settings, one section at a time. The rail follows the section being
// read; every change waits in a draft until the save bar saves it. The
// sound of her voice and adding or removing a connection apply at once.
export function Settings({ onSaved }: { onSaved: () => void }) {
  const config = useLoad<Config>('/api/config')
  const voice = useLoad<Voice>('/api/voice')
  const conns = useLoad<Connection[]>('/api/connections')
  const [cfg, setCfg] = useState<Config | null>(null)
  const [words, setWords] = useState<Voice | null>(null)
  const [switches, setSwitches] = useState<ConnectionSwitches>({})
  const [problem, setProblem] = useState('')
  const [toast, setToast] = useState('')
  const [busy, setBusy] = useState(false)
  const [current, setCurrent] = useState<Section>('you')
  const scroll = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (config.data) setCfg(config.data)
  }, [config.data])
  useEffect(() => {
    if (voice.data) setWords(voice.data)
  }, [voice.data])
  // A reload keeps switches still waiting to be saved, for connections
  // that are still there.
  useEffect(() => {
    const list = conns.data
    if (list) setSwitches((was) => Object.fromEntries(list.map((c) => [c.name, was[c.name] ?? { enabled: c.enabled, allow_changes: c.allow_changes }])))
  }, [conns.data])
  useEffect(() => {
    if (!toast) return
    const timer = setTimeout(() => setToast(''), 2500)
    return () => clearTimeout(timer)
  }, [toast])

  const savedSwitches = switchesOf(conns.data ?? [])
  const switchesChanged = Object.keys(switches).some((n) => savedSwitches[n] && (switches[n].enabled !== savedSwitches[n].enabled || switches[n].allow_changes !== savedSwitches[n].allow_changes))
  const voiceChanged = !!words && !!voice.data && (words.persona !== voice.data.persona || words.examples !== voice.data.examples)
  const sectionChanged = (s: Section) => {
    if (s === 'voice') return voiceChanged
    if (s === 'may' && switchesChanged) return true
    return !!cfg && !!config.data && fields[s].some((k) => cfg[k] !== config.data?.[k])
  }
  const changed = sections.filter(([s]) => sectionChanged(s))

  const set = (patch: Partial<Config>) => cfg && setCfg({ ...cfg, ...patch })
  const setConn = (name: string, patch: Partial<ConnectionSwitches[string]>) => setSwitches((was) => ({ ...was, [name]: { ...was[name], ...patch } }))

  async function save() {
    if (!cfg || !words) return
    setBusy(true)
    setProblem('')
    try {
      const configChanged = Object.values(fields).some((keys) => keys.some((k) => cfg[k] !== config.data?.[k]))
      if (configChanged) {
        setCfg(await api<Config>('/api/config', { method: 'PUT', body: cfg }))
        config.reload()
        onSaved()
      }
      if (voiceChanged) {
        await api('/api/voice', { method: 'PUT', body: { persona: words.persona, examples: words.examples } })
        voice.reload()
      }
      if (switchesChanged && conns.data) {
        await api('/api/connections', { method: 'PUT', body: conns.data.map((c) => ({ name: c.name, ...(switches[c.name] ?? { enabled: c.enabled, allow_changes: c.allow_changes }) })) })
        conns.reload()
      }
      setToast(voiceChanged ? 'Saved. It applies from her next reply.' : 'Saved.')
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  function undo() {
    if (config.data) setCfg(config.data)
    if (voice.data) setWords(voice.data)
    setSwitches(savedSwitches)
    setProblem('')
  }

  // Connections added or removed save straight away, keeping any switches
  // still waiting to be saved.
  async function connectionAction(work: () => Promise<unknown>) {
    setBusy(true)
    setProblem('')
    try {
      await work()
      conns.reload()
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const section = (s: Section) => scroll.current?.querySelector<HTMLElement>(`[data-section="${s}"]`)
  const jump = (s: Section) => {
    const el = section(s)
    if (!el || !scroll.current) return
    scroll.current.scrollTo({ top: el.offsetTop - 16, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
  }
  const onScroll = () => {
    const el = scroll.current
    if (!el) return
    let at: Section = 'you'
    for (const [s] of sections) {
      const top = section(s)?.offsetTop
      if (top !== undefined && top - 60 <= el.scrollTop) at = s
    }
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 4) at = sections[sections.length - 1][0]
    setCurrent(at)
  }

  const cooldowns = [...new Set([15, 30, 60, 90, 120, 240, ...(cfg ? [cfg.cooldown_minutes] : [])])].sort((a, b) => a - b)

  return (
    <Screen eyebrow="How she is set up" title="Settings" className="settings-screen">
      <Problem message={config.error || voice.error || conns.error || problem} />
      {cfg && (
        <div className="set-split">
          <nav className="set-rail" aria-label="Sections">
            {sections.map(([s, label]) => (
              <button key={s} type="button" aria-current={current === s} onClick={() => jump(s)}>
                {label}
                {sectionChanged(s) && <i className="set-dot" aria-label="unsaved changes" />}
              </button>
            ))}
            <span className="label set-rail-sep">Elsewhere</span>
            <a href="#models">Models →</a>
          </nav>

          <div className="set-main">
            <div className="set-scroll" ref={scroll} onScroll={onScroll}>
              <section className="set-section" data-section="you">
                <header>
                  <h2>You and her</h2>
                  <p>Names she uses, and where home is for the weather.</p>
                </header>
                <div className="set-grid">
                  <Field id="user" label="Your name">
                    <input id="user" value={cfg.user_name} onChange={(e) => set({ user_name: e.target.value })} />
                  </Field>
                  <Field id="name" label="Her name">
                    <input id="name" value={cfg.companion_name} onChange={(e) => set({ companion_name: e.target.value })} />
                  </Field>
                  <Field id="platform" label="Platform name">
                    <input id="platform" value={cfg.platform_name ?? ''} onChange={(e) => set({ platform_name: e.target.value })} />
                  </Field>
                  <Field id="location" label="Home">
                    <input id="location" placeholder="City or town" value={cfg.location} onChange={(e) => set({ location: e.target.value })} />
                  </Field>
                </div>
              </section>

              <section className="set-section" data-section="day">
                <header>
                  <h2>Her day</h2>
                  <p>When she stays quiet, when she writes her morning note, and when she goes to sleep. Reminders you ask for always arrive on time, whatever these say.</p>
                </header>
                <DayStrip cfg={cfg} />
                <div className="set-day-fields">
                  <Field id="quiet-from" label="Quiet from">
                    <HourPicker id="quiet-from" value={cfg.quiet_from} onChange={(h) => set({ quiet_from: h })} />
                  </Field>
                  <Field id="quiet-to" label="Quiet until">
                    <HourPicker id="quiet-to" value={cfg.quiet_to} onChange={(h) => set({ quiet_to: h })} />
                  </Field>
                  <Field id="brief" label="Morning note">
                    <HourPicker id="brief" value={cfg.brief_hour} onChange={(h) => set({ brief_hour: h })} />
                  </Field>
                  <Field id="sleep" label="Sleep">
                    <HourPicker id="sleep" value={cfg.sleep_hour} onChange={(h) => set({ sleep_hour: h })} />
                  </Field>
                  <div className="field">
                    <span className="field-label" id="budget-label">
                      At most per day
                    </span>
                    <div className="set-stepper" role="group" aria-labelledby="budget-label">
                      <button type="button" aria-label="Fewer" disabled={cfg.initiative_budget <= 0} onClick={() => set({ initiative_budget: Math.max(0, cfg.initiative_budget - 1) })}>
                        −
                      </button>
                      <output aria-live="polite">{cfg.initiative_budget}</output>
                      <button type="button" aria-label="More" onClick={() => set({ initiative_budget: cfg.initiative_budget + 1 })}>
                        +
                      </button>
                    </div>
                  </div>
                  <Field id="cooldown" label="Minutes between">
                    <select id="cooldown" value={cfg.cooldown_minutes} onChange={(e) => set({ cooldown_minutes: Number(e.target.value) })}>
                      {cooldowns.map((m) => (
                        <option key={m} value={m}>
                          {m}
                        </option>
                      ))}
                    </select>
                  </Field>
                </div>
              </section>

              <section className="set-section" data-section="thinks">
                <header>
                  <h2>How she thinks</h2>
                  <p>How she plans her work and how carefully she checks it.</p>
                </header>
                <div className="set-choices" role="radiogroup" aria-label="Planning">
                  {plans.map((p) => (
                    <button key={p.value} type="button" role="radio" aria-checked={cfg.planning === p.value} className="set-choice" onClick={() => set({ planning: p.value })}>
                      <b>{p.title}</b>
                      <span>{p.help}</span>
                    </button>
                  ))}
                </div>
                <div className="set-switches">
                  <SwitchRow title="Check replies that used tools" why="The main model reads the reply again before it is sent. Adds a few seconds." checked={cfg.verify_with_model} onChange={(on) => set({ verify_with_model: on })} />
                  <SwitchRow title="Check for invented facts about you" why="Catches her claiming you said things you did not." checked={cfg.verify_claims} onChange={(on) => set({ verify_claims: on })} />
                </div>
              </section>

              <section className="set-section" data-section="may">
                <header>
                  <h2>What she may do</h2>
                  <p>Things she does on her own, and the outside services she may reach.</p>
                </header>
                <div className="set-switches">
                  <SwitchRow title="Look things up online" why="Searches go to DuckDuckGo. Nothing else leaves this Mac." checked={cfg.web_search} onChange={(on) => set({ web_search: on })} />
                  <SwitchRow title="Listen to new podcast episodes" why="Downloads and transcribes new episodes from shows you follow." checked={cfg.podcasts} onChange={(on) => set({ podcasts: on })} />
                  <SwitchRow title="Watch the weather at home" why="Tells you about rain or heat before you need to know." checked={cfg.weather_watch} onChange={(on) => set({ weather_watch: on })} />
                </div>
                <Connections
                  list={conns.data ?? []}
                  switches={switches}
                  busy={busy}
                  change={setConn}
                  remove={(name) => void connectionAction(() => api('/api/connections', { method: 'PUT', body: (conns.data ?? []).filter((c) => c.name !== name).map((c) => ({ name: c.name, enabled: c.enabled, allow_changes: c.allow_changes })) }))}
                  bringIn={() => void connectionAction(() => api('/api/connections/import', { method: 'POST' }))}
                />
              </section>

              <section className="set-section" data-section="ears">
                <header>
                  <h2>Her ears</h2>
                  <p>
                    Listening for her name, she answers when she hears “{cfg.companion_name}” or anything that sounds like it, and for a while after she replies. What others say nearby is kept for a few minutes, in memory only, so she knows what was going on when you ask her. It is never saved.
                  </p>
                </header>
                <div className="set-grid">
                  <Field id="sounds-like" label="Also answers to">
                    <input
                      id="sounds-like"
                      placeholder="Other spellings, comma separated"
                      value={(cfg.name_sounds_like ?? []).join(', ')}
                      onChange={(e) => set({ name_sounds_like: e.target.value.split(',').map((w) => w.trim()).filter(Boolean) })}
                    />
                  </Field>
                  <Field id="listen-window" label="Seconds she listens on after a reply">
                    <input id="listen-window" type="number" min={0} max={300} value={cfg.listen_window} onChange={(e) => set({ listen_window: Math.max(0, Number(e.target.value)) })} />
                  </Field>
                  <Field id="room-minutes" label="Minutes of nearby talk kept">
                    <input id="room-minutes" type="number" min={0} max={30} value={cfg.room_minutes} onChange={(e) => set({ room_minutes: Math.max(0, Number(e.target.value)) })} />
                  </Field>
                  <Field id="hear-language" label="Language you speak">
                    <input id="hear-language" placeholder="en" value={cfg.hear_language} onChange={(e) => set({ hear_language: e.target.value.trim() })} />
                  </Field>
                </div>
                <p className="note">Spellings: her name is matched by sound, so close mishearings of “{cfg.companion_name}” already count. Add one here only if she keeps missing it. Set the kept minutes to 0 to keep nothing.</p>
              </section>

              <section className="set-section" data-section="voice">
                <header>
                  <h2>Her voice</h2>
                  <p>How she sounds out loud, and how she talks in writing.</p>
                </header>
                <VoicePicker />
                {words && (
                  <>
                    <div className="set-two">
                      <Field id="persona" label="Who she is">
                        <textarea id="persona" rows={8} placeholder={words.default_persona} value={words.persona} onChange={(e) => setWords({ ...words, persona: e.target.value })} />
                      </Field>
                      <Field id="examples" label="How she talks, by example">
                        <textarea id="examples" className="mono" rows={8} placeholder={words.default_examples} value={words.examples} onChange={(e) => setWords({ ...words, examples: e.target.value })} />
                      </Field>
                    </div>
                    <p className="note">
                      Write short exchanges the way you want her to sound: when you tease her, when you tell her something that matters, when you ask a plain question. Use <code>{'{user}'}</code> and <code>{'{name}'}</code> for the two names. A handful is enough. The situations should be made up, since she is told not to treat them as things you said.{' '}
                      <button type="button" className="linkish" onClick={() => setWords({ ...words, persona: '', examples: '' })}>
                        Back to the default
                      </button>
                    </p>
                  </>
                )}
              </section>
            </div>

            {changed.length > 0 && (
              <div className="set-savebar">
                <span>Unsaved changes in {changed.map(([, label]) => label).join(', ')}</span>
                <div>
                  <button type="button" className="secondary" disabled={busy} onClick={undo}>
                    Undo
                  </button>
                  <button type="button" className="primary" disabled={busy} onClick={() => void save()}>
                    {busy ? 'Saving…' : 'Save'}
                  </button>
                </div>
              </div>
            )}
            {toast && changed.length === 0 && (
              <div className="set-toast" role="status">
                {toast}
              </div>
            )}
          </div>
        </div>
      )}
    </Screen>
  )
}
