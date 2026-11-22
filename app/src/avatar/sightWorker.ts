/// <reference lib="webworker" />
import { FaceLandmarker, type FaceLandmarkerResult } from '@mediapipe/tasks-vision'
import type { Copied, Seen, Watching } from './sight'

// Her eyes, away from her figure: finding a face and movement takes long
// enough per picture that doing it beside the drawing made her stutter.
// Pictures come in from the camera; what she makes of them goes back.

// A face this wide in the picture is close to the screen.
const CLOSE = 0.24
// How long a choice holds before she changes her mind, and how long
// movement keeps her attention after it stops.
const HOLD_MS = 700
const MOTION_LINGER_MS = 1200
// The small picture movement is found in, and how much has to change.
const MW = 64
const MH = 48
const MOTION_PIXELS = 30

const clamp = (v: number) => Math.max(0, Math.min(1, v))

// The One Euro filter (Casiez, Roussel and Vogel, 2012): a smoothing
// that grows lighter the faster things move. Held still, the face
// finder's points shiver, and heavy smoothing steadies them; in motion,
// heavy smoothing would trail behind, so it lets go. Times in seconds.
class OneEuro {
  private x: number | null = null
  private dx = 0
  private at = 0
  constructor(private minCutoff: number, private beta: number, private dCutoff = 1) {}
  private static alpha(cutoff: number, dt: number) {
    const tau = 1 / (2 * Math.PI * cutoff)
    return 1 / (1 + tau / dt)
  }
  filter(v: number, t: number): number {
    if (this.x === null) {
      this.x = v
      this.at = t
      return v
    }
    const dt = Math.max(1e-3, t - this.at)
    this.at = t
    const a = OneEuro.alpha(this.dCutoff, dt)
    this.dx = a * ((v - this.x) / dt) + (1 - a) * this.dx
    const b = OneEuro.alpha(this.minCutoff + this.beta * Math.abs(this.dx), dt)
    this.x = b * v + (1 - b) * this.x
    return this.x
  }
  reset() { this.x = null; this.dx = 0 }
}

// A set of filters by name, made as needed.
class Steady {
  private f = new Map<string, OneEuro>()
  constructor(private minCutoff: number, private beta: number) {}
  get(name: string, v: number, t: number) {
    let one = this.f.get(name)
    if (!one) this.f.set(name, (one = new OneEuro(this.minCutoff, this.beta)))
    return one.filter(v, t)
  }
  reset() { for (const one of this.f.values()) one.reset() }
}
// Where things are, over a picture 0 to 1 wide; the parts of a face, 0
// to 1; the head's turn, in radians. Each wants its own give.
const place = new Steady(1.2, 1.5)
const parts = new Steady(2.5, 0.8)
const turns = new Steady(1.5, 0.6)

function copied(r: FaceLandmarkerResult, t: number): Copied | undefined {
  const shapes = r.faceBlendshapes?.[0]?.categories
  if (!shapes) return undefined
  const b = (name: string) => shapes.find((c) => c.categoryName === name)?.score ?? 0
  const k = (name: string, v: number) => parts.get(name, clamp(v), t)
  const r3 = (name: string, v: number) => turns.get(name, v, t)
  // The rotation, from the face's transform: yaw about y, pitch about x,
  // roll about z. The matrix is column-major.
  const m = r.facialTransformationMatrixes?.[0]?.data
  let yaw = 0
  let pitch = 0
  let roll = 0
  if (m) {
    yaw = Math.asin(Math.max(-1, Math.min(1, -m[8])))
    pitch = Math.atan2(m[9], m[10])
    roll = Math.atan2(m[1], m[0])
  }
  // Blinks pass unsmoothed: a smoothed blink is a slow, sleepy one.
  return {
    smile: k('smile', (b('mouthSmileLeft') + b('mouthSmileRight')) / 2),
    // A mirror: the eye on your left is the one on her right.
    blinkLeft: clamp(b('eyeBlinkRight')),
    blinkRight: clamp(b('eyeBlinkLeft')),
    browUp: k('browUp', (b('browOuterUpLeft') + b('browOuterUpRight')) / 2),
    browInner: k('browInner', b('browInnerUp')),
    wide: k('wide', (b('eyeWideLeft') + b('eyeWideRight')) / 2),
    down: k('down', (b('mouthFrownLeft') + b('mouthFrownRight')) / 2),
    frown: k('frown', (b('browDownLeft') + b('browDownRight')) / 2),
    jawOpen: k('jawOpen', b('jawOpen')),
    funnel: k('funnel', b('mouthFunnel')),
    pucker: k('pucker', b('mouthPucker')),
    stretch: k('stretch', (b('mouthStretchLeft') + b('mouthStretchRight')) / 2),
    squint: k('squint', (b('cheekSquintLeft') + b('cheekSquintRight') + b('eyeSquintLeft') + b('eyeSquintRight')) / 4),
    yaw: r3('yaw', -yaw),
    pitch: r3('pitch', pitch),
    roll: r3('roll', -roll),
  }
}

