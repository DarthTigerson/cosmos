import { type FormEvent, type KeyboardEvent, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, useLoad, useSignals } from '../api'
import { Avatar } from '../avatar/Avatar'
import { clock, longDate, when } from '../format'
import { Meters, Problem } from '../parts'
import type { Run, State, Step, StepSignal, StreamEvent, Today } from '../types'

const tierLabel: Record<Step['tier'], string> = { main: 'main model', worker: 'worker', tool: 'tool', code: 'check' }

interface LiveStep {
  name: string
  tier: Step['tier']
  running: boolean
  ok: boolean
  ms: number
}

export function Nova({ state }: { state: State | null }) {
  const { data, error, reload } = useLoad<Today>('/api/today')
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  const [live, setLive] = useState<LiveStep[] | null>(null)
  // Her reply as she writes it, before the finished one arrives.
  const [writing, setWriting] = useState('')
  const [extra, setExtra] = useState<StreamEvent[]>([])
  const end = useRef<HTMLDivElement>(null)
  // The reply whose checks are open in the side panel, if any.
  const [inspect, setInspect] = useState<{ run: Run; at: string } | null>(null)
  const [inspectError, setInspectError] = useState('')

  async function showChecks(m: StreamEvent) {
    if (!m.meta?.run_id) return
    if (inspect?.run.id === m.meta.run_id) {
      setInspect(null)
      return
    }
    setInspectError('')
    try {
      setInspect({ run: await api<Run>(`/api/runs/${m.meta.run_id}`), at: m.at })
    } catch (e) {
      setInspectError((e as Error).message)
    }
  }
  const box = useRef<HTMLTextAreaElement>(null)

  // Grow the message box with its text, up to the CSS max-height.
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${el.scrollHeight + 2}px`
  }, [text])

  // Enter sends; Shift+Enter (or Enter while composing with an IME) adds a new line.
  function onKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      e.currentTarget.form?.requestSubmit()
    }
  }

  useSignals((s) => {
    if (s.kind === 'sessions') {
      // A session was started or reopened: show its thread.
      setExtra([])
      setInspect(null)
      reload()
    }
    if (s.kind === 'message') {
      // Shown straight away; the next reload replaces it with the stored copy.
      const msg = s.data as StreamEvent
      setExtra((list) => (list.some((m) => m.id === msg.id) ? list : [...list, msg]))
      if ((s.data as StreamEvent).kind === 'initiative') reload()
    }
    if (s.kind === 'writing') setWriting((s.data as { text: string }).text)
    if (s.kind === 'message' || (s.kind === 'step' && (s.data as StepSignal).state === 'running')) setWriting('')
    if (s.kind === 'step') {
      const step = s.data as StepSignal
      setLive((steps) => {
        const list = step.name === 'route' && step.state === 'running' ? [] : [...(steps ?? [])]
        if (step.state === 'running') {
          list.push({ name: step.name, tier: step.tier, running: true, ok: true, ms: 0 })
        } else {
          const i = list.findLastIndex((x) => x.name === step.name && x.running)
          if (i >= 0) list[i] = { ...list[i], running: false, ok: step.ok ?? true, ms: step.ms ?? 0 }
        }
        return list
      })
    }
  })

  const stored = data?.messages ?? []
  const seen = new Set(stored.map((m) => m.id))
  const here = data?.session?.id
  // Live messages for another session (one she opened herself) stay out.
  const messages = [...stored, ...extra.filter((m) => !seen.has(m.id) && (!here || !m.session_id || m.session_id === here))]
  const earlier = data?.earlier ?? []

  useEffect(() => {
    end.current?.scrollIntoView({ block: 'end' })
  }, [messages.length, sending, writing])

  async function send(e: FormEvent) {
    e.preventDefault()
    const typed = text.trim()
    if (!typed) return
    setText('')
    await say(typed)
  }

  // say sends words to her, typed or spoken.
  async function say(say: string) {
    if (!say || sending) return
    setSending(true)
    setSendError('')
    try {
      await api('/api/message', { method: 'POST', body: { text: say } })
    } catch (err) {
      setSendError((err as Error).message)
    } finally {
      setSending(false)
      setExtra([])
      reload()
    }
  }

  const name = state?.name ?? 'Nova'
  const current = live?.find((s) => s.running)
  const steps: LiveStep[] = sending && live ? live : (data?.last_run?.steps ?? []).map((s) => ({ ...s, running: false }))

  return (
    <div className="split">
      <main className="chat">
        <header className="screen-head">
          <div>
            <span className="eyebrow">
              {longDate(new Date())}
              {data?.session && messages.length > 0 && ` · started ${clock(data.session.started_at)}`}
            </span>
            {data?.session?.title ? <h1 className="session-name">{data.session.title}</h1> : <h1>{name}</h1>}
          </div>
        </header>
        <Problem message={error} />

        <div className="messages">
          {messages.length === 0 && !error && (
            <p className="fresh">
              A fresh session. She keeps everything she remembers.
              {earlier.length > 0 && ` Since she last slept you also talked about: ${earlier.map((e) => e.title).join('; ')}.`}
            </p>
          )}
          {messages.map((m) =>
            m.kind === 'user_msg' ? (
              <div className="bubble" key={m.id}>
                {m.text}
              </div>
            ) : m.kind === 'initiative' ? (
              <article className="reach" key={m.id}>
                <span className="who">
                  {name} reached out · {clock(m.at)}
                </span>
                <p>{m.text}</p>
                {m.meta?.reason && <p className="why">Why: {m.meta.reason}</p>}
              </article>
            ) : (
              <article className="reply" key={m.id}>
                <span className="who">
                  {name} · {clock(m.at)}
                  {m.meta?.run_id !== undefined && (
                    <button
                      type="button"
                      className={m.meta.verified ? 'mark ok' : 'mark failed'}
                      aria-pressed={inspect?.run.id === m.meta.run_id}
                      aria-label={m.meta.verified ? 'Passed her checks. Show how this reply was made.' : 'Did not pass her checks. Show what failed.'}
                      title={m.meta.verified ? 'Passed her checks' : 'Did not pass her checks'}
                      onClick={() => void showChecks(m)}
                    >
                      <svg aria-hidden="true" width="18" height="18" viewBox="0 0 16 16" fill="none">
                        <rect x="1" y="1" width="14" height="14" rx="4" className="tile" />
                        {m.meta.verified ? (
                          <path d="M4.8 8.3l2.2 2.2L11.2 6" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
                        ) : (
                          <path d="M5.6 5.6l4.8 4.8M10.4 5.6l-4.8 4.8" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
                        )}
                      </svg>
                    </button>
                  )}
                </span>
                {m.text.split('\n').filter(Boolean).map((line, i) => (
                  <p key={i}>{line}</p>
                ))}
              </article>
            ),
          )}
          {sending && writing && (
            <article className="reply" aria-live="off">
              <span className="who">{name} is writing</span>
              {writing.split('\n').filter(Boolean).map((line, i) => (
                <p key={i}>{line}</p>
              ))}
            </article>
          )}
          {sending && !writing && (
            <p className="thinking" role="status">
              {current ? `${name} is on: ${current.name}` : `${name} is thinking`}
            </p>
          )}
          <Problem message={sendError || inspectError} />
          <div ref={end} />
        </div>

        <form className="composer" onSubmit={send}>
          <label htmlFor="say" className="visually-hidden">
            Message {name}
          </label>
          <textarea
            id="say"
            ref={box}
            rows={1}
            autoComplete="off"
            autoFocus
            placeholder={`Say something to ${name}`}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKey}
          />
          <button type="submit" className="primary" disabled={sending || !text.trim()}>
            Send
          </button>
        </form>
      </main>

      {inspect ? (
        <aside className="rail" aria-label="How this reply was made">
          <section>
            <div className="inspect-head">
              <h2 className="label">Reply at {clock(inspect.at)}</h2>
              <button type="button" className="text" onClick={() => setInspect(null)}>
                Close
              </button>
            </div>
            <p className={inspect.run.verified ? 'verdict ok' : 'verdict failed'}>
              {inspect.run.verified
                ? inspect.run.rewrites > 0
                  ? 'Passed her checks after one rewrite.'
                  : 'Passed her checks.'
                : 'Sent without passing: the rewrite failed a check too.'}
            </p>
            <p className="quiet">You said: “{inspect.run.request}”</p>
          </section>
          <section>
            <h2 className="label">Step by step</h2>
            <ol className="steps detailed">
              {inspect.run.steps.map((st, i) => (
                <li key={i} className={st.ok ? '' : 'failed'}>
                  <div>
                    <span>{st.name}</span>
                    <span className={`tier ${st.tier}`}>
                      {tierLabel[st.tier]}
                      {st.ok ? '' : ' failed'}
                    </span>
                  </div>
                  {!st.ok && st.note && <p className="reason">{st.note}</p>}
                  {st.ok && st.tier === 'tool' && st.note && <p className="args">{st.note}</p>}
                </li>
              ))}
            </ol>
            <p className="quiet">
              {(inspect.run.ms / 1000).toFixed(1)} s · planned {inspect.run.mode === 'steps' ? 'step by step' : 'in one go'}
            </p>
          </section>
        </aside>
      ) : (
      <aside className="rail" aria-label={`${name}'s state`}>
        {state && (
          <section>
            <Avatar state={state} working={sending} onSay={(words) => void say(words)} />
            <Meters mood={state.mood} />
          </section>
        )}

        <section>
          <h2 className="label">{sending ? 'This reply, step by step' : 'Last reply, step by step'}</h2>
          {steps.length === 0 ? (
            <p className="quiet">No reply yet.</p>
          ) : (
            <ol className="steps">
              {steps.map((s, i) => (
                <li key={i} className={s.running ? 'running' : s.ok ? '' : 'failed'}>
                  <span>{s.name}</span>
                  <span className={`tier ${s.tier}`}>
                    {tierLabel[s.tier]}
                    {s.running ? ' …' : s.ok ? '' : ' failed'}
                  </span>
                </li>
              ))}
            </ol>
          )}
          {!sending && data?.last_run && (
            <p className="quiet">
              {(data.last_run.ms / 1000).toFixed(1)} s · planned {data.last_run.mode === 'steps' ? 'step by step' : 'in one go'}
              {data.last_run.rewrites > 0 && ' · rewritten once'}
            </p>
          )}
        </section>

        <section>
          <h2 className="label">On her mind</h2>
          {(data?.intentions.length ?? 0) === 0 ? (
            <p className="quiet">Nothing she is waiting to bring up.</p>
          ) : (
            <ul className="intentions">
              {data!.intentions.map((i) => (
                <li key={i.id}>
                  <span>{i.text}</span>
                  <span className="sub">{i.status === 'sent' ? 'asked, waiting for an answer' : `${when(i.trigger_at)} · lets go ${when(i.expires_at)}`}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </aside>
      )}
    </div>
  )
}
