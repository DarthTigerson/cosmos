import { api } from '../api'
import type { Mouth } from './stage'

// Her voice. The core turns a sentence into sound and says when each
// speech sound in it falls. The sound is played here, and her mouth is
// given the shape of each sound at the moment it is heard: lips closed
// on p, b and m, round on "oo", wide on "ee". That is what makes the
// words seem to come from her.

type Mark = [start: number, length: number, sound: string]

interface Clip {
  buffer: AudioBuffer
  marks: Mark[]
}

const SHUT: Mouth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 }

// The mouth for each speech sound, in the phonetic alphabet the voice
// uses. These follow the standard viseme groups (closed lips, lip to
// teeth, tongue to teeth, and so on) folded onto the five shapes her
// model has. Consonants are small: they shape the mouth without opening it.
const shapes: Record<string, Partial<Mouth>> = {}
const set = (sounds: string, shape: Partial<Mouth>) => {
  for (const s of sounds) shapes[s] = shape
}
set('pbm', {}) // lips together
set('fv', { ih: 0.25 }) // lower lip to the teeth
set('θð', { ih: 0.3, aa: 0.08 })
set('tdnlɾɫ', { ih: 0.3, aa: 0.1 })
set('kgŋxɡ', { aa: 0.22 })
set('szʃʒ', { ee: 0.3, ih: 0.15 })
set('ʧʤ', { ou: 0.3, ih: 0.15 }) // "ch", "j": lips pushed forward
set('ɹr', { oh: 0.3, ou: 0.15 })
set('w', { ou: 0.75 })
set('j', { ee: 0.5 })
set('hɦʔ', { aa: 0.18 })
set('ɑaæ', { aa: 0.95 })
set('ʌɐ', { aa: 0.6 })
set('əɜɚɝᵊ', { aa: 0.35, oh: 0.1 })
set('ɛe', { ee: 0.45, aa: 0.3 })
set('iɪ', { ee: 0.75, ih: 0.2 })
set('ɔɒ', { oh: 0.85, aa: 0.15 })
set('oO', { oh: 0.8 })
set('uʊ', { ou: 0.9 })
// The voice writes some double vowels as one letter.
set('A', { ee: 0.45, aa: 0.4 }) // "ay"
set('I', { aa: 0.8, ee: 0.2 }) // "eye"
set('W', { aa: 0.7, ou: 0.3 }) // "ow"
set('Y', { oh: 0.6, ee: 0.3 }) // "oy"
set('Q', { oh: 0.5, ou: 0.4 }) // British "oh"

const full = (s: Partial<Mouth>): Mouth => ({ ...SHUT, ...s })
const mix = (a: Mouth, b: Mouth, t: number): Mouth => ({
  aa: a.aa + (b.aa - a.aa) * t, ih: a.ih + (b.ih - a.ih) * t, ou: a.ou + (b.ou - a.ou) * t,
  ee: a.ee + (b.ee - a.ee) * t, oh: a.oh + (b.oh - a.oh) * t,
})

// timeline turns the voice's marks into mouth shapes over time. Stress
// and length marks carry no shape of their own: their time is given to
// the sound they belong to. Spaces and punctuation rest the mouth.
function timeline(marks: Mark[]): { at: number; until: number; mouth: Mouth }[] {
  const out: { at: number; until: number; mouth: Mouth }[] = []
  for (const [start, length, sound] of marks) {
    const last = out[out.length - 1]
    if (sound === 'ː' && last) {
      last.until = start + length // a long vowel is held
      continue
    }
    if (sound === 'ˈ' || sound === 'ˌ') continue // the next sound starts when it starts
    const shape = shapes[sound]
    if (shape === undefined) {
      // A pause. Short gaps between words do not shut the mouth.
      if (length > 0.12) out.push({ at: start, until: start + length, mouth: SHUT })
      continue
    }
    out.push({ at: start, until: start + length, mouth: full(shape) })
  }
  return out
}

// mouthAt is the mouth at time t. Each shape leans toward the next one
// as it ends, the way sounds run together in speech.
function mouthAt(line: { at: number; until: number; mouth: Mouth }[], t: number, from: number): [Mouth, number] {
  let i = from
  while (i < line.length - 1 && line[i + 1].at <= t) i++
  const cur = line[i]
  if (!cur || t < cur.at - 0.02) return [SHUT, i]
  const next = line[i + 1]
  if (t >= cur.until) {
    // Between sounds: already forming the next if it is close.
    if (next && next.at - t < 0.09) return [mix(SHUT, next.mouth, 0.5), i]
    return [SHUT, i]
  }
  const span = Math.max(0.02, cur.until - cur.at)
  const through = (t - cur.at) / span
  if (next && through > 0.6) return [mix(cur.mouth, next.mouth, ((through - 0.6) / 0.4) * 0.6), i]
  return [cur.mouth, i]
}

// sentences splits text where a listener would hear a break.
export function sentences(text: string): string[] {
  const out: string[] = []
  let cur = ''
  for (let i = 0; i < text.length; i++) {
    cur += text[i]
    const ends = '.!?…'.includes(text[i]) || text[i] === '\n'
    const next = text[i + 1]
    if (ends && (next === undefined || next === ' ' || next === '\n') && cur.trim().length > 1) {
      out.push(cur.trim())
      cur = ''
    }
  }
  if (cur.trim()) out.push(cur.trim())
  return out
}

