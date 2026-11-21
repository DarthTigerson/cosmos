// Mic capture + WAV encoding for push-to-talk (LIVE-6).
// Captures Float32 PCM from the mic via getUserMedia, downsamples to 16kHz
// mono, and encodes a standard 16-bit PCM WAV ArrayBuffer ready to send to
// whisper-cli.

export interface MicRecording {
  stop: () => Promise<ArrayBuffer>
}

/** Start recording from the mic. Returns a handle with a stop() that resolves
 *  the encoded WAV when called. Throws if the user denies mic permission. */
export async function startMicRecording(): Promise<MicRecording> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
  const TARGET_SAMPLE_RATE = 16000

  // Prefer a 16kHz AudioContext (many browsers allow it). If the browser
  // clamps it, we'll downsample manually in stop().
  let ctx: AudioContext
  try {
    ctx = new AudioContext({ sampleRate: TARGET_SAMPLE_RATE })
  } catch {
    ctx = new AudioContext()
  }

  const source = ctx.createMediaStreamSource(stream)

  // 4096-frame buffer — small latency, big enough to avoid dropout clicks.
  // ScriptProcessorNode is deprecated but still the most compatible approach
  // for collecting raw Float32 samples in Electron's renderer.
  const bufferSize = 4096
  const processor = ctx.createScriptProcessor(bufferSize, 1, 1)
  const chunks: Float32Array[] = []

  processor.onaudioprocess = (e) => {
    // Clone the buffer — it's reused on the next callback.
    const input = e.inputBuffer.getChannelData(0)
    chunks.push(new Float32Array(input))
  }

  source.connect(processor)
  processor.connect(ctx.destination)

  return {
    stop: async (): Promise<ArrayBuffer> => {
      // Disconnect everything first so no more samples arrive.
      processor.disconnect()
      source.disconnect()
      stream.getTracks().forEach((t) => t.stop())

      // Concatenate all chunks into one flat Float32 array.
      const totalLength = chunks.reduce((sum, c) => sum + c.length, 0)
      const raw = new Float32Array(totalLength)
      let offset = 0
      for (const chunk of chunks) {
        raw.set(chunk, offset)
        offset += chunk.length
      }

      // Downsample to 16kHz if the context ran at a different rate.
      const capturedRate = ctx.sampleRate
      await ctx.close()

      const pcm = capturedRate === TARGET_SAMPLE_RATE
        ? raw
        : downsample(raw, capturedRate, TARGET_SAMPLE_RATE)

      return encodeWav(pcm, TARGET_SAMPLE_RATE)
    },
  }
}

/** Nearest-neighbour downsample — good enough for speech. */
function downsample(input: Float32Array, fromRate: number, toRate: number): Float32Array {
  if (fromRate === toRate) return input
  const ratio = fromRate / toRate
  const outputLength = Math.floor(input.length / ratio)
  const output = new Float32Array(outputLength)
  for (let i = 0; i < outputLength; i++) {
    output[i] = input[Math.round(i * ratio)]
  }
  return output
}

/** Encode Float32 PCM samples (range –1..1, mono) as a 16-bit PCM WAV file. */
export function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const numSamples = samples.length
  const bytesPerSample = 2  // 16-bit
  const dataSize = numSamples * bytesPerSample
  const buffer = new ArrayBuffer(44 + dataSize)
  const view = new DataView(buffer)

  // RIFF header
  writeString(view, 0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)        // ChunkSize
  writeString(view, 8, 'WAVE')

  // fmt sub-chunk
  writeString(view, 12, 'fmt ')
  view.setUint32(16, 16, true)                  // Subchunk1Size (PCM = 16)
  view.setUint16(20, 1, true)                   // AudioFormat (PCM = 1)
  view.setUint16(22, 1, true)                   // NumChannels (mono)
  view.setUint32(24, sampleRate, true)           // SampleRate
  view.setUint32(28, sampleRate * bytesPerSample, true) // ByteRate
  view.setUint16(32, bytesPerSample, true)       // BlockAlign
  view.setUint16(34, 16, true)                  // BitsPerSample

  // data sub-chunk
  writeString(view, 36, 'data')
  view.setUint32(40, dataSize, true)

  // PCM samples: Float32 → Int16
  let idx = 44
  for (let i = 0; i < numSamples; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(idx, s < 0 ? s * 0x8000 : s * 0x7fff, true)
    idx += 2
  }

  return buffer
}

function writeString(view: DataView, offset: number, str: string): void {
  for (let i = 0; i < str.length; i++) {
    view.setUint8(offset + i, str.charCodeAt(i))
  }
}
