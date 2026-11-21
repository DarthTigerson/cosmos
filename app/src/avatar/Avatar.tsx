import { useEffect, useRef, useState } from 'react'
import { apiHear, useSignals } from '../api'
import type { State, StepSignal, StreamEvent } from '../types'
import { type Ears, openEars } from './ears'
import type { Watching } from './sight'
import { sentences, Speaker } from './speech'
import { type Face, Stage } from './stage'

// Which of the model's faces goes with each feeling, and how strongly.
const faces: Record<string, [Face['expression'], number]> = {
  glad: ['happy', 0.6], warmth: ['happy', 0.5], tenderness: ['relaxed', 0.7], amusement: ['happy', 0.8],
  pride: ['happy', 0.65], hope: ['happy', 0.4], curiosity: ['surprised', 0.3],
  concern: ['sad', 0.3], worry: ['sad', 0.4], sadness: ['sad', 0.7], hurt: ['sad', 0.6], unease: ['sad', 0.25],
  anger: ['angry', 0.55],
}

// The words of a line, lower case, without punctuation.
function wordsOf(line: string): string[] {
  return line.toLowerCase().split(/[^a-z0-9']+/).filter((w) => w.length > 1)
}

type Ear = 'off' | 'listening' | 'hearing' | 'writing'

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
export function Avatar({ state, working, onSay, view, mindCount }: { state: State | null; working: boolean; onSay: (words: string) => void; view: HerView; mindCount: number }) {
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
  const [seeing, setSeeing] = useState(false)
  const [watching, setWatching] = useState<Watching>('pointer')
  const video = useRef<HTMLVideoElement>(null)
  const sight = useRef<{ stop: () => void } | null>(null)
  const aloud = useRef(voice)
  aloud.current = voice
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
  useEffect(() => {
    const [expression, intensity] = faces[feeling] ?? ['neutral', 0]
    stage.current?.setFace({ expression, intensity, warmth, energy })
  }, [feeling, warmth, energy, ready])

  // With her voice on she speaks as she writes. Each sentence is said as soon as
  // the next one has begun; the last waits for the finished reply, since
  // the end of a draft is the part most likely to change.
  useSignals((s) => {
    if (!aloud.current) return
    const sp = speaker.current!
    if (s.kind === 'step') {
      const step = s.data as StepSignal
      if (step.name === 'route' && step.state === 'running') said.current = []
      return
    }
    if (s.kind === 'writing') {
      const done = sentences((s.data as { text: string }).text).slice(0, -1)
      for (let i = said.current.length; i < done.length; i++) {
        said.current.push(done[i])
        noteHers(done[i])
        sp.say(done[i])
      }
      return
    }
    if (s.kind !== 'message') return
    const m = s.data as StreamEvent
    if (m.kind !== 'nova_msg' && m.kind !== 'initiative') return
    const final = sentences(m.text)
    const already = said.current
    said.current = []
    // What she said while writing should be how the reply begins. If the
    // reply was redone after she started, she does not say it twice.
    const same = already.every((line, i) => final[i] === line)
    const rest = same ? final.slice(already.length) : already.length === 0 ? final : []
    for (const line of rest) {
      noteHers(line)
      sp.say(line)
    }
  })

  async function startListening() {
    setProblem('')
    try {
      ears.current = await openEars({
        // You began to speak: she stops at once, as a person would.
        start: () => {
          speaker.current?.stop()
          setEar('hearing')
        },
        heard: (wav) => {
          setEar('writing')
          apiHear(wav)
            .then((text) => {
              const words = text.trim()
              if (words.length > 1 && !notSpeech.includes(words.toLowerCase()) && !isHers(words)) say.current(words)
            })
            .catch((e: Error) => setProblem(`She could not make that out: ${e.message}`))
            .finally(() => setEar((cur) => (cur === 'off' ? 'off' : 'listening')))
        },
      })
    } catch {
      setProblem('She cannot hear you: the microphone is not allowed. Allow it for Cosmos in System Settings, under Privacy and Security.')
      return
    }
    setEar('listening')
  }

  function stopListening() {
    setEar('off')
    ears.current?.stop()
    ears.current = null
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
                ? 'listening'
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
        <button type="button" className={`her-sense ${seeing ? 'on' : ''}`} aria-pressed={seeing} aria-label={seeing ? 'Turn her camera off' : 'Let her see you'} title={seeing ? 'Turn her camera off' : 'Let her see you'} disabled={asleep} onClick={() => setSeeing(!seeing)}>
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
          <div className="her-sight" hidden={!seeing}>
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
      {listening && view !== 'only' && <p className="quiet">Just talk. She answers when you pause, and stops if you speak over her.</p>}
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
