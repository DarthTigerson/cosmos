import { encodeWav } from './mic'

// Her ears during a call. The microphone stays open, and this decides on
// its own when you have started speaking and when you have finished, so
// talking to her needs no button: you talk, pause, and she answers.

export interface Ears {
  // While she is speaking she listens less keenly, so her own voice
  // coming out of the speakers does not count as you.
  setHerSpeaking(on: boolean): void
  setMuted(on: boolean): void
  stop(): void
}

const RATE = 16000
const BLOCK = 2048 // about an eighth of a second
const blockSeconds = BLOCK / RATE

export async function openEars(on: {
  // Something loud began: maybe you, maybe a cough.
  start: () => void
  // It has gone on long enough to be someone speaking.
  confirmed?: () => void
  // It was too short to be speech, and is dropped.
  dropped?: () => void
  // You finished: here is what you said, as a WAV file.
  heard: (wav: ArrayBuffer) => void
  // Loudness of the room, 0 to 1, for showing that she hears something.
  level?: (v: number) => void
}): Promise<Ears> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } })
  let ctx: AudioContext
  try {
    ctx = new AudioContext({ sampleRate: RATE })
  } catch {
    ctx = new AudioContext()
  }
  const rate = ctx.sampleRate
  const source = ctx.createMediaStreamSource(stream)
  const node = ctx.createScriptProcessor(BLOCK, 1, 1)
  const quiet = ctx.createGain()
  quiet.gain.value = 0 // the microphone is never played back

  let herSpeaking = false
  let muted = false
  let floor = 0.004 // how loud the room is with nobody talking
  // How loud her own voice is at the microphone, coming back from the
  // speakers. Learned while she talks, and kept for a moment after she
  // stops, since a room rings on.
  let echo = 0
  let echoHold = 0
  let speaking = false
  let loudRun = 0
  let silentRun = 0
  // How much of what is being kept was loud, and whether that has
  // already been enough to count as speech.
  let loudKept = 0
  let sure = false
  let kept: Float32Array[] = []
  // A little from before the first loud block, so first syllables are kept.
  const before: Float32Array[] = []

  const finish = () => {
    const blocks = kept
    kept = []
    speaking = false
    silentRun = 0
    loudKept = 0
    sure = false
    const spoken = blocks.length * blockSeconds
    // A cough or a door is not a sentence.
    if (spoken < 0.45) {
      on.dropped?.()
      return
    }
    let total = 0
    for (const b of blocks) total += b.length
    const all = new Float32Array(total)
    let at = 0
    for (const b of blocks) {
      all.set(b, at)
      at += b.length
    }
    let pcm = all
    if (rate !== RATE) {
      const ratio = rate / RATE
      pcm = new Float32Array(Math.floor(all.length / ratio))
      for (let i = 0; i < pcm.length; i++) pcm[i] = all[Math.round(i * ratio)]
    }
    on.heard(encodeWav(pcm, RATE))
  }

  node.onaudioprocess = (e) => {
    const block = new Float32Array(e.inputBuffer.getChannelData(0))
    let sum = 0
    for (let i = 0; i < block.length; i++) sum += block[i] * block[i]
    const loudness = Math.sqrt(sum / block.length)
    on.level?.(Math.min(1, loudness * 12))
    if (muted) {
      if (speaking) {
        kept = []
        speaking = false
        loudKept = 0
        sure = false
        on.dropped?.()
      }
      return
    }
    // Speech has to stand clear of the room. While she is talking it has
    // to stand well clear of her own voice too, and for longer: you
    // cutting in is louder at the microphone than she is, and lasts.
    if (herSpeaking) echoHold = 0.6
    else echoHold = Math.max(0, echoHold - blockSeconds)
    const overHer = echoHold > 0
    let bar = Math.max(0.012, floor * 3.2)
    if (overHer) bar = Math.max(bar * 2, echo * 2.4)
    const loud = loudness > bar
    if (!speaking) {
      if (herSpeaking) echo += (loudness - echo) * (loudness > echo ? 0.5 : 0.1)
      else if (!overHer) {
        echo *= 0.98
        if (!loud) floor += (loudness - floor) * 0.05
      }
      before.push(block)
      if (before.length > 3) before.shift()
      loudRun = loud ? loudRun + 1 : 0
      if (loudRun >= (overHer ? 4 : 2)) {
        speaking = true
        silentRun = 0
        kept = [...before]
        before.length = 0
        loudKept = loudRun
        loudRun = 0
        on.start()
      }
      return
    }
    kept.push(block)
    silentRun = loud ? 0 : silentRun + 1
    if (loud) loudKept++
    // Half a second of sound is a voice, not a knock.
    if (!sure && loudKept * blockSeconds >= 0.5) {
      sure = true
      on.confirmed?.()
    }
    // Seven tenths of a second of quiet ends what you were saying; half
    // a minute is the most she takes in one go.
    if (silentRun * blockSeconds >= 0.7 || kept.length * blockSeconds > 30) finish()
  }

  source.connect(node)
  node.connect(quiet)
  quiet.connect(ctx.destination)

  return {
    setHerSpeaking: (v) => { herSpeaking = v },
    setMuted: (v) => { muted = v },
    stop: () => {
      node.disconnect()
      source.disconnect()
      stream.getTracks().forEach((t) => t.stop())
      void ctx.close()
    },
  }
}
