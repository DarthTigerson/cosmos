import { type ClipboardEvent, type DragEvent, type FormEvent, type KeyboardEvent, type ReactNode, type PointerEvent as ReactPointerEvent, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, useLoad, useSignals } from '../api'
import { Avatar, type HerView } from '../avatar/Avatar'
import { asksToLook, glance } from '../avatar/glance'
import { FaceAsk, FacePicture } from '../faces'
import { clock, longDate, when } from '../format'
import { Linked, Meters, Problem } from '../parts'
import type { Run, State, Step, StepSignal, StreamEvent, Today } from '../types'

// At most this many pictures go with one message, each no bigger than this
// on its long side: enough for her to read, small enough to send.
const maxPictures = 4
const pictureSide = 1280

// shrink turns a picture file into a JPEG data: address, as glance does for
// the camera, or null when it cannot be read as a picture.
async function shrink(file: File): Promise<string | null> {
  try {
    const bitmap = await createImageBitmap(file)
    const scale = Math.min(1, pictureSide / Math.max(bitmap.width, bitmap.height))
    const c = document.createElement('canvas')
    c.width = Math.max(1, Math.round(bitmap.width * scale))
    c.height = Math.max(1, Math.round(bitmap.height * scale))
    c.getContext('2d')!.drawImage(bitmap, 0, 0, c.width, c.height)
    bitmap.close()
    return c.toDataURL('image/jpeg', 0.85)
  } catch {
    return null
  }
}

const tierLabel: Record<Step['tier'], string> = { main: 'main model', worker: 'worker', tool: 'tool', code: 'check', voice: 'voice' }

interface LiveStep {
  name: string
  tier: Step['tier']
  running: boolean
  ok: boolean
  ms: number
}

// The side panel's choices, kept between launches.
function kept(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
function keep(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Not kept; it still applies for now.
  }
}

const views: { view: HerView; label: string; icon: string }[] = [
  { view: 'full', label: 'Full', icon: 'M6 3h12a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM12 6.4a2.6 2.6 0 1 1 0 5.2 2.6 2.6 0 0 1 0-5.2zM8 16.5c.8-2 2.2-3 4-3s3.2 1 4 3' },
  { view: 'compact', label: 'Compact', icon: 'M7.5 4.5a3 3 0 1 1 0 6 3 3 0 0 1 0-6zM13 6h7M13 9h5M4 14h16M4 18h12' },
  { view: 'text', label: 'Text', icon: 'M4 6h16M4 10h16M4 14h12M4 18h9' },
  { view: 'only', label: 'Only her', icon: 'M12 4.9a3.6 3.6 0 1 1 0 7.2 3.6 3.6 0 0 1 0-7.2zM5 21c1-4.2 3.6-6.5 7-6.5s6 2.3 7 6.5' },
]

function firstView(): HerView {
  const v = kept('cosmos:her-view')
  if (v && views.some((x) => x.view === v)) return v as HerView
  // Before the views, hiding her was a yes or no.
  return kept('cosmos:avatar') === 'false' ? 'text' : 'full'
}

const RAIL_MIN = 280
const RAIL_MAX = 560
const RAIL_USUAL = 356
// The chat keeps at least this much room when the panel is widened.
const CHAT_MIN = 420

function firstWidth(): number {
  const w = Number(kept('cosmos:rail-width'))
  return w >= RAIL_MIN && w <= RAIL_MAX ? w : RAIL_USUAL
}

// A section of the side panel that folds. Folded, it still says in one
// line what is in it.
function Fold({ id, label, summary, open, children }: { id: string; label: string; summary: string; open?: boolean; children: ReactNode }) {
  const [isOpen, setOpen] = useState(() => (kept(`cosmos:fold:${id}`) ?? String(open ?? true)) === 'true')
  const toggle = () => {
    keep(`cosmos:fold:${id}`, String(!isOpen))
    setOpen(!isOpen)
  }
  return (
    <section className={isOpen ? 'her-fold open' : 'her-fold'}>
      <button type="button" className="her-fold-head" aria-expanded={isOpen} onClick={toggle}>
        <span className="label">{label}</span>
        <span className="her-fold-sum">{summary}</span>
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M6 3l5 5-5 5" />
        </svg>
      </button>
      {isOpen && <div className="her-fold-body">{children}</div>}
    </section>
  )
}

