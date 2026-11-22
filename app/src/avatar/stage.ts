import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { type VRM, VRMExpression, VRMExpressionMorphTargetBind, type VRMHumanBoneName, VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm'
import { loadMixamoAnimation } from './mixamo'
import { type Look, type Manner, PARTS, type Part, usual } from './feelings'
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
  // Her face, from the parts of her model, and her manner (feelings.ts).
  look: Look
  manner: Manner
  // 0 to 1. Low energy slows her down and lowers her head a little.
  energy: number
}

const CLIPS: Record<string, string> = {
  idle: './animations/idle.fbx',
  thinking: './animations/thinking.fbx',
  'look-around': './animations/look-around.fbx',
}
const FADE = 0.6
// Thirty frames a second is plenty for a talking figure, and leaves the
// graphics card to the language models.
const FRAME = 1 / 30

const ease = (cur: number, target: number, rate: number, dt: number) => cur + (target - cur) * Math.min(1, rate * dt)
// A critically damped spring: it speeds up and slows down into place, as a
// head does, where easing starts fast and creeps in. Higher freq is quicker.
interface Spring { x: number; v: number }
const spring = (sp: Spring, target: number, freq: number, dt: number) => {
  sp.v += (freq * freq * (target - sp.x) - 2 * freq * sp.v) * dt
  sp.x += sp.v * dt
  return sp.x
}
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
  private face: Face = { look: {}, manner: usual, energy: 0.5 }
  // Her manner as shown, eased so a change of feeling settles in.
  private manner: Manner = { ...usual }
  // The parts her model has, of those feelings use.
  private parts: Part[] = []
  private mouth: Mouth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 }
  private shown: Mouth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 }
  private expr: Record<string, number> = {}
  private saved = new Map<THREE.Object3D, THREE.Quaternion>()
  private pointer = { x: 0, y: 0, at: -10 }
  // What her camera shows, when it is on; null when it is off.
  private seen: Seen | null = null
  // The copied face as shown, eased so it does not twitch.
  private mirror: Copied = { smile: 0, blinkLeft: 0, blinkRight: 0, browUp: 0, frown: 0, browInner: 0, wide: 0, down: 0, jawOpen: 0, funnel: 0, pucker: 0, stretch: 0, squint: 0, yaw: 0, pitch: 0, roll: 0 }
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
    // Tiny shifts of the eyes while they rest on something, as living eyes
    // never hold quite still.
    microX: 0, microY: 0, nextMicro: 0.4,
    lastGX: 0, lastGY: 0,
  }
  // Her head follows her eyes, a beat behind and not all the way: the eyes
  // jump, the head turns after them and shares the angle.
  private headX: Spring = { x: 0, v: 0 }
  private headY: Spring = { x: 0, v: 0 }
  private bodyX: Spring = { x: 0, v: 0 }
  private copyYaw: Spring = { x: 0, v: 0 }
  private copyPitch: Spring = { x: 0, v: 0 }
  private copyRoll: Spring = { x: 0, v: 0 }
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
      this.addParts(vrm)
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

  // Each brow, eye and mouth shape of her model becomes an expression of
  // its own, so feelings can mix them rather than wear one stock face.
  private addParts(vrm: VRM) {
    const em = vrm.expressionManager
    if (!em) return
    for (const part of PARTS) {
      const e = new VRMExpression(`part:${part}`)
      vrm.scene.traverse((o) => {
        const mesh = o as THREE.Mesh
        const index = mesh.isMesh ? mesh.morphTargetDictionary?.[part] : undefined
        if (index !== undefined) e.addBind(new VRMExpressionMorphTargetBind({ primitives: [mesh], index, weight: 1 }))
      })
      if (e.binds.length === 0) continue
      em.registerExpression(e)
      this.parts.push(part)
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
    const fm = this.face.manner
    const mn = this.manner
    for (const k of Object.keys(mn) as (keyof Manner)[]) mn[k] = ease(mn[k], fm[k], 1.2, dt)
    s.nextLook -= dt
    if (s.nextLook <= 0) {
      // Uneasy, her eyes leave you more often; warm, they stay.
      const away = Math.random() < Math.min(0.85, (talking ? 0.25 : 0.45) * mn.away)
      s.toX = away ? rand(-0.28, 0.28) : rand(-0.04, 0.04)
      s.toY = away ? rand(-0.1, 0.14) : rand(-0.03, 0.03)
      s.nextLook = away ? rand(0.5, 1.4) : rand(1.6, 4.5)
      if (away && Math.random() < 0.5) s.blink = 1
    }
    let gx = s.toX
    let gy = s.toY
    gy -= mn.down
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
    const seeing = watching !== 'pointer'
    // Resting on something, the eyes make small jumps around it.
    s.nextMicro -= dt
    if (s.nextMicro <= 0) {
      s.microX = seeing ? rand(-0.018, 0.018) : 0
      s.microY = seeing ? rand(-0.01, 0.01) : 0
      s.nextMicro = rand(0.25, 0.9)
    }
    gx += s.microX
    gy += s.microY
    // A big shift of gaze often comes with a blink.
    if (Math.hypot(gx - s.lastGX, gy - s.lastGY) > 0.22 && Math.random() < 0.5) s.blink = 1
    s.lastGX = gx
    s.lastGY = gy
    // The eyes jump; the head and then the body come round after them.
    s.lookX = ease(s.lookX, gx, 20, dt)
    s.lookY = ease(s.lookY, gy, 20, dt)
    const hx = spring(this.headX, gx, 7 * pace, dt)
    const hy = spring(this.headY, gy, 7 * pace, dt)
    const bx = spring(this.bodyX, watching === 'motion' ? gx : 0, 2.5, dt)
    const cy = spring(this.copyYaw, mr.yaw * c, 9, dt)
    const cp = spring(this.copyPitch, mr.pitch * c, 9, dt)
    const cr = spring(this.copyRoll, mr.roll * c, 9, dt)
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
    // Following something across the room, her body turns a little too.
    this.turn('spine', s.asleep * 0.06, Math.sin(t * 0.33) * 0.012 + bx * 0.12, 0)
    this.turn('neck',
      droop * 0.4 - hy * 0.25 + s.nod * 0.03 + cp * 0.3,
      hx * 0.35 + cy * 0.3,
      s.asleep * 0.06 + cr * 0.3)
    this.turn('head',
      droop * 0.6 - hy * 0.35 + s.nod * 0.06 * Math.sin(t * 5.2) + Math.sin(t * 0.7) * 0.008 + cp * 0.55 + mn.chin * (1 - c),
      hx * 0.5 + (talking ? Math.sin(t * 1.3) * 0.03 * (0.4 + s.loud) : 0) + cy * 0.55,
      s.asleep * 0.1 + Math.sin(t * 0.45) * 0.01 + (talking ? Math.sin(t * 0.9) * 0.025 * s.loud : 0) + cr * 0.6 + mn.tilt * (1 - c))

    // ----- face -----
    const em = vrm.expressionManager
    if (em) {
      s.nextBlink -= dt
      if (s.nextBlink <= 0) {
        s.blink = 1
        // Sometimes twice, as people do.
        s.again = !s.again && Math.random() < 0.18
        s.nextBlink = s.again ? 0.22 : rand(2.4, 6) / ((0.6 + this.face.energy * 0.8) * this.manner.blinks)
      }
      s.blink = Math.max(0, s.blink - dt * 8)
      // Copying, your blinks are hers, eye by eye; her own fade away.
      em.setValue('blink', Math.max(s.blink * (1 - c), s.asleep))
      em.setValue('blinkLeft', mr.blinkLeft * c)
      em.setValue('blinkRight', mr.blinkRight * c)
      // While she speaks her mouth stays with her words; otherwise it
      // opens as yours does.
      const quiet = c * (1 - Math.min(1, loud * 3))
      const yourMouth: Mouth = {
        aa: mr.jawOpen * (1 - mr.funnel * 0.5),
        oh: mr.funnel * 0.9,
        ou: mr.pucker * 0.8,
        ee: mr.stretch * 0.6,
        ih: mr.stretch * 0.3,
      }
      for (const k of MOUTH) em.setValue(k, Math.max(this.shown[k], yourMouth[k] * quiet))
      // Her mood shows, mixed from the parts of her face. Copying, your
      // face leads and hers stays faintly underneath. A wide mouth while
      // speaking would fight a smile, so the mouth gives way to speech.
      const look = this.face.look
      const yours: Look = c > 0.01 ? {
        Fcl_MTH_Fun: mr.smile * (1 - mr.jawOpen),
        Fcl_MTH_Joy: mr.smile * mr.jawOpen * 1.2,
        Fcl_EYE_Fun: mr.smile * 0.3 + mr.squint * 0.5,
        Fcl_BRW_Fun: mr.smile * 0.2,
        Fcl_BRW_Surprised: mr.browUp * 0.9,
        Fcl_BRW_Sorrow: Math.max(0, mr.browInner - mr.browUp) * 1.1,
        Fcl_EYE_Spread: mr.wide * 0.8,
        Fcl_BRW_Angry: mr.frown * 0.8,
        Fcl_EYE_Angry: mr.frown * 0.3,
        Fcl_MTH_Sorrow: mr.down * 0.8,
      } : {}
      this.parts.forEach((p, i) => {
        // A felt face is never held perfectly still; each part drifts a
        // little, out of step with the others.
        const life = 1 + Math.sin(t * (0.35 + i * 0.037) + i * 1.7) * 0.1
        let target = Math.min(1, (look[p] ?? 0) * life * (1 - c * 0.8) + (yours[p] ?? 0) * c)
        target *= 1 - s.asleep
        if (p.startsWith('Fcl_MTH')) target *= 1 - Math.min(1, loud * 1.5)
        const cur = this.expr[p] ?? 0
        // A feeling comes on quicker than it fades; copying keeps up with you.
        const rate = c > 0.3 ? 14 : target > cur ? 2.5 : 1.1
        const next = ease(cur, target, rate, dt)
        this.expr[p] = next
        if (next > 0.001 || cur > 0.001) em.setValue(`part:${p}`, next)
      })
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
