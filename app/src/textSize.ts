import { useSyncExternalStore } from 'react'

// Text size for the whole window, shown in the footer. It scales the page
// rather than each rule's font-size, so spacing grows with the text. The
// number is the body text size in pixels: 15 is the size Cosmos is drawn at.
export const DEFAULT_SIZE = 15
const MIN = 11
const MAX = 22
const KEY = 'cosmos:textSize'

function stored(): number {
  try {
    const n = Number(localStorage.getItem(KEY) || DEFAULT_SIZE)
    return n >= MIN && n <= MAX ? n : DEFAULT_SIZE
  } catch {
    return DEFAULT_SIZE
  }
}

let size = stored()
const listeners = new Set<() => void>()

// In a plain browser (while developing) there is no Electron to zoom the
// page, so CSS zoom stands in.
function apply(n: number) {
  if (window.cosmos?.setZoom) window.cosmos.setZoom(n / DEFAULT_SIZE)
  else document.documentElement.style.zoom = String(n / DEFAULT_SIZE)
}

function set(n: number) {
  const next = Math.min(MAX, Math.max(MIN, n))
  if (next === size) return
  size = next
  apply(size)
  try {
    localStorage.setItem(KEY, String(size))
  } catch { /* the size still applies until the window closes */ }
  listeners.forEach((l) => l())
}

export const textSize = {
  larger: () => set(size + 1),
  smaller: () => set(size - 1),
  reset: () => set(DEFAULT_SIZE),
  min: MIN,
  max: MAX,
}

apply(size)
// ⌘⇧+ / ⌘⇧− / ⌘⇧0 come from the app menu (electron/main.cjs).
window.cosmos?.onTextSize?.((dir) => textSize[dir]())

export function useTextSize(): number {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => size,
  )
}