// The line between the chat and the side panel. Dragging it, or the arrow
// keys while it has focus, sets the panel's width; a double click resets it.
function RailGrip({ width, setWidth }: { width: number; setWidth: (w: number) => void }) {
  const fit = (w: number) => Math.round(Math.max(RAIL_MIN, Math.min(RAIL_MAX, w, window.innerWidth - CHAT_MIN)))
  const [dragging, setDragging] = useState(false)
  function down(e: ReactPointerEvent<HTMLDivElement>) {
    e.preventDefault()
    const grip = e.currentTarget
    grip.setPointerCapture(e.pointerId)
    const startX = e.clientX
    const start = width
    setDragging(true)
    document.body.classList.add('her-resizing')
    const move = (ev: PointerEvent) => setWidth(fit(start + startX - ev.clientX))
    const up = () => {
      setDragging(false)
      document.body.classList.remove('her-resizing')
      grip.removeEventListener('pointermove', move)
      grip.removeEventListener('pointerup', up)
      grip.removeEventListener('pointercancel', up)
    }
    grip.addEventListener('pointermove', move)
    grip.addEventListener('pointerup', up)
    grip.addEventListener('pointercancel', up)
  }
  return (
    <div
      className={dragging ? 'her-grip dragging' : 'her-grip'}
      role="separator"
      aria-orientation="vertical"
      aria-label="Side panel width"
      aria-valuemin={RAIL_MIN}
      aria-valuemax={RAIL_MAX}
      aria-valuenow={width}
      tabIndex={0}
      title="Drag to resize. Double-click to reset."
      onPointerDown={down}
      onDoubleClick={() => setWidth(RAIL_USUAL)}
      onKeyDown={(e) => {
        if (e.key === 'ArrowLeft') setWidth(fit(width + 20))
        else if (e.key === 'ArrowRight') setWidth(fit(width - 20))
        else return
        e.preventDefault()
      }}
    >
      <span />
    </div>
  )
}

