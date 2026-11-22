import { useEffect, useRef, useState } from 'react'
import { api, apiHear, useSignals } from '../api'
import type { State, StepSignal, StreamEvent } from '../types'
import { type Ears, openEars } from './ears'
import type { Watching } from './sight'
import { sentences, Speaker } from './speech'
import { shownFor } from './feelings'
import { Stage } from './stage'

// The words of a line, lower case, without punctuation.
function wordsOf(line: string): string[] {
  return line.toLowerCase().split(/[^a-z0-9']+/).filter((w) => w.length > 1)
}

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

// Things a speech recogniser writes when it is handed a noise.
const notSpeech = ['you', 'thank you.', 'thanks for watching!', 'thank you for watching.', 'bye.', '.', 'okay.']

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
// Voice and listening go on whichever way she is shown; the camera needs
// her figure, so it goes off with it.
export function Avatar({ state, working, onSay, view, mindCount }: { state: State | null; working: boolean; onSay: (words: string, spoken: boolean) => void; view: HerView; mindCount: number }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const stage = useRef<Stage | null>(null)
  const speaker = useRef<Speaker | null>(null)
  const ears = useRef<Ears | null>(null)
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
  const named = useRef(byName)
  named.current = byName
  // Until when what is said is taken as meant for her without her name:
  // held open while she works on a reply, then open a little longer.
  const [talking, setTalking] = useState(false)
  const openUntil = useRef(0)
  const windowFor = useRef(25)
  // Set when you said goodbye: the window shuts after this reply.
  const closing = useRef(false)
  const inWindow = () => Date.now() < openUntil.current
  const holdOpen = () => {
    openUntil.current = Infinity
    setTalking(true)
  }
  // Her reply is over: the window runs from now, or shuts.
  const letRun = () => {
    if (openUntil.current !== Infinity) return
    if (closing.current) {
      closing.current = false
      openUntil.current = 0
      setTalking(false)
      return
    }
    openUntil.current = Date.now() + windowFor.current * 1000
  }
  useEffect(() => {
    if (!talking) return
    const id = window.setInterval(() => {
      if (!inWindow()) setTalking(false)
    }, 1000)
    return () => window.clearInterval(id)
  }, [talking])
  const [seeing, setSeeing] = useState(false)
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
  // Her own recent words, so that if the microphone picks her up from
  // the speakers, what comes back is known to be hers and is dropped.
  const hers = useRef<{ words: Set<string>; at: number }>({ words: new Set(), at: 0 })
  const noteHers = (line: string) => {
    const now = Date.now()
    if (now - hers.current.at > 30_000) hers.current.words = new Set()
    for (const w of wordsOf(line)) hers.current.words.add(w)
    hers.current.at = now
  }
  const isHers = (heard: string) => {
    if (Date.now() - hers.current.at > 30_000) return false
    const w = wordsOf(heard)
    if (w.length === 0) return false
    const shared = w.filter((x) => hers.current.words.has(x)).length
    // Nearly all of it is something she just said.
    return shared / w.length >= 0.7
  }

  if (!speaker.current) {
    const sp = new Speaker()
    sp.onMouth = (m) => stage.current?.setMouth(m)
    sp.onState = (on) => {
      setSpeaking(on)
      ears.current?.setHerSpeaking(on)
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
      ears.current?.stop()
      ears.current = null
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

  // The camera: on while she is shown and awake. What she sees goes to the
  // stage, which looks or copies; nothing is kept.
  useEffect(() => {
    if (!seeing || !shown || asleep || !video.current) return
    let gone = false
    let was: Watching = 'pointer'
    setProblem('')
    // Loaded only when first needed: the face finder is large.
    const el = video.current
    import('./sight')
      .then(({ openSight }) =>
        openSight(el, (seen) => {
          if (gone) return
          stage.current?.see(seen)
          if (seen.watching !== was) {
            was = seen.watching
            setWatching(was)
          }
        }),
      )
      .then((s) => {
        if (gone) s.stop()
        else sight.current = s
      })
      .catch(() => {
        if (gone) return
        setSeeing(false)
        setProblem('She cannot see you: the camera is not allowed. Allow it for Cosmos in System Settings, under Privacy and Security.')
      })
    return () => {
      gone = true
      sight.current?.stop()
      sight.current = null
      stage.current?.see(null)
      setWatching('pointer')
    }
  }, [seeing, shown, asleep])
  // With no figure, or asleep, the camera turns off rather than waiting.
  useEffect(() => {
    if (!shown || asleep) setSeeing(false)
  }, [shown, asleep])
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
        noteHers(done[i])
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
    for (const line of rest) {
      noteHers(line)
      sp.say(line)
    }
  })

  async function startListening() {
    setProblem('')
    try {
      ears.current = await openEars({
        // Something began: she goes quieter until it is clear it is you.
        start: () => {
          speaker.current?.duck(true)
          setEar('hearing')
        },
        // It is you: she stops, as a person would, and leaves the rest
        // of that reply unsaid. Listening for her name, she stops at once
        // only for someone she is talking with; anyone else she waits on
        // until it is clear they are talking to her.
        confirmed: () => {
          if (named.current && !inWindow()) return
          if (speaker.current?.speaking) {
            speaker.current.stop()
            hushed.current = true
          }
        },
        dropped: () => {
          speaker.current?.duck(false)
          setEar((cur) => (cur === 'off' ? 'off' : 'listening'))
        },
        heard: (wav) => {
          setEar('writing')
          apiHear(wav)
            .then((heard) => {
              const words = heard.text.trim()
              windowFor.current = heard.window
              const real = words.length > 1 && !notSpeech.includes(words.toLowerCase()) && !isHers(words)
              // Not for her: kept for a few minutes as what is going on
              // around her, in memory only, and she carries on.
              if (real && named.current && !heard.addressed && !inWindow()) {
                speaker.current?.duck(false)
                void api('/api/overheard', { method: 'POST', body: { text: words } }).catch(() => {})
                return
              }
              if (real) {
                holdOpen()
                closing.current = heard.ending
                // Said while she was still working on a reply: that reply
                // is dropped and she answers both together.
                if (busy.current) {
                  hushed.current = true
                  speaker.current?.stop()
                  void api('/api/interrupt', { method: 'POST' }).catch(() => {})
                } else if (speaker.current?.speaking) {
                  // Too short to stop her while it was said ("wait!"), but
                  // real words: she stops now.
                  hushed.current = true
                  speaker.current.stop()
                }
                speaker.current?.duck(false)
                say.current(words, true)
              } else {
                speaker.current?.duck(false)
              }
            })
            .catch((e: Error) => {
              speaker.current?.duck(false)
              setProblem(`She could not make that out: ${e.message}`)
            })
            .finally(() => setEar((cur) => (cur === 'off' ? 'off' : 'listening')))
        },
      })
    } catch {
      setProblem('She cannot hear you: the microphone is not allowed. Allow it for Cosmos in System Settings, under Privacy and Security.')
      return
    }
    setEar('listening')
    // Her ears and voice load now, not on the first thing said.
    void api('/api/hear/ready', { method: 'POST' }).catch(() => {})
  }

  function stopListening() {
    setEar('off')
    ears.current?.stop()
    ears.current = null
    openUntil.current = 0
    closing.current = false
    setTalking(false)
    // What she overheard goes with her ears.
    void api('/api/overheard', { method: 'DELETE' }).catch(() => {})
  }

  function toggleByName() {
    const next = !byName
    setByName(next)
    keep('cosmos:her-listen', next ? 'name' : 'all')
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
        <SenseIcon name="listen" off={!listening} />
        {listening && <i className="her-sense-live" aria-hidden="true" />}
      </button>
      {shown && (
        <button type="button" className={`her-sense ${seeing ? 'on' : ''}`} aria-pressed={seeing} aria-label={seeing ? 'Turn her camera off' : 'Let her see you'} title={`${seeing ? 'Turn her camera off' : 'Let her see you'} · right-click to ${preview ? 'hide' : 'show'} her view`} disabled={asleep} onClick={() => setSeeing(!seeing)} onContextMenu={(e) => { e.preventDefault(); togglePreview() }}>
          <SenseIcon name="see" off={!seeing} />
          {seeing && <i className="her-sense-live" aria-hidden="true" />}
        </button>
      )}
    </div>
  )
  const mood = state?.mood
  return (
    <div className={`avatar her-${view}`}>
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
