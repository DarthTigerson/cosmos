// The microphone, on the audio thread. It brings the sound to 16 kHz
// (filtered first, so nothing above what 16 kHz can hold folds back in
// as noise), cuts it into blocks of 512 samples as 16-bit numbers, and
// posts each block with how loud it was. Nothing here touches the page,
// so her figure and the chat never make the sound stutter.

declare const sampleRate: number
declare function registerProcessor(name: string, ctor: unknown): void
declare class AudioWorkletProcessor {
  readonly port: MessagePort
}

const OUT = 16000
const BLOCK = 512

// A windowed-sinc low-pass, cut a little under half the output rate.
function lowPass(ratio: number): Float32Array {
  const taps = Math.max(1, Math.ceil(ratio * 8)) * 2 + 1
  const cut = 0.45 / ratio
  const h = new Float32Array(taps)
  const mid = (taps - 1) / 2
  let sum = 0
  for (let i = 0; i < taps; i++) {
    const x = i - mid
    const sinc = x === 0 ? 2 * cut : Math.sin(2 * Math.PI * cut * x) / (Math.PI * x)
    const window = 0.42 - 0.5 * Math.cos((2 * Math.PI * i) / (taps - 1)) + 0.08 * Math.cos((4 * Math.PI * i) / (taps - 1))
    h[i] = sinc * window
    sum += h[i]
  }
  for (let i = 0; i < taps; i++) h[i] /= sum
  return h
}

class Mic extends AudioWorkletProcessor {
  private ratio = sampleRate / OUT
  private filter = this.ratio > 1 ? lowPass(this.ratio) : null
  // The recent input, for the filter to look back over.
  private history = new Float32Array(this.filter ? this.filter.length : 1)
  private filled = 0
  // Where, in input samples, the next output sample falls.
  private next = 0
  private seen = 0
  private block = new Int16Array(BLOCK)
  private at = 0
  private sum = 0

  process(inputs: Float32Array[][]): boolean {
    const input = inputs[0]?.[0]
    if (!input) return true
    for (let i = 0; i < input.length; i++) this.take(input[i])
    return true
  }

  private take(v: number) {
    if (!this.filter) {
      this.put(v)
      return
    }
    const h = this.history
    h.copyWithin(0, 1)
    h[h.length - 1] = v
    if (this.filled < h.length) this.filled++
    this.seen++
    while (this.next < this.seen) {
      let y = 0
      for (let k = 0; k < h.length; k++) y += h[k] * this.filter[k]
      this.put(y)
      this.next += this.ratio
    }
  }

  private put(v: number) {
    const s = Math.max(-1, Math.min(1, v))
    this.sum += s * s
    this.block[this.at++] = s < 0 ? s * 0x8000 : s * 0x7fff
    if (this.at < BLOCK) return
    const level = Math.sqrt(this.sum / BLOCK)
    const out = this.block
    this.port.postMessage({ pcm: out.buffer, level }, [out.buffer])
    this.block = new Int16Array(BLOCK)
    this.at = 0
    this.sum = 0
  }
}

registerProcessor('cosmos-mic', Mic)
