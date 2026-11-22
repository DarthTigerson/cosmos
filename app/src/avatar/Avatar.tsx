import { useEffect, useRef, useState } from 'react'
import { api, useSignals } from '../api'
import type { State, StepSignal, StreamEvent } from '../types'
import { type Call, openCall } from './call'
import { HearingMark, type HearingTone } from './HearingMark'
import { watchFrom } from './glance'
import type { Watching } from './sight'
import { sentences, Speaker } from './speech'
import { shownFor } from './feelings'
import { Stage } from './stage'

type Ear = 'off' | 'listening' | 'hearing' | 'writing'

// A sentence with a number or an address in it is not said until the
// reply is finished: those are what her checks look at, and a rewrite
// can change them.
const risky = /\d|https?:\/\/|www\./i

// How many lines two drafts start with in common.
function shared(a: string[], b: string[]): number {
  let i = 0
  while (i < a.length && i < b.length && a[i] === b[i]) i++
  return i
}

// How she is shown beside the chat: her full figure, a small round one,
// none at all, or her figure filling the panel with the rest over it.
export type HerView = 'full' | 'compact' | 'text' | 'only'

// Remembered between visits, so her voice stays on once turned on.
const kept = (key: string) => {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
const keep = (key: string, value: string) => {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Not remembered; it still works for now.
  }
}

