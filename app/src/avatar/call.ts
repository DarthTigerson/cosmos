import { socketAddress } from '../api'
import { openMic } from './mic'

// A call with her: the microphone goes to the core over one connection,
// and her voice comes back over it. The core decides when you started
// and stopped speaking, whose voice it was, and whether it was for her;
// this side plays her voice and does what it is told.

export type Mark = [start: number, length: number, sound: string]

export interface Spoken {
  audio: ArrayBuffer
  marks: Mark[]
}

export interface Call {
  // Whether she waits for her name.
  setByName(on: boolean): void
  // Her voice started or stopped playing.
  setHerSpeaking(on: boolean): void
  // Her reply is over, said and shown.
  idle(): void
  // speak asks for the sound of a sentence. played says it started.
  speak(text: string, voice?: string): { sound: Promise<Spoken>; played: () => void }
  stop(): void
}

export interface CallEvents {
  ready?: (detector: string) => void
  // Something began: maybe you, maybe a cough.
  start: () => void
  // It was not speech.
  dropped: () => void
  // Someone is speaking over her.
  cut: () => void
  // What was said is being written out.
  writing: () => void
  // Send this to her, as said aloud.
  say: (text: string, heard: number, cut: boolean) => void
  // Not for her; she carries on.
  ignored: (overheard?: string) => void
  // Whether she takes what is said as for her without her name.
  window: (open: boolean) => void
  problem: (text: string) => void
  // How loud the room is, 0 to 1.
  level?: (v: number) => void
  // The call ended without being stopped.
  closed: () => void
}

export async function openCall(on: CallEvents, byName: boolean): Promise<Call> {
  const ws = new WebSocket(await socketAddress('/api/call'))
  ws.binaryType = 'arraybuffer'
  await new Promise<void>((resolve, reject) => {
    ws.onopen = () => resolve()
    ws.onerror = () => reject(new Error('The core would not take the call.'))
  })
  const send = (v: unknown) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(v))
  }
  send({ type: 'mode', by_name: byName })

  let nextId = 0
  const waiting = new Map<number, { resolve: (s: Spoken) => void; reject: (e: Error) => void }>()
  let stopped = false
  ws.onmessage = (e: MessageEvent<string | ArrayBuffer>) => {
    if (typeof e.data !== 'string') {
      // Her voice: a header, then the sound.
      const view = new DataView(e.data)
      const size = view.getUint32(0, true)
      const head = JSON.parse(new TextDecoder().decode(new Uint8Array(e.data, 4, size))) as { id: number; marks?: Mark[]; error?: string }
      const w = waiting.get(head.id)
      if (!w) return
      waiting.delete(head.id)
      if (head.error) w.reject(new Error(head.error))
      else w.resolve({ audio: e.data.slice(4 + size), marks: head.marks ?? [] })
      return
    }
    const m = JSON.parse(e.data) as { type: string; text?: string; heard?: number; cut?: boolean; open?: boolean; detector?: string }
    switch (m.type) {
      case 'ready':
        on.ready?.(m.detector ?? '')
        break
      case 'start':
        on.start()
        break
      case 'dropped':
        on.dropped()
        break
      case 'cut':
        on.cut()
        break
      case 'writing':
        on.writing()
        break
      case 'say':
        on.say(m.text ?? '', m.heard ?? 0, !!m.cut)
        break
      case 'overheard':
        on.ignored(m.text)
        break
      case 'ignored':
        on.ignored()
        break
      case 'window':
        on.window(!!m.open)
        break
      case 'problem':
        on.problem(m.text ?? '')
        break
    }
  }

  let mic: { stop(): void } | null = null
  ws.onclose = () => {
    mic?.stop()
    for (const w of waiting.values()) w.reject(new Error('the call ended'))
    waiting.clear()
    if (!stopped) on.closed()
  }
  try {
    mic = await openMic((pcm, level) => {
      on.level?.(Math.min(1, level * 12))
      if (ws.readyState === WebSocket.OPEN) ws.send(pcm)
    })
  } catch (e) {
    stopped = true
    ws.close()
    throw e
  }

  return {
    setByName: (v) => send({ type: 'mode', by_name: v }),
    setHerSpeaking: (v) => send({ type: 'her', speaking: v }),
    idle: () => send({ type: 'idle' }),
    speak: (text, voice) => {
      const id = ++nextId
      const sound = new Promise<Spoken>((resolve, reject) => {
        if (ws.readyState !== WebSocket.OPEN) return reject(new Error('the call ended'))
        waiting.set(id, { resolve, reject })
        send({ type: 'speak', id, text, voice: voice ?? '' })
      })
      return { sound, played: () => send({ type: 'played', id }) }
    },
    stop: () => {
      stopped = true
      mic?.stop()
      ws.close()
    },
  }
}
