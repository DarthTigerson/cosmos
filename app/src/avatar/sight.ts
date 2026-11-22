// The face finder's program and its model ship with the app; nothing is
// fetched from outside, and no picture leaves the Mac.
import wasmLoaderPath from '../../node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_internal.js?url'
import wasmBinaryPath from '../../node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_internal.wasm?url'

// What she sees through the camera, and what she makes of it. She picks
// for herself, from the picture:
//   - a face up close and turned to her: she copies it;
//   - anything moving: she follows it;
//   - nothing: she goes back to the pointer.
// The looking happens in a worker (sightWorker.ts), so that her figure
// keeps its pace.
export type Watching = 'pointer' | 'motion' | 'copy'

// The face she copies, each part 0 to 1, as a mirror shows it.
export interface Copied {
  smile: number
  blinkLeft: number
  blinkRight: number
  browUp: number
  frown: number
  // The inner brows raised alone, as in worry; eyes opened wide; the
  // corners of the mouth pulled down.
  browInner: number
  wide: number
  down: number
  jawOpen: number
  // Lips pushed into an O, pursed, pulled wide; cheeks raised and eyes
  // narrowed, as in a real smile.
  funnel: number
  pucker: number
  stretch: number
  squint: number
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

// Looked at up to twenty-five times a second, off her drawing's thread;
// a picture is sent only once the last is done, so a slow Mac simply
// looks less often.
const EVERY_MS = 40
// Pictures are made small before they are looked at.
const PW = 320
const PH = 240

const MODEL = './mediapipe/face_landmarker.task'
const full = (path: string) => new URL(path, location.href).href

// A face she followed long enough to remember: a crop to keep (JPEG) and
// the face lined up for its print (112 by 112 RGB), both base64.
export interface Remembered {
  jpeg: string
  rgb: string
  seconds: number
}

// Opens the camera into the given video element and reports what she
// makes of it until stopped. Faces worth remembering go to remember.
export async function openSight(
  video: HTMLVideoElement,
  report: (seen: Seen) => void,
  remember?: (face: Remembered) => void,
): Promise<{ stop: () => void }> {
  const stream = await navigator.mediaDevices.getUserMedia({ video: { width: 640, height: 480, facingMode: 'user' }, audio: false })
  video.srcObject = stream
  video.muted = true
  video.playsInline = true
  await video.play()

  const worker = new Worker(new URL('./sightWorker.ts', import.meta.url), { type: 'module' })
  worker.postMessage({ kind: 'start', urls: { loader: full(wasmLoaderPath), wasm: full(wasmBinaryPath), model: full(MODEL) } })
  let stopped = false
  let busy = false
  type Said =
    | { kind: 'ready' }
    | { kind: 'failed'; why: string }
    | { kind: 'seen'; seen: Seen & { wantsFace?: boolean } }
    | { kind: 'face'; face: Remembered }
  worker.onmessage = (e: MessageEvent<Said>) => {
    const m = e.data
    if (m.kind === 'failed') console.warn(`She cannot find faces, only movement: ${m.why}`)
    if (m.kind === 'face') {
      if (!stopped) remember?.(m.face)
      return
    }
    if (m.kind !== 'seen') return
    busy = false
    if (stopped) return
    const { wantsFace, ...seen } = m.seen
    report(seen)
    // The face is looked at again in the full picture, for a crop sharp
    // enough to know someone by.
    if (wantsFace && remember) {
      const t = performance.now()
      createImageBitmap(video)
        .then((picture) => {
          if (stopped) return picture.close()
          worker.postMessage({ kind: 'face', picture, t }, [picture])
        })
        .catch(() => {})
    }
  }

  const timer = window.setInterval(() => {
    if (stopped || busy || video.readyState < 2 || document.hidden) return
    busy = true
    createImageBitmap(video, { resizeWidth: PW, resizeHeight: PH, resizeQuality: 'low' })
      .then((picture) => {
        if (stopped) return picture.close()
        worker.postMessage({ kind: 'picture', picture, t: performance.now() }, [picture])
      })
      .catch(() => {
        busy = false
      })
  }, EVERY_MS)

  return {
    stop() {
      stopped = true
      window.clearInterval(timer)
      worker.terminate()
      for (const track of stream.getTracks()) track.stop()
      video.srcObject = null
    },
  }
}