// Her figure beside the chat: the 3D model, alive between words. Three
// buttons under her, each on or off by itself:
//   - voice: she says her replies aloud as she writes them;
//   - listen: she hears you, knows when you have finished, and stops if
//     you cut in. There is nothing to hold down;
//   - see: the camera. She decides for herself what to do with it.
// All three go on whichever way she is shown. The camera stays on until
// it is turned off, except while she sleeps.
export function Avatar({ state, working, onSay, view, mindCount }: { state: State | null; working: boolean; onSay: (words: string, spoken: boolean, heard?: number) => void; view: HerView; mindCount: number }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const stage = useRef<Stage | null>(null)
  const speaker = useRef<Speaker | null>(null)
  // The call: while she listens, the microphone and her voice go over
  // it, and the core decides what was said to her.
  const call = useRef<Call | null>(null)
  const shown = view !== 'text'
  const [ready, setReady] = useState(false)
  const [problem, setProblem] = useState('')
  const [speaking, setSpeaking] = useState(false)
  const [voice, setVoice] = useState(() => kept('cosmos:her-voice') === 'on')
  const [ear, setEar] = useState<Ear>('off')
  const listening = ear !== 'off'
  // How she listens: to everything said, or only when her name is said
  // (and for a short while after she answers, without it). The second
  // keeps her out of conversations that are not with her.
  const [byName, setByName] = useState(() => kept('cosmos:her-listen') !== 'all')
  // Whether what is said is taken as meant for her without her name:
  // held open while she works on a reply, then open a little longer.
  // The core keeps the window; this only shows it.
  const [talking, setTalking] = useState(false)
  // How loud the room is, 0 to 1, for the mark in the listen button.
  const loudness = useRef(0)
  // Her reply is over: the window runs from now, or shuts.
  const letRun = () => call.current?.idle()
  // She may keep her camera on: it stays on whichever way she is shown,
  // goes off while she sleeps, and comes back when she wakes.
  const [wantSight, setWantSight] = useState(() => kept('cosmos:her-sight') === 'on')
  const setSight = (on: boolean) => {
    setWantSight(on)
    keep('cosmos:her-sight', on ? 'on' : 'off')
  }
  const [watching, setWatching] = useState<Watching>('pointer')
  // Whether the small picture of what she sees is shown; right-click the
  // camera button to switch it.
  const [preview, setPreview] = useState(() => kept('cosmos:her-sight-preview') !== 'off')
  function togglePreview() {
    const next = !preview
    setPreview(next)
    keep('cosmos:her-sight-preview', next ? 'on' : 'off')
  }
  const video = useRef<HTMLVideoElement>(null)
  const sight = useRef<{ stop: () => void } | null>(null)
  const aloud = useRef(voice)
  aloud.current = voice
  const busy = useRef(working)
  busy.current = working
  // Set when you cut in: the rest of the reply she was giving is not
  // said. The next reply she starts lifts it.
  const hushed = useRef(false)
  // Set when the reply is redone after she started saying it: the rest
  // waits for the finished reply, which is compared with what was said.
  const settling = useRef(false)
  const say = useRef(onSay)
  say.current = onSay
  // What of the reply being written has already been said aloud.
  const said = useRef<string[]>([])

  if (!speaker.current) {
    const sp = new Speaker()
    sp.onMouth = (m) => stage.current?.setMouth(m)
    sp.onState = (on) => {
      setSpeaking(on)
      call.current?.setHerSpeaking(on)
      if (!on && !busy.current) letRun()
    }
    sp.onError = setProblem
    speaker.current = sp
  }

  useEffect(() => {
    if (!shown || !canvas.current) return
    const s = new Stage(canvas.current)
    s.onReady = () => setReady(true)
    s.onError = setProblem
    stage.current = s
    void s.load('./nova.vrm')
    const move = (e: PointerEvent) => s.look((e.clientX / window.innerWidth) * 2 - 1, (e.clientY / window.innerHeight) * 2 - 1)
    window.addEventListener('pointermove', move)
    return () => {
      window.removeEventListener('pointermove', move)
      s.dispose()
      stage.current = null
      setReady(false)
    }
  }, [shown])

  // Leaving the screen stops her voice, her ears and her camera.
  useEffect(
    () => () => {
      speaker.current?.stop()
      call.current?.stop()
      call.current = null
      sight.current?.stop()
      sight.current = null
    },
    [],
  )

  const close = view === 'compact'
  useEffect(() => {
    stage.current?.setClose(close)
  }, [close, ready])

  const asleep = state?.state === 'asleep'
  const seeing = wantSight && !asleep

  // The camera: on while she is awake and allowed. What she sees goes to
  // the stage, which looks or copies, and a still is taken when she is
  // asked to look. A new way of showing her starts it afresh.
  useEffect(() => {
    if (!seeing || !video.current) return
    let gone = false
    let was: Watching = 'pointer'
    setProblem('')
    // Loaded only when first needed: the face finder is large.
    const el = video.current
    let remembering = false
    import('./sight')
      .then(({ openSight }) =>
        openSight(el, (seen) => {
          if (gone) return
          stage.current?.see(seen)
          if (seen.watching !== was) {
            was = seen.watching
            setWatching(was)
          }
        }, (face) => {
          // One face at a time to the core: she remembers it, and knows
          // it the next time.
          if (gone || remembering) return
          remembering = true
          api('/api/see', { method: 'POST', body: face })
            .catch(() => {})
            .finally(() => {
              remembering = false
            })
        }),
      )
      .then((s) => {
        if (gone) s.stop()
        else {
          sight.current = s
          watchFrom(el)
        }
      })
      .catch(() => {
        if (gone) return
        setSight(false)
        setProblem('She cannot see you: the camera is not allowed. Allow it for Cosmos in System Settings, under Privacy and Security.')
      })
    return () => {
      gone = true
      watchFrom(null)
      sight.current?.stop()
      sight.current = null
      stage.current?.see(null)
      setWatching('pointer')
    }
  }, [seeing, shown])
  const pose = asleep ? 'asleep' : speaking ? 'talking' : working || ear === 'writing' ? 'thinking' : 'idle'
  useEffect(() => {
    stage.current?.setPose(pose)
  }, [pose, ready])

  const feeling = state?.feeling ?? 'calm'
  const warmth = state?.mood.warmth ?? 0.6
  const energy = state?.mood.energy ?? 0.5
  const confidence = state?.mood.confidence ?? 0.6
  const since = state?.mood.feeling_at
  // A feeling shows plainly when new and settles over the next twenty
  // minutes, so her face is looked at again every half minute.
  const [, setNow] = useState(0)
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(id)
  }, [])
  useEffect(() => {
    const { look, manner } = shownFor(feeling, { warmth, energy, confidence }, since)
    stage.current?.setFace({ look, manner, energy })
  })

  // Her reply is in (or was cut off). Unless she is about to say it, the
  // window starts to run.
  useSignals((s) => {
    if (s.kind === 'message' && (s.data as StreamEvent).kind === 'user_msg') return
    if (s.kind !== 'message' && s.kind !== 'interrupted') return
    window.setTimeout(() => {
      // Still busy: more she was told is being answered next.
      if (!speaker.current?.speaking && !busy.current) letRun()
    }, 500)
  })

  // With her voice on she speaks as she writes. Each sentence is said as
  // soon as the next one has begun; the last waits for the finished
  // reply, since the end of a draft is the part most likely to change.
  useSignals((s) => {
    if (!aloud.current) return
    const sp = speaker.current!
    if (s.kind === 'step') {
      const step = s.data as StepSignal
      if (step.state !== 'running') return
      if (step.name === 'route') {
        said.current = []
        hushed.current = false
        settling.current = false
      }
      // A second draft: whatever of the first was still to come is
      // dropped, and she waits for the finished reply.
      if ((step.name === 'rewrite' || step.name === 'writer') && said.current.length > 0) {
        sp.stop()
        settling.current = true
      }
      return
    }
    if (s.kind === 'writing') {
      if (hushed.current || settling.current) return
      const done = sentences((s.data as { text: string }).text).slice(0, -1)
      for (let i = said.current.length; i < done.length; i++) {
        if (risky.test(done[i])) break
        said.current.push(done[i])
        sp.say(done[i])
      }
      return
    }
    if (s.kind !== 'message') return
    const m = s.data as StreamEvent
    if (m.kind !== 'nova_msg' && m.kind !== 'initiative') return
    const already = said.current
    said.current = []
    settling.current = false
    // The reply you cut off: it is in the chat, and not said.
    if (hushed.current) {
      hushed.current = false
      return
    }
    const final = sentences(m.text)
    // What she said while writing should be how the reply begins. Where
    // the finished reply parts from it, she picks up from there, so the
    // corrected part is heard and nothing is said twice.
    const from = shared(already, final)
    const rest = final.slice(from)
    if (from < already.length && rest.length > 0) {
      sp.stop()
      sp.say('Sorry, let me put that better.')
    }
    for (const line of rest) sp.say(line)
  })

  async function startListening() {
    setProblem('')
    const sp = speaker.current!
    const back = () => {
      sp.duck(false)
      setEar((cur) => (cur === 'off' ? 'off' : 'listening'))
    }
    try {
      call.current = await openCall(
        {
          // Something began: she goes quieter until it is clear it is you.
          start: () => {
            sp.duck(true)
            setEar('hearing')
          },
          dropped: back,
          // Someone she is talking with is speaking over her: she stops,
          // as a person would, and leaves the rest of that reply unsaid.
          cut: () => {
            if (!sp.speaking) return
            sp.stop()
            hushed.current = true
          },
          writing: () => setEar('writing'),
          ignored: back,
          say: (words, heard, cut) => {
            // Said while she was working on a reply (which the core has
            // dropped, to answer both together), or while she was still
            // speaking: what she was saying is left unsaid.
            if (cut || sp.speaking) {
              hushed.current = true
              sp.stop()
            }
            back()
            say.current(words, true, heard)
          },
          window: setTalking,
          problem: setProblem,
          level: (v) => {
            loudness.current = v
          },
          closed: () => {
            stopListening()
            setProblem('Her ears stopped: the core ended the call. Turn listening on again to carry on.')
          },
        },
        byName,
      )
    } catch {
      setProblem('She cannot hear you: the microphone is not allowed, or the core would not take the call. Allow the microphone for Cosmos in System Settings, under Privacy and Security.')
      return
    }
    const c = call.current
    sp.voiceFrom = (text) => c.speak(text)
    setEar('listening')
  }

  function stopListening() {
    setEar('off')
    call.current?.stop()
    call.current = null
    if (speaker.current) speaker.current.voiceFrom = null
    setTalking(false)
  }

  function toggleByName() {
    const next = !byName
    setByName(next)
    keep('cosmos:her-listen', next ? 'name' : 'all')
    call.current?.setByName(next)
  }

  function toggleVoice() {
    const next = !voice
    setVoice(next)
    keep('cosmos:her-voice', next ? 'on' : 'off')
    if (!next) {
      speaker.current?.stop()
      said.current = []
    }
  }

  const word = asleep
    ? 'asleep'
    : speaking
      ? 'talking'
      : ear === 'hearing'
        ? 'hearing you'
        : pose === 'thinking'
          ? 'thinking'
          : watching === 'copy'
            ? 'copying you'
            : watching === 'motion'
              ? 'watching'
              : listening
                ? !byName
                  ? 'listening'
                  : talking
                    ? 'with you'
                    : `listening for ${state?.name ?? 'her name'}`
                : 'here'
  // Whose voice the mark takes it to be. Listening for her name, a voice
  // is grey until it is clear it is for her.
  const hearingTone: HearingTone = ear === 'writing' ? 'writing' : ear === 'hearing' ? (byName && !talking ? 'other' : 'you') : 'rest'
  const lit = word === 'talking' || word === 'hearing you' || word === 'thinking'
  const status = (
    <span className={`avatar-state ${lit ? 'lit' : ''} ${asleep ? 'asleep' : ''}`} role="status">
      <i aria-hidden="true" />
      {word}
    </span>
  )
  const actions = (
    <div className="her-senses">
      <button type="button" className={`her-sense ${voice ? 'on' : ''}`} aria-pressed={voice} aria-label={voice ? 'Stop her speaking aloud' : 'Hear her replies aloud'} title={voice ? 'Stop her speaking aloud' : 'Hear her replies aloud'} onClick={toggleVoice}>
        <SenseIcon name="voice" off={!voice} />
      </button>
      <button type="button" className={`her-sense ${listening ? 'on' : ''}`} aria-pressed={listening} aria-label={listening ? 'Stop her listening' : 'Talk to her'} title={listening ? 'Stop her listening' : 'Talk to her'} disabled={asleep && !listening} onClick={() => (listening ? stopListening() : void startListening())}>
        {listening ? <HearingMark level={loudness} tone={hearingTone} /> : <SenseIcon name="listen" off />}
      </button>
      <button type="button" className={`her-sense ${seeing ? 'on' : ''}`} aria-pressed={seeing} aria-label={seeing ? 'Turn her camera off' : 'Let her see you'} title={`${seeing ? 'Turn her camera off' : 'Let her see you'} · right-click to ${preview ? 'hide' : 'show'} her view`} disabled={asleep} onClick={() => setSight(!wantSight)} onContextMenu={(e) => { e.preventDefault(); togglePreview() }}>
        <SenseIcon name="see" off={!seeing} />
        {seeing && <i className="her-sense-live" aria-hidden="true" />}
      </button>
    </div>
  )
  const mood = state?.mood
  return (
    <div className={`avatar her-${view}`}>
      {/* With no figure the camera still runs; its picture is not shown. */}
      {!shown && seeing && <video ref={video} hidden aria-hidden="true" />}
      {shown && (
        <div className="avatar-stage">
          <canvas ref={canvas} role="img" aria-label={`${state?.name ?? 'She'}, ${word}`} />
          {!ready && !problem && <span className="avatar-wait">She is arriving…</span>}
          {/* What she sees, small, mirrored as a mirror would show you. */}
          <div className="her-sight" hidden={!seeing || !preview}>
            <video ref={video} aria-label="What her camera sees" />
            <span>
              <i aria-hidden="true" />
              {watching === 'copy' ? 'copying you' : watching === 'motion' ? 'watching' : 'her view'}
            </span>
          </div>
          {view === 'only' && (
            <div className="her-overlay">
              <div className="her-glass">
                {status}
                <span className="her-feel">{feeling}</span>
              </div>
              {mood && (
                <div className="her-glass">
                  <span className="her-minis">
                    {(
                      [
                        ['Warmth', mood.warmth],
                        ['Energy', mood.energy],
                        ['Confidence', mood.confidence],
                      ] as const
                    ).map(([label, v]) => (
                      <span key={label} title={`${label} ${v.toFixed(2)}`}>
                        {label[0]}
                        <i role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(v * 100)}>
                          <b style={{ width: `${Math.round(v * 100)}%` }} />
                        </i>
                      </span>
                    ))}
                  </span>
                  {mindCount > 0 && <span className="sub">{mindCount} on her mind</span>}
                </div>
              )}
              {actions}
            </div>
          )}
        </div>
      )}
      {view === 'compact' && (
        <div className="her-who">
          <b>{state?.name ?? 'She'}</b>
          <div className="avatar-row">
            {status}
            <span className="her-feel">feeling {feeling}</span>
          </div>
        </div>
      )}
      {view === 'text' && (
        <div className="her-mood">
          {status}
          <b>Feeling {feeling}</b>
        </div>
      )}
      {view === 'full' && (
        <div className="avatar-row">
          {status}
          <span className="her-feel">feeling {feeling}</span>
        </div>
      )}
      {problem && (
        <p className="problem" role="alert">
          {problem}
        </p>
      )}
      {view !== 'only' && actions}
      {listening && view !== 'only' && (
        <p className="quiet">
          {byName
            ? `She answers when you say “${state?.name ?? 'her name'}”, and for a little while after without it. Other talk nearby is left alone. `
            : 'Just talk. She answers when you pause, and stops if you speak over her. '}
          <button type="button" className="text" onClick={toggleByName}>
            {byName ? 'Answer everything' : 'Only when named'}
          </button>
        </p>
      )}
    </div>
  )
}

// The three buttons' icons: crossed out while off.
function SenseIcon({ name, off }: { name: 'voice' | 'listen' | 'see'; off: boolean }) {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {name === 'voice' && (
        <>
          <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
          {off ? <path d="M16 9.5l5 5M21 9.5l-5 5" /> : <path d="M15.5 9a4 4 0 0 1 0 6M18 6.5a7.5 7.5 0 0 1 0 11" />}
        </>
      )}
      {name === 'listen' && (
        <>
          <rect x="9" y="3" width="6" height="11" rx="3" />
          <path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21" />
          {off && <path d="M4 4l16 16" />}
        </>
      )}
      {name === 'see' && (
        <>
          <path d="M3 7.5A1.5 1.5 0 0 1 4.5 6H14a1.5 1.5 0 0 1 1.5 1.5v9A1.5 1.5 0 0 1 14 18H4.5A1.5 1.5 0 0 1 3 16.5z" />
          <path d="M15.5 10.5 21 7.5v9l-5.5-3" />
          {off && <path d="M3 3l18 18" />}
        </>
      )}
    </svg>
  )
}