// Speaker says sentences one after another. Each is asked for the moment
// it is known, so the sound for the second is being made while the first
// is still playing.
export class Speaker {
  private ctx: AudioContext | null = null
  private queue: Promise<Clip | null>[] = []
  private running = false
  private source: AudioBufferSourceNode | null = null
  private frame = 0
  private generation = 0
  // Everything she says passes through this, so she can be made quieter
  // while it is not yet clear whether someone is talking over her.
  private level: GainNode | null = null
  onMouth: (m: Mouth) => void = () => {}
  onState: (speaking: boolean) => void = () => {}
  onError: (message: string) => void = () => {}

  private audio(): AudioContext {
    this.ctx ??= new AudioContext()
    if (this.ctx.state === 'suspended') void this.ctx.resume()
    return this.ctx
  }

  private out(): GainNode {
    const ctx = this.audio()
    if (!this.level) {
      this.level = ctx.createGain()
      this.level.connect(ctx.destination)
    }
    return this.level
  }

  // duck lowers her voice while she might be being cut off, and brings
  // it back when it was only a noise.
  duck(on: boolean) {
    if (!this.ctx) return
    const g = this.out().gain
    g.cancelScheduledValues(this.ctx.currentTime)
    g.setTargetAtTime(on ? 0.25 : 1, this.ctx.currentTime, 0.05)
  }

  get speaking(): boolean {
    return this.running
  }

  say(text: string) {
    const words = text.trim()
    if (!words) return
    const ctx = this.audio()
    const gen = this.generation
    this.queue.push(
      api<{ audio: string; marks: Mark[] }>('/api/speech', { method: 'POST', body: { text: words } })
        .then(async (res) => {
          if (gen !== this.generation) return null
          const raw = Uint8Array.from(atob(res.audio), (c) => c.charCodeAt(0))
          return { buffer: await ctx.decodeAudioData(raw.buffer), marks: res.marks ?? [] }
        })
        .catch((e: Error) => {
          if (gen === this.generation) this.onError(`She could not speak: ${e.message}`)
          return null
        }),
    )
    if (!this.running) void this.run()
  }

  private async run() {
    this.running = true
    const gen = this.generation
    this.onState(true)
    while (this.queue.length && gen === this.generation) {
      const clip = await this.queue.shift()!
      if (clip && gen === this.generation) await this.play(clip, gen)
    }
    if (gen === this.generation) {
      this.running = false
      this.onMouth(SHUT)
      this.onState(false)
    }
  }

  private play(clip: Clip, gen: number): Promise<void> {
    const ctx = this.audio()
    const source = ctx.createBufferSource()
    source.buffer = clip.buffer
    const analyser = ctx.createAnalyser()
    analyser.fftSize = 1024
    analyser.smoothingTimeConstant = 0.4
    source.connect(analyser)
    analyser.connect(this.out())
    const line = timeline(clip.marks)
    const bins = new Uint8Array(analyser.frequencyBinCount)
    const perBin = ctx.sampleRate / analyser.fftSize
    const band = (from: number, to: number) => {
      let sum = 0
      const a = Math.floor(from / perBin)
      const b = Math.min(bins.length - 1, Math.ceil(to / perBin))
      for (let i = a; i <= b; i++) sum += bins[i]
      return sum / ((b - a + 1) * 255)
    }
    return new Promise((resolve) => {
      let at = 0
      const started = ctx.currentTime
      const tick = () => {
        if (gen !== this.generation) return
        if (line.length) {
          const [mouth, i] = mouthAt(line, ctx.currentTime - started, at)
          at = i
          this.onMouth(mouth)
        } else {
          // A voice with no timing: move the mouth by the sound alone.
          analyser.getByteFrequencyData(bins)
          const low = band(120, 450)
          const mid = band(450, 1600)
          const high = band(1600, 4200)
          const open = (v: number, floor: number, gain: number) => Math.max(0, Math.min(1, (v - floor) * gain))
          if (Math.max(low, mid, high) < 0.08) this.onMouth(SHUT)
          else this.onMouth({ ...SHUT, aa: open(mid, 0.1, 2.6), oh: open(low - mid * 0.5, 0.05, 2.2), ee: open(high - mid * 0.3, 0.04, 2.4) })
        }
        this.frame = requestAnimationFrame(tick)
      }
      source.onended = () => {
        cancelAnimationFrame(this.frame)
        if (this.source === source) this.source = null
        resolve()
      }
      this.source = source
      source.start()
      tick()
    })
  }

  // stop cuts her off mid-word and drops whatever she was about to say.
  stop() {
    this.generation++
    if (this.level && this.ctx) {
      this.level.gain.cancelScheduledValues(this.ctx.currentTime)
      this.level.gain.value = 1
    }
    this.queue = []
    cancelAnimationFrame(this.frame)
    try {
      this.source?.stop()
    } catch { /* already ended */ }
    this.source = null
    if (this.running) {
      this.running = false
      this.onMouth(SHUT)
      this.onState(false)
    }
  }
}
