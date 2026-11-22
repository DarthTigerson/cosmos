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

function copied(r: FaceLandmarkerResult): Copied | undefined {
  const shapes = r.faceBlendshapes?.[0]?.categories
  if (!shapes) return undefined
  const b = (name: string) => shapes.find((c) => c.categoryName === name)?.score ?? 0
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
  return {
    smile: clamp((b('mouthSmileLeft') + b('mouthSmileRight')) / 2),
    // A mirror: the eye on your left is the one on her right.
    blinkLeft: clamp(b('eyeBlinkRight')),
    blinkRight: clamp(b('eyeBlinkLeft')),
    browUp: clamp(Math.max(b('browInnerUp'), (b('browOuterUpLeft') + b('browOuterUpRight')) / 2)),
    frown: clamp((b('browDownLeft') + b('browDownRight')) / 2),
    jawOpen: clamp(b('jawOpen')),
    yaw: -yaw,
    pitch,
    roll: -roll,
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
  let sx = 0
  let sy = 0
  for (let i = 0, p = 0; i < now.length; i += 4, p++) {
    const d = Math.abs(now[i] - was[i]) + Math.abs(now[i + 1] - was[i + 1]) + Math.abs(now[i + 2] - was[i + 2])
    if (d > 90) {
      n++
      sx += p % MW
      sy += Math.floor(p / MW)
    }
  }
  if (n < MOTION_PIXELS) return null
  return { x: sx / n / MW, y: sy / n / MH }
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
  const moved = movement(picture)
  if (moved) {
    movedAt = t
    moveX = moved.x
    moveY = moved.y
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
    fx = (minX + maxX) / 2
    fy = (minY + maxY) / 2
    shown = face ? copied(face) : undefined
    const turned = shown ? Math.abs(shown.yaw) < 0.45 && Math.abs(shown.pitch) < 0.45 : true
    near = maxX - minX >= CLOSE && turned
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
