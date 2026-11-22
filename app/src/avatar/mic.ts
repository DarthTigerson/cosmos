import workletUrl from './micWorklet.ts?worker&url'

// The microphone: 16 kHz mono, 16-bit, in blocks of 512 samples, cut on
// the audio thread (micWorklet.ts). Used for a call with her, and to
// record a passage so she learns your voice.

export interface Mic {
  stop(): void
}

export async function openMic(onBlock: (pcm: Int16Array<ArrayBuffer>, level: number) => void): Promise<Mic> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } })
  let ctx: AudioContext
  try {
    // At 16 kHz the browser does the resampling itself.
    ctx = new AudioContext({ sampleRate: 16000 })
  } catch {
    ctx = new AudioContext()
  }
  try {
    await ctx.audioWorklet.addModule(workletUrl)
  } catch (e) {
    stream.getTracks().forEach((t) => t.stop())
    void ctx.close()
    throw e
  }
  const source = ctx.createMediaStreamSource(stream)
  const node = new AudioWorkletNode(ctx, 'cosmos-mic', { numberOfInputs: 1, numberOfOutputs: 0 })
  node.port.onmessage = (e: MessageEvent<{ pcm: ArrayBuffer; level: number }>) => onBlock(new Int16Array(e.data.pcm), e.data.level)
  source.connect(node)
  return {
    stop: () => {
      node.port.onmessage = null
      source.disconnect()
      node.disconnect()
      stream.getTracks().forEach((t) => t.stop())
      void ctx.close()
    },
  }
}

export interface MicRecording {
  stop: () => Promise<ArrayBuffer>
}

// startMicRecording records until stopped, and gives back a WAV file.
export async function startMicRecording(): Promise<MicRecording> {
  const blocks: Int16Array[] = []
  const mic = await openMic((pcm) => blocks.push(pcm))
  return {
    stop: async () => {
      mic.stop()
      return wavOf(blocks)
    },
  }
}

// wavOf wraps 16 kHz mono blocks as a 16-bit WAV file.
export function wavOf(blocks: Int16Array[]): ArrayBuffer {
  const samples = blocks.reduce((n, b) => n + b.length, 0)
  const buffer = new ArrayBuffer(44 + samples * 2)
  const view = new DataView(buffer)
  const text = (at: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(at + i, s.charCodeAt(i))
  }
  text(0, 'RIFF')
  view.setUint32(4, 36 + samples * 2, true)
  text(8, 'WAVE')
  text(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, 16000, true)
  view.setUint32(28, 32000, true)
  view.setUint16(32, 2, true)
  view.setUint16(34, 16, true)
  text(36, 'data')
  view.setUint32(40, samples * 2, true)
  let at = 44
  for (const b of blocks)
    for (let i = 0; i < b.length; i++, at += 2) view.setInt16(at, b[i], true)
  return buffer
}
