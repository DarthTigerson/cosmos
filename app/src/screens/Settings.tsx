import { type FormEvent, useEffect, useState } from 'react'
import { api, useLoad } from '../api'
import { Problem, Screen } from '../parts'
import type { Config, Voice } from '../types'
import { Connections } from './Connections'
import { VoicePicker } from './VoicePicker'

function Field({ id, label, children }: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children}
    </div>
  )
}

// Her voice: who she is, and a few exchanges that show how she talks.
// Example dialogue shapes her tone more than any rule, so this is the
// place to make her sound the way you want.
function VoiceForm() {
  const { data, error, reload } = useLoad<Voice>('/api/voice')
  const [persona, setPersona] = useState('')
  const [examples, setExamples] = useState('')
  const [problem, setProblem] = useState('')
  const [saved, setSaved] = useState(false)
  useEffect(() => {
    if (data) {
      setPersona(data.persona)
      setExamples(data.examples)
    }
  }, [data])

  async function save(next: { persona: string; examples: string }) {
    setProblem('')
    try {
      await api('/api/voice', { method: 'PUT', body: next })
      setSaved(true)
      reload()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  return (
    <form
      className="settings"
      onSubmit={(e) => {
        e.preventDefault()
        void save({ persona, examples })
      }}
    >
      <fieldset>
        <legend>Her voice</legend>
        <Problem message={error || problem} />
        <div className="field">
          <label htmlFor="persona">Who she is</label>
          <textarea id="persona" rows={5} value={persona} onChange={(e) => { setPersona(e.target.value); setSaved(false) }} />
        </div>
        <div className="field">
          <label htmlFor="examples">How she talks, by example</label>
          <textarea id="examples" rows={14} value={examples} onChange={(e) => { setExamples(e.target.value); setSaved(false) }} />
        </div>
        <p className="note">
          Write short exchanges the way you want her to sound: when you tease her, when you tell her something that matters, when you ask a plain question.
          Use <code>{'{user}'}</code> and <code>{'{name}'}</code> for the two names. A handful is enough. The situations should be made up, since she is told not to treat them as things you said.
        </p>
        <div className="actions">
          <button type="submit" className="primary">Save her voice</button>
          <button type="button" className="secondary" onClick={() => void save({ persona: '', examples: '' })}>Back to the default</button>
          {saved && <span className="muted" role="status">Saved. It applies from her next reply.</span>}
        </div>
      </fieldset>
    </form>
  )
}

export function Settings({ onSaved }: { onSaved: () => void }) {
  const { data, error } = useLoad<Config>('/api/config')
  const [cfg, setCfg] = useState<Config | null>(null)
  const [problem, setProblem] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (data) setCfg(data)
  }, [data])

  if (!cfg) {
    return (
      <Screen eyebrow="How she is set up" title="Settings">
        <Problem message={error} />
      </Screen>
    )
  }

  const set = (patch: Partial<Config>) => {
    setCfg({ ...cfg, ...patch })
    setSaved(false)
  }
  const num = (v: string) => Number.parseInt(v, 10) || 0

  async function save(e: FormEvent) {
    e.preventDefault()
    setProblem('')
    try {
      setCfg(await api<Config>('/api/config', { method: 'PUT', body: cfg }))
      setSaved(true)
      onSaved()
    } catch (err) {
      setProblem((err as Error).message)
    }
  }

  return (
    <Screen eyebrow="How she is set up" title="Settings">
      <Problem message={error || problem} />
      <form className="settings" onSubmit={save}>
        <fieldset>
          <legend>You and her</legend>
          <div className="row">
            <Field id="user" label="Your name">
              <input id="user" value={cfg.user_name} onChange={(e) => set({ user_name: e.target.value })} />
            </Field>
            <Field id="name" label="Her name">
              <input id="name" value={cfg.companion_name} onChange={(e) => set({ companion_name: e.target.value })} />
            </Field>
            <Field id="location" label="Home location (for weather)">
              <input id="location" placeholder="City or town" value={cfg.location} onChange={(e) => set({ location: e.target.value })} />
            </Field>
          </div>
        </fieldset>

        <fieldset>
          <legend>How she thinks</legend>
          <div className="row">
            <Field id="planning" label="Planning">
              <select id="planning" value={cfg.planning} onChange={(e) => set({ planning: e.target.value as Config['planning'] })}>
                <option value="auto">Let her choose and learn</option>
                <option value="once">Always plan in one go</option>
                <option value="steps">Always one step at a time</option>
              </select>
            </Field>
            <div className="field check">
              <input id="verify" type="checkbox" checked={cfg.verify_with_model} onChange={(e) => set({ verify_with_model: e.target.checked })} />
              <label htmlFor="verify">Main model checks replies that used tools</label>
            </div>
            <div className="field check">
              <input id="claims" type="checkbox" checked={cfg.verify_claims} onChange={(e) => set({ verify_claims: e.target.checked })} />
              <label htmlFor="claims">Check replies for invented facts about you</label>
            </div>
            <div className="field check">
              <input id="watch" type="checkbox" checked={cfg.weather_watch} onChange={(e) => set({ weather_watch: e.target.checked })} />
              <label htmlFor="watch">Watch the weather at home</label>
            </div>
            <div className="field check">
              <input id="web" type="checkbox" checked={cfg.web_search} onChange={(e) => set({ web_search: e.target.checked })} />
              <label htmlFor="web">Let her look things up online (searches go to DuckDuckGo)</label>
            </div>
            <div className="field check">
              <input id="pods" type="checkbox" checked={cfg.podcasts} onChange={(e) => set({ podcasts: e.target.checked })} />
              <label htmlFor="pods">Let her listen to new podcast episodes on her own</label>
            </div>
          </div>
        </fieldset>

        <fieldset>
          <legend>When she may speak first</legend>
          <div className="row">
            <Field id="quiet-from" label="Quiet from (hour)">
              <input id="quiet-from" type="number" min={0} max={23} value={cfg.quiet_from} onChange={(e) => set({ quiet_from: num(e.target.value) })} />
            </Field>
            <Field id="quiet-to" label="Quiet until (hour)">
              <input id="quiet-to" type="number" min={0} max={23} value={cfg.quiet_to} onChange={(e) => set({ quiet_to: num(e.target.value) })} />
            </Field>
            <Field id="budget" label="At most per day">
              <input id="budget" type="number" min={0} value={cfg.initiative_budget} onChange={(e) => set({ initiative_budget: num(e.target.value) })} />
            </Field>
            <Field id="cooldown" label="Minutes between">
              <input id="cooldown" type="number" min={0} value={cfg.cooldown_minutes} onChange={(e) => set({ cooldown_minutes: num(e.target.value) })} />
            </Field>
            <Field id="brief" label="Morning note (hour)">
              <input id="brief" type="number" min={0} max={23} value={cfg.brief_hour} onChange={(e) => set({ brief_hour: num(e.target.value) })} />
            </Field>
            <Field id="sleep" label="Sleep (hour)">
              <input id="sleep" type="number" min={0} max={23} value={cfg.sleep_hour} onChange={(e) => set({ sleep_hour: num(e.target.value) })} />
            </Field>
          </div>
          <p className="note">Reminders you ask for always arrive on time, whatever these say.</p>
        </fieldset>
        <p className="note">
          The models she runs on are set up on the <a href="#models">Models</a> screen.
        </p>

        <div className="actions">
          <button type="submit" className="primary">
            Save
          </button>
          {saved && <span className="muted" role="status">Saved.</span>}
        </div>
      </form>
      <VoicePicker />
      <Connections />
      <VoiceForm />
    </Screen>
  )
}
