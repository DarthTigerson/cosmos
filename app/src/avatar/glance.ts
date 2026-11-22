// A glance: one still from her camera, when she is asked to look. The
// camera's own picture never leaves this window except as this still,
// sent to the core on this computer with the message it belongs to.

let source: HTMLVideoElement | null = null

// watchFrom is called by her figure while the camera is on, and with null
// when it goes off.
export function watchFrom(video: HTMLVideoElement | null) {
  source = video
}

// Words that ask her to look at something.
const asks = /\b(look|looking|see|seeing|watch|can you see|show(ing)? you|this is (my|our)|meet (my|our)|check (this|him|her|it|them) out|who('s| is) this|what('s| is) this)\b/i

export function asksToLook(words: string): boolean {
  return asks.test(words)
}

// glance returns what the camera sees now as a JPEG data: address, at
// most 768 pixels on its long side, or null when the camera is off.
export function glance(): string | null {
  const v = source
  if (!v || v.readyState < 2 || !v.videoWidth) return null
  const scale = Math.min(1, 768 / Math.max(v.videoWidth, v.videoHeight))
  const c = document.createElement('canvas')
  c.width = Math.round(v.videoWidth * scale)
  c.height = Math.round(v.videoHeight * scale)
  c.getContext('2d')!.drawImage(v, 0, 0, c.width, c.height)
  return c.toDataURL('image/jpeg', 0.82)
}
