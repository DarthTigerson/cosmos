import { useEffect, useRef, useState } from 'react'
import { apiHear, useSignals } from '../api'
import type { State, StepSignal, StreamEvent } from '../types'
import { type Ears, openEars } from './ears'
import { sentences, Speaker } from './speech'
import { type Face, Stage } from './stage'

// Which of the model's faces goes with each feeling, and how strongly.
const faces: Record<string, [Face['expression'], number]> = {
  glad: ['happy', 0.6], warmth: ['happy', 0.5], tenderness: ['relaxed', 0.7], amusement: ['happy', 0.8],
  pride: ['happy', 0.65], hope: ['happy', 0.4], curiosity: ['surprised', 0.3],
  concern: ['sad', 0.3], worry: ['sad', 0.4], sadness: ['sad', 0.7], hurt: ['sad', 0.6], unease: ['sad', 0.25],
  anger: ['angry', 0.55],
}

function remembered(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(key)
    return v === null ? fallback : v === 'true'
  } catch {
    return fallback
  }
}

function remember(key: string, value: boolean) {
  try {
    localStorage.setItem(key, String(value))
  } catch { /* the choice just will not be kept */ }
}

// An arrow for folding her away (up) and bringing her back (down).
function Arrow({ down }: { down: boolean }) {
  return (
    <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="none" style={down ? undefined : { transform: 'rotate(180deg)' }}>
      <path d="M3 6l5 5 5-5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// The words of a line, lower case, without punctuation.
function wordsOf(line: string): string[] {
  return line.toLowerCase().split(/[^a-z0-9']+/).filter((w) => w.length > 1)
}

type Ear = 'off' | 'listening' | 'hearing' | 'writing'

// Things a speech recogniser writes when it is handed a noise.
const notSpeech = ['you', 'thank you.', 'thanks for watching!', 'thank you for watching.', 'bye.', '.', 'okay.']

// Her figure beside the chat: the 3D model, alive between words. "Talk
// to her" starts a call that works like a phone call: she listens, she
// knows when you have finished, she answers aloud as she writes, and
// she stops if you cut in. There is nothing to hold down.
export function Avatar({ state, working, onSay }: { state: State | null; working: boolean; onSay: (words: string) => void }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const stage = useRef<Stage | null>(null)
  const speaker = useRef<Speaker | null>(null)
  const ears = useRef<Ears | null>(null)
  const [shown, setShown] = useState(() => remembered('cosmos:avatar', true))
  const [ready, setReady] = useState(false)
  const [problem, setProblem] = useState('')
  const [speaking, setSpeaking] = useState(false)
  const [call, setCall] = useState(false)
  const [muted, setMuted] = useState(false)
  const [ear, setEar] = useState<Ear>('off')
  const inCall = useRef(call)
  inCall.current = call
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

  // Leaving the screen ends the call.
  useEffect(
    () => () => {
      speaker.current?.stop()
      ears.current?.stop()
      ears.current = null
    },
    [],
  )

  const asleep = state?.state === 'asleep'
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

  // In a call she speaks as she writes. Each sentence is said as soon as
  // the next one has begun; the last waits for the finished reply, since
  // the end of a draft is the part most likely to change.
  useSignals((s) => {
    if (!inCall.current) return
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

  async function startCall() {
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
    setMuted(false)
    setEar('listening')
    setCall(true)
  }

  function endCall() {
    setCall(false)
    setEar('off')
    speaker.current?.stop()
    ears.current?.stop()
    ears.current = null
    said.current = []
  }

  function toggleMute() {
    const next = !muted
    setMuted(next)
    ears.current?.setMuted(next)
  }

  function show(next: boolean) {
    setShown(next)
    remember('cosmos:avatar', next)
    if (!next) endCall()
  }

  if (!shown) {
    return (
      <div className="avatar-row">
        <span className="avatar-mood">Feeling {feeling}</span>
        <button type="button" className="secondary avatar-arrow" aria-label="Show her" title="Show her" onClick={() => show(true)}>
          <Arrow down />
        </button>
      </div>
    )
  }

  const word = asleep
    ? 'asleep'
    : speaking
      ? 'talking'
      : ear === 'hearing'
        ? 'hearing you'
        : pose === 'thinking'
          ? 'thinking'
          : call
            ? muted
              ? 'not listening'
              : 'listening'
            : 'here'
  const lit = word === 'talking' || word === 'hearing you' || word === 'thinking'
  return (
    <div className="avatar">
      <div className="avatar-stage">
        <canvas ref={canvas} role="img" aria-label={`${state?.name ?? 'She'}, ${word}`} />
        {!ready && !problem && <span className="avatar-wait">She is arriving…</span>}
      </div>
      <div className="avatar-row">
        <span className={`avatar-state ${lit ? 'lit' : ''} ${asleep ? 'asleep' : ''}`} role="status">
          <i aria-hidden="true" />
          {word}
        </span>
        <span className="avatar-mood">
          {feeling} · warmth {warmth.toFixed(2)}
        </span>
      </div>
      {problem && (
        <p className="problem" role="alert">
          {problem}
        </p>
      )}
      {call ? (
        <div className="avatar-actions">
          <button type="button" className="secondary" aria-pressed={muted} onClick={toggleMute}>
            {muted ? 'Unmute' : 'Mute'}
          </button>
          <button type="button" className="primary" onClick={endCall}>
            End the call
          </button>
        </div>
      ) : (
        <div className="avatar-actions">
          <button type="button" className="primary" disabled={asleep} onClick={() => void startCall()}>
            Talk to her
          </button>
          <button type="button" className="secondary avatar-arrow" aria-label="Hide her" title="Hide her" onClick={() => show(false)}>
            <Arrow down={false} />
          </button>
        </div>
      )}
      {call && <p className="quiet">Just talk. She answers when you pause, and stops if you speak over her.</p>}
    </div>
  )
}
