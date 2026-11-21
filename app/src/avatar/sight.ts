import { FaceLandmarker, type FaceLandmarkerResult } from '@mediapipe/tasks-vision'
// The face finder's program and its model ship with the app; nothing is
// fetched from outside, and no picture leaves the Mac.
import wasmLoaderPath from '../../node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_internal.js?url'
import wasmBinaryPath from '../../node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_internal.wasm?url'

// What she sees through the camera, and what she makes of it. She picks
// for herself, from the picture:
//   - a face up close and turned to her: she copies it;
//   - anything moving: she follows it;
//   - nothing: she goes back to the pointer.
export type Watching = 'pointer' | 'motion' | 'copy'

// The face she copies, each part 0 to 1, as a mirror shows it.
export interface Copied {
  smile: number
  blinkLeft: number
  blinkRight: number
  browUp: number
  frown: number
  jawOpen: number
  // Head turn and tilt in radians, mirrored.
  yaw: number
  pitch: number
  roll: number
}

export interface Seen {
  watching: Watching
  // Where to look, -1 to 1 each way over the picture, mirrored so that
  // your right is her left, as the pointer is: up is -1.
  x: number
  y: number
  face?: Copied
}

// Looked at fifteen times a second: enough to follow someone, and light
// beside her models.
const EVERY_MS = 66
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

const MODEL = './mediapipe/face_landmarker.task'

let finder: Promise<FaceLandmarker> | null = null
function faceFinder(): Promise<FaceLandmarker> {
  finder ??= FaceLandmarker.createFromOptions(
    { wasmLoaderPath, wasmBinaryPath },
    {
      baseOptions: { modelAssetPath: MODEL, delegate: 'GPU' },
      runningMode: 'VIDEO',
      numFaces: 1,
      outputFaceBlendshapes: true,
      outputFacialTransformationMatrixes: true,
    },
  ).catch((e) => {
    finder = null
    throw e
  })
  return finder
}

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

// Opens the camera into the given video element and reports what she
// makes of it until stopped.
export async function openSight(video: HTMLVideoElement, report: (seen: Seen) => void): Promise<{ stop: () => void }> {
  const stream = await navigator.mediaDevices.getUserMedia({ video: { width: 640, height: 480, facingMode: 'user' }, audio: false })
  video.srcObject = stream
  video.muted = true
  video.playsInline = true
  await video.play()

  let landmarker: FaceLandmarker | null = null
  faceFinder()
    .then((f) => {
      landmarker = f
    })
    .catch(() => {
      // Without the face finder she still follows movement.
    })

  const small = document.createElement('canvas')
  small.width = MW
  small.height = MH
  const pen = small.getContext('2d', { willReadFrequently: true })!
  let before: Uint8ClampedArray | null = null

  let stopped = false
  let watching: Watching = 'pointer'
  let since = 0
  let movedAt = -Infinity
  let moveX = 0
  let moveY = 0
  let lastVideoTime = -1

  // Where in the small picture things changed since the last look.
  function movement(): { x: number; y: number } | null {
    pen.drawImage(video, 0, 0, MW, MH)
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

  const timer = window.setInterval(() => {
    if (stopped || video.readyState < 2 || document.hidden) return
    const t = performance.now()
    let face: FaceLandmarkerResult | null = null
    if (landmarker && video.currentTime !== lastVideoTime) {
      lastVideoTime = video.currentTime
      try {
        face = landmarker.detectForVideo(video, t)
      } catch {
        face = null
      }
    }
    const moved = movement()
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
    if (watching === 'copy') report({ watching, x: 1 - fx * 2, y: fy * 2 - 1, face: shown })
    else if (watching === 'motion') {
      // A face in view is what is moving, as a rule; follow it.
      const [x, y] = points ? [fx, fy] : [moveX, moveY]
      report({ watching, x: 1 - x * 2, y: y * 2 - 1 })
    } else report({ watching, x: 0, y: 0 })
  }, EVERY_MS)

  return {
    stop() {
      stopped = true
      window.clearInterval(timer)
      for (const track of stream.getTracks()) track.stop()
      video.srcObject = null
    },
  }
}