export function Nova({ state }: { state: State | null }) {
  const { data, error, reload } = useLoad<Today>('/api/today')
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const busy = useRef(false)
  // Words said or typed while she was busy. Nothing is dropped: they go
  // to her together as soon as she is free.
  const [waiting, setWaiting] = useState<string[]>([])
  const queued = useRef<string[]>([])
  const queuedAloud = useRef(false)
  const queuedHeard = useRef(0)
  const queuedPictures = useRef<string[]>([])
  // Pictures chosen for the next message, as data: addresses.
  const [pictures, setPictures] = useState<string[]>([])
  const picker = useRef<HTMLInputElement>(null)
  const [sendError, setSendError] = useState('')
  const [live, setLive] = useState<LiveStep[] | null>(null)
  // Her reply as she writes it, before the finished one arrives.
  const [writing, setWriting] = useState('')
  const [extra, setExtra] = useState<StreamEvent[]>([])
  const end = useRef<HTMLDivElement>(null)
  // The reply whose checks are open in the side panel, if any.
  const [inspect, setInspect] = useState<{ run: Run; at: string } | null>(null)
  const [inspectError, setInspectError] = useState('')
  const [view, setViewState] = useState<HerView>(firstView)
  const [details, setDetails] = useState(false)
  const [width, setWidthState] = useState(firstWidth)
  const setView = (v: HerView) => {
    keep('cosmos:her-view', v)
    setViewState(v)
    setDetails(false)
  }
  const setWidth = (w: number) => {
    keep('cosmos:rail-width', String(w))
    setWidthState(w)
  }

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
    if (s.kind === 'interrupted') setWriting('')
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
  }, [messages.length, sending, writing, waiting.length])

  async function send(e: FormEvent) {
    e.preventDefault()
    const typed = text.trim()
    if (!typed && pictures.length === 0) return
    const attached = pictures
    setText('')
    setPictures([])
    await say(typed || '(picture)', false, 0, attached)
  }

  // attach adds picture files to the next message, up to the most one
  // message can carry.
  async function attach(files: File[]) {
    const shots = files.filter((f) => f.type.startsWith('image/'))
    if (shots.length === 0) return
    const read = (await Promise.all(shots.map(shrink))).filter((p): p is string => !!p)
    setPictures((old) => [...old, ...read].slice(0, maxPictures))
  }

  function onPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const files = Array.from(e.clipboardData.items)
      .filter((i) => i.kind === 'file' && i.type.startsWith('image/'))
      .map((i) => i.getAsFile())
      .filter((f): f is File => !!f)
    if (files.length === 0) return
    // Words pasted alongside still paste as words; only the pictures are taken.
    e.preventDefault()
    void attach(files)
  }

  function onDrop(e: DragEvent<HTMLFormElement>) {
    if (!Array.from(e.dataTransfer.types).includes('Files')) return
    e.preventDefault()
    void attach(Array.from(e.dataTransfer.files))
  }

  // say sends words to her, typed or spoken. While she is busy they
  // wait, and everything that waited goes as one message.
  async function say(say: string, spoken = false, heard = 0, shots: string[] = []) {
    if (!say) return
    if (busy.current) {
      queued.current = [...queued.current, say]
      queuedAloud.current ||= spoken
      if (heard) queuedHeard.current = heard
      queuedPictures.current = [...queuedPictures.current, ...shots].slice(0, maxPictures)
      setWaiting(queued.current)
      return
    }
    busy.current = true
    setSending(true)
    setSendError('')
    let next = say
    let aloud = spoken
    // The number the call gave what was said, so its timings go with
    // the reply.
    let from = heard
    let images = shots
    while (next) {
      // Asked to look, she takes one still from her camera, if it is on.
      const seen = asksToLook(next) ? glance() : null
      try {
        await api('/api/message', { method: 'POST', body: { text: next, spoken: aloud, ...(seen ? { seen } : {}), ...(images.length ? { images } : {}), ...(from ? { heard: from } : {}) } })
      } catch (err) {
        setSendError((err as Error).message)
      }
      setExtra([])
      reload()
      next = queued.current.join(' ')
      aloud = queuedAloud.current
      from = queuedHeard.current
      images = queuedPictures.current
      queuedPictures.current = []
      queued.current = []
      queuedAloud.current = false
      queuedHeard.current = 0
      setWaiting([])
    }
    busy.current = false
    setSending(false)
  }

  const name = state?.name ?? 'Companion'
  const current = live?.find((s) => s.running)
  const steps: LiveStep[] = sending && live ? live : (data?.last_run?.steps ?? []).map((s) => ({ ...s, running: false }))

  const mind = data?.intentions ?? []
  const sidePanelSections = (
    <>
      <Fold
        id="steps"
        label={sending ? 'This reply' : 'Last reply'}
        open={false}
        summary={sending ? 'working…' : steps.length === 0 ? 'none yet' : `${steps.length} steps${data?.last_run ? ` · ${(data.last_run.ms / 1000).toFixed(1)} s` : ''}`}
      >
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
      </Fold>
      <Fold id="mind" label="On her mind" summary={mind.length === 0 ? 'nothing' : `${mind.length} waiting`}>
        {mind.length === 0 ? (
          <p className="quiet">Nothing she is waiting to bring up.</p>
        ) : (
          <ul className="intentions">
            {mind.map((i) => (
              <li key={i.id}>
                <span>{i.text}</span>
                <span className="sub">{i.status === 'sent' ? 'asked, waiting for an answer' : `${when(i.trigger_at)} · lets go ${when(i.expires_at)}`}</span>
              </li>
            ))}
          </ul>
        )}
      </Fold>
    </>
  )

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
                {m.meta?.image_ids && m.meta.image_ids.length > 0 && (
                  <div className="bubble-pics">
                    {m.meta.image_ids.map((id) => (
                      <FacePicture key={id} id={id} size={96} alt="A picture you sent" />
                    ))}
                  </div>
                )}
                <Linked text={m.text} />
              </div>
            ) : m.kind === 'initiative' ? (
              <article className="reach" key={m.id}>
                <span className="who">
                  {name} reached out · {clock(m.at)}
                </span>
                <p>
                  <Linked text={m.text} />
                </p>
                {m.meta?.image_id && m.meta.subject ? (
                  <FaceAsk subject={m.meta.subject} image={m.meta.image_id} user={state?.user ?? 'you'} />
                ) : (
                  m.meta?.reason && <p className="why">Why: {m.meta.reason}</p>
                )}
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
                  <p key={i}>
                    <Linked text={line} />
                  </p>
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
          {waiting.length > 0 && (
            <div className="bubble waiting" role="status" title="Goes to her as soon as she is free">
              {waiting.join(' ')}
            </div>
          )}
          <Problem message={sendError || inspectError} />
          <div ref={end} />
        </div>

        <form className="composer" onSubmit={send} onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
          {pictures.length > 0 && (
            <ul className="attached" aria-label="Pictures to send">
              {pictures.map((p, i) => (
                <li key={i}>
                  <img src={p} alt={`Picture ${i + 1} to send`} />
                  <button type="button" aria-label={`Remove picture ${i + 1}`} onClick={() => setPictures(pictures.filter((_, j) => j !== i))}>
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}
          <input
            ref={picker}
            type="file"
            accept="image/*"
            multiple
            hidden
            onChange={(e) => {
              void attach(Array.from(e.target.files ?? []))
              e.target.value = ''
            }}
          />
          <button type="button" className="attach" title="Attach a picture" aria-label="Attach a picture" disabled={pictures.length >= maxPictures} onClick={() => picker.current?.click()}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M21 11.5l-8.6 8.6a5.5 5.5 0 01-7.8-7.8l9-9a3.7 3.7 0 015.2 5.2l-9 9a1.8 1.8 0 01-2.6-2.6l8.3-8.3" />
            </svg>
          </button>
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
            onPaste={onPaste}
          />
          <button type="submit" className="primary" disabled={!text.trim() && pictures.length === 0}>
            Send
          </button>
        </form>
      </main>

      <RailGrip width={width} setWidth={setWidth} />
      {inspect ? (
        <aside className="rail" style={{ flexBasis: width }} aria-label="How this reply was made">
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
      <aside className={`rail her-rail her-rail-${view}`} style={{ flexBasis: width }} aria-label={`${name}'s state`}>
        <div className="her-bar">
          <span className="label">{name}</span>
          <div className="her-views" role="group" aria-label={`How to show ${name}`}>
            {views.map((v) => (
              <button key={v.view} type="button" aria-pressed={view === v.view} title={v.label} aria-label={v.label} onClick={() => setView(v.view)}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d={v.icon} />
                </svg>
              </button>
            ))}
          </div>
        </div>
        {state && <Avatar state={state} working={sending} onSay={(words, spoken, heard) => void say(words, spoken, heard)} view={view} mindCount={data?.intentions.length ?? 0} />}
        {view === 'only' ? (
          <>
            <button type="button" className="her-details-open" aria-expanded={details} onClick={() => setDetails(!details)}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
                <path d="M4 7h16M4 12h16M4 17h10" />
              </svg>
              Details
            </button>
            {details && (
              <div className="her-drawer">
                <div className="her-drawer-top">
                  <span className="label">Details</span>
                  <button type="button" className="linkish" onClick={() => setDetails(false)}>
                    Close
                  </button>
                </div>
                {state && <Meters mood={state.mood} />}
                {sidePanelSections}
              </div>
            )}
          </>
        ) : (
          <>
            {state && <Meters mood={state.mood} row={view === 'compact'} />}
            {sidePanelSections}
          </>
        )}
      </aside>
      )}
    </div>
  )
}
