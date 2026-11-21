import * as THREE from 'three'
import { FBXLoader } from 'three/examples/jsm/loaders/FBXLoader.js'
import type { VRM } from '@pixiv/three-vrm'

// Mixamo rig bone name -> VRM humanoid bone name. Body bones share names across
// VRM 0.x and 1.0; fingers are best-effort (skipped if the VRM lacks them).
const mixamoVRMRigMap: Record<string, string> = {
  mixamorigHips: 'hips',
  mixamorigSpine: 'spine',
  mixamorigSpine1: 'chest',
  mixamorigSpine2: 'upperChest',
  mixamorigNeck: 'neck',
  mixamorigHead: 'head',
  mixamorigLeftShoulder: 'leftShoulder',
  mixamorigLeftArm: 'leftUpperArm',
  mixamorigLeftForeArm: 'leftLowerArm',
  mixamorigLeftHand: 'leftHand',
  mixamorigLeftHandThumb1: 'leftThumbProximal',
  mixamorigLeftHandThumb2: 'leftThumbIntermediate',
  mixamorigLeftHandThumb3: 'leftThumbDistal',
  mixamorigLeftHandIndex1: 'leftIndexProximal',
  mixamorigLeftHandIndex2: 'leftIndexIntermediate',
  mixamorigLeftHandIndex3: 'leftIndexDistal',
  mixamorigLeftHandMiddle1: 'leftMiddleProximal',
  mixamorigLeftHandMiddle2: 'leftMiddleIntermediate',
  mixamorigLeftHandMiddle3: 'leftMiddleDistal',
  mixamorigLeftHandRing1: 'leftRingProximal',
  mixamorigLeftHandRing2: 'leftRingIntermediate',
  mixamorigLeftHandRing3: 'leftRingDistal',
  mixamorigLeftHandPinky1: 'leftLittleProximal',
  mixamorigLeftHandPinky2: 'leftLittleIntermediate',
  mixamorigLeftHandPinky3: 'leftLittleDistal',
  mixamorigRightShoulder: 'rightShoulder',
  mixamorigRightArm: 'rightUpperArm',
  mixamorigRightForeArm: 'rightLowerArm',
  mixamorigRightHand: 'rightHand',
  mixamorigRightHandThumb1: 'rightThumbProximal',
  mixamorigRightHandThumb2: 'rightThumbIntermediate',
  mixamorigRightHandThumb3: 'rightThumbDistal',
  mixamorigRightHandIndex1: 'rightIndexProximal',
  mixamorigRightHandIndex2: 'rightIndexIntermediate',
  mixamorigRightHandIndex3: 'rightIndexDistal',
  mixamorigRightHandMiddle1: 'rightMiddleProximal',
  mixamorigRightHandMiddle2: 'rightMiddleIntermediate',
  mixamorigRightHandMiddle3: 'rightMiddleDistal',
  mixamorigRightHandRing1: 'rightRingProximal',
  mixamorigRightHandRing2: 'rightRingIntermediate',
  mixamorigRightHandRing3: 'rightRingDistal',
  mixamorigRightHandPinky1: 'rightLittleProximal',
  mixamorigRightHandPinky2: 'rightLittleIntermediate',
  mixamorigRightHandPinky3: 'rightLittleDistal',
  mixamorigLeftUpLeg: 'leftUpperLeg',
  mixamorigLeftLeg: 'leftLowerLeg',
  mixamorigLeftFoot: 'leftFoot',
  mixamorigLeftToeBase: 'leftToes',
  mixamorigRightUpLeg: 'rightUpperLeg',
  mixamorigRightLeg: 'rightLowerLeg',
  mixamorigRightFoot: 'rightFoot',
  mixamorigRightToeBase: 'rightToes',
}

// Load a Mixamo FBX and retarget it onto a VRM humanoid, returning an
// AnimationClip bound to the VRM's normalized bones. Port of the official
// @pixiv/three-vrm loadMixamoAnimation example (handles VRM 0.x mirroring and
// hip-height scaling so clips fit avatars of any size).
export async function loadMixamoAnimation(url: string, vrm: VRM): Promise<THREE.AnimationClip> {
  const loader = new FBXLoader()
  const asset = await loader.loadAsync(url)

  const clip = THREE.AnimationClip.findByName(asset.animations, 'mixamo.com') ?? asset.animations[0]
  const tracks: THREE.KeyframeTrack[] = []

  const restRotationInverse = new THREE.Quaternion()
  const parentRestWorldRotation = new THREE.Quaternion()
  const _quatA = new THREE.Quaternion()
  const _vec3 = new THREE.Vector3()

  const motionHips = asset.getObjectByName('mixamorigHips')
  const vrmHipsNode = vrm.humanoid?.getNormalizedBoneNode('hips')
  if (!motionHips || !vrmHipsNode) return new THREE.AnimationClip('vrmAnimation', clip.duration, tracks)

  const motionHipsHeight = motionHips.position.y
  const vrmHipsY = vrmHipsNode.getWorldPosition(_vec3).y
  const vrmRootY = vrm.scene.getWorldPosition(_vec3).y
  const vrmHipsHeight = Math.abs(vrmHipsY - vrmRootY)
  const hipsPositionScale = motionHipsHeight === 0 ? 1 : vrmHipsHeight / motionHipsHeight

  const isVRM0 = vrm.meta?.metaVersion === '0'

  clip.tracks.forEach((track) => {
    const [mixamoRigName, propertyName] = track.name.split('.')
    const vrmBoneName = mixamoVRMRigMap[mixamoRigName]
    const vrmNodeName = vrmBoneName ? vrm.humanoid?.getNormalizedBoneNode(vrmBoneName as never)?.name : undefined
    const mixamoRigNode = asset.getObjectByName(mixamoRigName)
    if (!vrmNodeName || !mixamoRigNode?.parent) return

    mixamoRigNode.getWorldQuaternion(restRotationInverse).invert()
    mixamoRigNode.parent.getWorldQuaternion(parentRestWorldRotation)

    if (track instanceof THREE.QuaternionKeyframeTrack) {
      const values = Array.from(track.values)
      for (let i = 0; i < values.length; i += 4) {
        _quatA.fromArray(values, i)
        _quatA.premultiply(parentRestWorldRotation).multiply(restRotationInverse)
        _quatA.toArray(values, i)
      }
      tracks.push(new THREE.QuaternionKeyframeTrack(
        `${vrmNodeName}.${propertyName}`,
        Array.from(track.times),
        values.map((v, i) => (isVRM0 && i % 2 === 0 ? -v : v)),
      ))
    } else if (track instanceof THREE.VectorKeyframeTrack) {
      const values = Array.from(track.values).map((v, i) => (isVRM0 && i % 3 !== 1 ? -v : v) * hipsPositionScale)
      tracks.push(new THREE.VectorKeyframeTrack(`${vrmNodeName}.${propertyName}`, Array.from(track.times), values))
    }
  })

  return new THREE.AnimationClip('vrmAnimation', clip.duration, tracks)
}