// MediaPipe loads its program with importScripts, which a module worker
// does not have. Set up beforehand, it skips that step.
async function faceFinder(urls: { loader: string; wasm: string; model: string }): Promise<FaceLandmarker> {
  const text = await (await fetch(urls.loader)).text()
  ;(self as unknown as { ModuleFactory: unknown }).ModuleFactory = new Function(`${text}\nreturn ModuleFactory`)()
  return FaceLandmarker.createFromOptions(
    { wasmLoaderPath: '', wasmBinaryPath: urls.wasm },
    {
      baseOptions: { modelAssetPath: urls.model, delegate: 'CPU' },
      runningMode: 'VIDEO',
      numFaces: 1,
      outputFaceBlendshapes: true,
      outputFacialTransformationMatrixes: true,
    },
  )
}

let landmarker: FaceLandmarker | null = null
const small = new OffscreenCanvas(MW, MH)
const pen = small.getContext('2d', { willReadFrequently: true })!
let before: Uint8ClampedArray | null = null
let watching: Watching = 'pointer'
let since = 0
let movedAt = -Infinity
let moveX = 0
let moveY = 0
let lastT = -1

// Where in the small picture things changed since the last look.
function movement(picture: ImageBitmap): { x: number; y: number } | null {
  pen.drawImage(picture, 0, 0, MW, MH)
  const now = pen.getImageData(0, 0, MW, MH).data
  const was = before
  before = new Uint8ClampedArray(now)
  if (!was) return null
  let n = 0
  let w = 0
  let sx = 0
  let sy = 0
  for (let i = 0, p = 0; i < now.length; i += 4, p++) {
    const d = Math.abs(now[i] - was[i]) + Math.abs(now[i + 1] - was[i + 1]) + Math.abs(now[i + 2] - was[i + 2])
    if (d > 90) {
      // Weighted by how much changed, so the middle of a moving thing
      // counts for more than its flickering edge.
      n++
      w += d
      sx += (p % MW) * d
      sy += Math.floor(p / MW) * d
    }
  }
  if (n < MOTION_PIXELS) return null
  return { x: sx / w / MW, y: sy / w / MH }
}

function look(picture: ImageBitmap, t: number): Seen {
  let face: FaceLandmarkerResult | null = null
  if (landmarker && t > lastT) {
    lastT = t
    try {
      face = landmarker.detectForVideo(picture, t)
    } catch {
      face = null
    }
  }
  const sec = t / 1000
  const moved = movement(picture)
  if (moved) {
    movedAt = t
    moveX = place.get('moveX', moved.x, sec)
    moveY = place.get('moveY', moved.y, sec)
  }

  // A face: how wide it is, where it is, and whether it faces her.
  const points = face?.faceLandmarks?.[0]
  let near = false
  let fx = 0.5
  let fy = 0.5
  let shown: Copied | undefined
  if (points && points.length > 0) {
    let minX = 1
    let maxX = 0
    let minY = 1
    let maxY = 0
    for (const p of points) {
      minX = Math.min(minX, p.x)
      maxX = Math.max(maxX, p.x)
      minY = Math.min(minY, p.y)
      maxY = Math.max(maxY, p.y)
    }
    fx = place.get('faceX', (minX + maxX) / 2, sec)
    fy = place.get('faceY', (minY + maxY) / 2, sec)
    shown = face ? copied(face, sec) : undefined
    const turned = shown ? Math.abs(shown.yaw) < 0.45 && Math.abs(shown.pitch) < 0.45 : true
    near = maxX - minX >= CLOSE && turned
  } else if (face) {
    // The face has gone; the next one starts fresh rather than sliding in
    // from where this one was.
    for (const f of [parts, turns]) f.reset()
    place.reset()
  }

  // Her choice, held a moment so she does not flicker between them.
  const wants: Watching = near ? 'copy' : t - movedAt < MOTION_LINGER_MS ? 'motion' : 'pointer'
  if (wants !== watching && t - since > HOLD_MS) {
    watching = wants
    since = t
  }

  // Mirrored, so that your right is her left.
  if (watching === 'copy') return { watching, x: 1 - fx * 2, y: fy * 2 - 1, face: shown }
  if (watching === 'motion') {
    // A face in view is what is moving, as a rule; follow it.
    const [x, y] = points ? [fx, fy] : [moveX, moveY]
    return { watching, x: 1 - x * 2, y: y * 2 - 1 }
  }
  return { watching, x: 0, y: 0 }
}

self.onmessage = (e: MessageEvent) => {
  const m = e.data
  if (m.kind === 'start') {
    faceFinder(m.urls)
      .then((f) => {
        landmarker = f
        self.postMessage({ kind: 'ready' })
      })
      .catch((err: Error) => {
        // Without the face finder she still follows movement.
        self.postMessage({ kind: 'failed', why: String(err?.message ?? err) })
      })
    return
  }
  if (m.kind === 'picture') {
    const picture = m.picture as ImageBitmap
    const seen = look(picture, m.t as number)
    picture.close()
    self.postMessage({ kind: 'seen', seen })
  }
}
