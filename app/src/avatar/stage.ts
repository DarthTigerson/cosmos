import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { type VRM, type VRMHumanBoneName, VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm'
import { loadMixamoAnimation } from './mixamo'
import type { Copied, Seen } from './sight'

// The stage draws her 3D model and keeps her alive between words.
//
// The motion clips (idle, thinking, look around) set the pose of the whole
// body. On top of them, every frame, a second layer adds what makes a
// figure read as present: breathing, eyes that find you, blinks, a head
// that moves with her speech, a face that follows her mood. The clips
// alone looked like a looping mannequin; the layer is where the life is.

export type Pose = 'idle' | 'thinking' | 'talking' | 'asleep'

// The mouth, as the five shapes her model has, each 0 to 1. All zero
// is lips together.
export interface Mouth {
  aa: number // open, as in "father"
  ih: number // a little open, teeth showing, as in "sit"
  ou: number // small and round, as in "you"
  ee: number // wide, as in "see"
  oh: number // round and open, as in "go"
}

const MOUTH = ['aa', 'ih', 'ou', 'ee', 'oh'] as const

export interface Face {
  // One of the model's expression presets, and how strongly to wear it.
  expression: 'neutral' | 'happy' | 'angry' | 'sad' | 'relaxed' | 'surprised'
  intensity: number
  // 0 to 1. A warm mood leaves a trace of a smile even when calm.
  warmth: number
  // 0 to 1. Low energy slows her down and lowers her head a little.
  energy: number
}

const CLIPS: Record<string, string> = {
  idle: './animations/idle.fbx',
  thinking: './animations/thinking.fbx',
  'look-around': './animations/look-around.fbx',
}
const PRESETS = ['happy', 'angry', 'sad', 'relaxed', 'surprised'] as const
const FADE = 0.6
// Thirty frames a second is plenty for a talking figure, and leaves the
// graphics card to the language models.
const FRAME = 1 / 30

const ease = (cur: number, target: number, rate: number, dt: number) => cur + (target - cur) * Math.min(1, rate * dt)
const rand = (a: number, b: number) => a + Math.random() * (b - a)

export class Stage {
  private renderer: THREE.WebGLRenderer
  private scene = new THREE.Scene()
  private camera = new THREE.PerspectiveCamera(26, 1, 0.1, 20)
  private vrm: VRM | null = null
  private mixer: THREE.AnimationMixer | null = null
  private clips: Record<string, THREE.AnimationClip> = {}
  private action: THREE.AnimationAction | null = null
  private gaze = new THREE.Object3D()
  private pose: Pose = 'idle'
  private face: Face = { expression: 'neutral', intensity: 0, warmth: 0.6, energy: 0.5 }
  private mouth: Mouth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 }
  private shown: Mouth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 }
  private expr: Record<string, number> = {}
  private saved = new Map<THREE.Object3D, THREE.Quaternion>()
  private pointer = { x: 0, y: 0, at: -10 }
  // What her camera shows, when it is on; null when it is off.
  private seen: Seen | null = null
  // The copied face as shown, eased so it does not twitch.
  private mirror: Copied = { smile: 0, blinkLeft: 0, blinkRight: 0, browUp: 0, frown: 0, jawOpen: 0, yaw: 0, pitch: 0, roll: 0 }
  private copying = 0
  private t = 0
  private since = 0
  private last = 0
  private raf = 0
  private disposed = false
  private s = {
    blink: 0, nextBlink: 2, again: false,
    lookX: 0, lookY: 0, toX: 0, toY: 0, nextLook: 1.5,
    nod: 0, loud: 0, thinkingFor: 0, nextBreak: rand(20, 40), breakFor: 0,
    asleep: 0,
  }
  private resize: ResizeObserver
  onReady: () => void = () => {}
  onError: (message: string) => void = () => {}

  constructor(private canvas: HTMLCanvasElement) {
    this.renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true, powerPreference: 'low-power' })
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    this.renderer.outputColorSpace = THREE.SRGBColorSpace
    // A soft sky light, a warm key from the front left, a cool rim from
    // behind: the lighting of her portrait.
    this.scene.add(new THREE.HemisphereLight(0xdfe8ff, 0x1a2033, 1.1))
    const key = new THREE.DirectionalLight(0xfff1dc, 1.9)
    key.position.set(-1.2, 1.6, 2.2)
    const rim = new THREE.DirectionalLight(0x7ab8e0, 1.4)
    rim.position.set(1.4, 1.2, -2)
    this.scene.add(key, rim, this.gaze)
    this.resize = new ResizeObserver(() => this.fit())
    this.resize.observe(canvas.parentElement ?? canvas)
    this.fit()
    this.raf = requestAnimationFrame(this.tick)
  }

  private fit() {
    const box = (this.canvas.parentElement ?? this.canvas).getBoundingClientRect()
    if (box.width < 2 || box.height < 2) return
    this.renderer.setSize(box.width, box.height, false)
    this.camera.aspect = box.width / box.height
    this.camera.updateProjectionMatrix()
  }

  async load(url: string) {
    try {
      const loader = new GLTFLoader()
      loader.register((parser) => new VRMLoaderPlugin(parser))
      const gltf = await loader.loadAsync(url)
      if (this.disposed) return
      const vrm = gltf.userData.vrm as VRM
      VRMUtils.rotateVRM0(vrm)
      vrm.scene.traverse((o) => { o.frustumCulled = false })
      this.scene.add(vrm.scene)
      this.vrm = vrm
      this.mixer = new THREE.AnimationMixer(vrm.scene)
      if (vrm.lookAt) vrm.lookAt.target = this.gaze
      this.frame()
      // The idle clip first, so she is never seen in the arms-out rest
      // pose; the others can arrive when they arrive.
      await this.clip('idle')
      this.play('idle')
      this.onReady()
      void this.clip('thinking')
      void this.clip('look-around')
    } catch (e) {
      this.onError(`Her model could not be loaded: ${(e as Error).message}`)
    }
  }

  // Head and shoulders, at her eye level; close up, only her face.
  private close = false
  private frame() {
    const head = this.bone('head')
    const y = head ? head.getWorldPosition(new THREE.Vector3()).y : 1.4
    this.camera.fov = this.close ? 11 : 26
    this.camera.updateProjectionMatrix()
    this.camera.position.set(0, y - 0.02, 1.55)
    this.camera.lookAt(0, this.close ? y + 0.02 : y - 0.07, 0)
    this.gaze.position.set(0, y, 1.5)
  }

  // For the small round avatar: her face fills the frame.
  setClose(close: boolean) {
    this.close = close
    this.frame()
  }

  private bone(name: VRMHumanBoneName) {
    return this.vrm?.humanoid?.getNormalizedBoneNode(name) ?? null
  }

  private async clip(key: string): Promise<THREE.AnimationClip | null> {
    if (this.clips[key]) return this.clips[key]
    if (!this.vrm || !CLIPS[key]) return null
    try {
      const c = await loadMixamoAnimation(CLIPS[key], this.vrm)
      // The clips walk the hips about; she stays where she is.
      c.tracks = c.tracks.filter((tr) => !tr.name.endsWith('.position'))
      this.clips[key] = c
      return c
    } catch {
      return null
    }
  }

  private play(key: string, once = false) {
    const c = this.clips[key]
    if (!c || !this.mixer) return false
    const next = this.mixer.clipAction(c)
    if (next === this.action) return true
    next.reset()
    next.setLoop(once ? THREE.LoopOnce : THREE.LoopRepeat, once ? 1 : Infinity)
    next.clampWhenFinished = once
    next.fadeIn(FADE).play()
    this.action?.fadeOut(FADE)
    this.action = next
    return true
  }

  setPose(pose: Pose) {
    if (pose === this.pose) return
    this.pose = pose
    this.since = 0
    this.s.thinkingFor = 0
    this.s.breakFor = 0
    if (pose !== 'thinking') this.play('idle')
  }

  setFace(face: Face) { this.face = face }
  setMouth(mouth: Mouth) { this.mouth = mouth }

  // What her camera shows. While it shows something she watches that, and
  // the pointer is left alone.
  see(seen: Seen | null) { this.seen = seen }

  // Where the pointer is over the window, -1 to 1 each way. She glances
  // at it for a moment when it moves.
  look(x: number, y: number) {
    this.pointer = { x, y, at: this.t }
  }

  private tick = (now: number) => {
    if (this.disposed) return
    this.raf = requestAnimationFrame(this.tick)
    const dt = Math.min(0.1, (now - this.last) / 1000)
    if (dt < FRAME) return
    this.last = now
    if (document.hidden) return
    this.update(dt)
    this.renderer.render(this.scene, this.camera)
  }

  // turn adds a small rotation to a bone on top of what the clip gave it.
  // The clip's own value is put back before the next frame, so these
  // never pile up.
  private turn(name: VRMHumanBoneName, x: number, y: number, z: number) {
    const b = this.bone(name)
    if (!b) return
    if (!this.saved.has(b)) this.saved.set(b, new THREE.Quaternion())
    this.saved.get(b)!.copy(b.quaternion)
    // A VRM 0 model's normalized bones tip the other way about x and z,
    // so up would be down and a lean left would lean right.
    const flip = this.vrm?.meta?.metaVersion === '0' ? -1 : 1
    b.quaternion.multiply(new THREE.Quaternion().setFromEuler(new THREE.Euler(x * flip, y, z * flip)))
  }

  private update(dt: number) {
    const vrm = this.vrm
    if (!vrm) return
    const s = this.s
    this.t += dt
    this.since += dt
    const t = this.t
    const asleep = this.pose === 'asleep'
    const talking = this.pose === 'talking'
    const thinking = this.pose === 'thinking'
    s.asleep = ease(s.asleep, asleep ? 1 : 0, 1.5, dt)
    // A tired or sleeping figure moves slowly.
    const pace = (0.75 + this.face.energy * 0.5) * (1 - s.asleep * 0.6)

    // Thinking takes its pose only once a reply is taking a while; a
    // quick answer should not make her strike one.
    if (thinking) {
      s.thinkingFor += dt
      if (s.thinkingFor > 1.2) this.play('thinking')
    }
    // Now and then, idle, she looks about.
    if (this.pose === 'idle') {
      if (s.breakFor > 0) {
        s.breakFor -= dt
        if (s.breakFor <= 0) this.play('idle')
      } else {
        s.nextBreak -= dt
        if (s.nextBreak <= 0 && this.clips['look-around']) {
          this.play('look-around', true)
          s.breakFor = this.clips['look-around'].duration
          s.nextBreak = rand(35, 70)
        }
      }
    }

    // Put the bones back as the clip left them, then let the clip move on.
    this.saved.forEach((q, b) => b.quaternion.copy(q))
    if (this.action) this.action.timeScale = pace
    this.mixer?.update(dt)

    // ----- eyes -----
    // She looks at you. Every few seconds her eyes drift a little and
    // come back; while thinking they go up and away; a moving pointer
    // draws a glance.
    s.nextLook -= dt
    if (s.nextLook <= 0) {
      const away = Math.random() < (talking ? 0.25 : 0.45)
      s.toX = away ? rand(-0.28, 0.28) : rand(-0.04, 0.04)
      s.toY = away ? rand(-0.1, 0.14) : rand(-0.03, 0.03)
      s.nextLook = away ? rand(0.5, 1.4) : rand(1.6, 4.5)
      if (away && Math.random() < 0.5) s.blink = 1
    }
    let gx = s.toX
    let gy = s.toY
    if (thinking && s.thinkingFor > 0.6) { gx = 0.35; gy = 0.3 }
    const watching = this.seen?.watching ?? 'pointer'
    if (watching === 'motion') { gx = this.seen!.x * 0.5; gy = -this.seen!.y * 0.3 }
    else if (watching === 'copy') { gx = this.seen!.x * 0.2; gy = -this.seen!.y * 0.12 }
    else if (t - this.pointer.at < 1.8) { gx = this.pointer.x * 0.5; gy = -this.pointer.y * 0.3 }
    // Copying a face fades in and out rather than snapping.
    this.copying = ease(this.copying, watching === 'copy' && this.seen?.face && !asleep ? 1 : 0, 4, dt)
    const want = this.seen?.face
    for (const k of Object.keys(this.mirror) as (keyof Copied)[]) this.mirror[k] = ease(this.mirror[k], want ? want[k] : 0, 12, dt)
    const mr = this.mirror
    const c = this.copying
    s.lookX = ease(s.lookX, gx, 7, dt)
    s.lookY = ease(s.lookY, gy, 7, dt)
    const head = this.bone('head')
    const eye = head ? head.getWorldPosition(new THREE.Vector3()) : new THREE.Vector3(0, 1.4, 0)
    this.gaze.position.set(eye.x + s.lookX, eye.y + s.lookY - s.asleep * 0.6, 1.5)

    // ----- voice -----
    // The mouth follows the sound. It opens fast and closes a little
    // slower, as a jaw does.
    // The shapes arrive already timed to the sound; the easing here only
    // takes the snap out of the change from one to the next.
    let loud = 0
    for (const k of MOUTH) {
      const target = asleep ? 0 : this.mouth[k]
      this.shown[k] = ease(this.shown[k], target, target > this.shown[k] ? 34 : 24, dt)
      loud = Math.max(loud, this.shown[k])
    }
    s.loud = ease(s.loud, loud, 6, dt)
    // Her head moves with the stress of her words.
    s.nod = ease(s.nod, talking ? s.loud : 0, 5, dt)

    // ----- body -----
    const breath = Math.sin(t * 1.5 * pace)
    const droop = s.asleep * 0.42 + (1 - this.face.energy) * 0.05
    this.turn('chest', breath * 0.014, 0, 0)
    this.turn('upperChest', breath * 0.01, 0, 0)
    this.turn('spine', s.asleep * 0.06, Math.sin(t * 0.33) * 0.012, 0)
    this.turn('neck',
      droop * 0.4 - s.lookY * 0.25 + s.nod * 0.03,
      s.lookX * 0.35,
      s.asleep * 0.06)
    this.turn('head',
      droop * 0.6 - s.lookY * 0.35 + s.nod * 0.06 * Math.sin(t * 5.2) + Math.sin(t * 0.7) * 0.008 + mr.pitch * 0.6 * c,
      s.lookX * 0.5 + (talking ? Math.sin(t * 1.3) * 0.03 * (0.4 + s.loud) : 0) + mr.yaw * 0.6 * c,
      s.asleep * 0.1 + Math.sin(t * 0.45) * 0.01 + (talking ? Math.sin(t * 0.9) * 0.025 * s.loud : 0) + mr.roll * 0.7 * c)

    // ----- face -----
    const em = vrm.expressionManager
    if (em) {
      s.nextBlink -= dt
      if (s.nextBlink <= 0) {
        s.blink = 1
        // Sometimes twice, as people do.
        s.again = !s.again && Math.random() < 0.18
        s.nextBlink = s.again ? 0.22 : rand(2.4, 6) / (0.6 + this.face.energy * 0.8)
      }
      s.blink = Math.max(0, s.blink - dt * 8)
      // Copying, your blinks are hers, eye by eye; her own fade away.
      em.setValue('blink', Math.max(s.blink * (1 - c), s.asleep))
      em.setValue('blinkLeft', mr.blinkLeft * c)
      em.setValue('blinkRight', mr.blinkRight * c)
      // While she speaks her mouth stays with her words; otherwise it
      // opens as yours does.
      for (const k of MOUTH) em.setValue(k, k === 'aa' ? Math.max(this.shown.aa, mr.jawOpen * c * (1 - Math.min(1, loud * 3))) : this.shown[k])
      // Her mood shows: the feeling she has now, over a trace of a smile
      // when she is warm. A wide mouth while speaking would fight the
      // smile, so the expression gives way a little to speech.
      const rest = Math.max(0, this.face.warmth - 0.5) * 0.5
      for (const p of PRESETS) {
        let target = p === this.face.expression ? this.face.intensity : 0
        if (p === 'relaxed' && this.face.expression === 'neutral') target = rest
        // Copying, your face leads and her mood stays faintly underneath.
        if (c > 0.01) {
          const yours = p === 'happy' ? mr.smile : p === 'surprised' ? mr.browUp * 0.8 : p === 'angry' ? mr.frown * 0.6 : 0
          target = target * (1 - c * 0.8) + yours * c
        }
        target *= (1 - s.asleep) * (1 - loud * 0.5)
        const cur = this.expr[p] ?? 0
        const next = ease(cur, target, 3, dt)
        this.expr[p] = next
        if (next > 0.001 || cur > 0.001) em.setValue(p, next)
      }
    }
    vrm.update(dt)
  }

  dispose() {
    this.disposed = true
    cancelAnimationFrame(this.raf)
    this.resize.disconnect()
    if (this.vrm) VRMUtils.deepDispose(this.vrm.scene)
    this.renderer.dispose()
  }
}
