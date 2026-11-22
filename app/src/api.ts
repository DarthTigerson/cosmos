import { useCallback, useEffect, useRef, useState } from 'react'
import type { BatteryStatus, MemoryBreakdown, MemoryUsage, Signal } from './types'

interface Conn {
  baseUrl: string
  token: string
}

declare global {
  interface Window {
    cosmos?: {
      conn(): Promise<Conn>
      memory(): Promise<MemoryUsage>
      memoryBreakdown(): Promise<MemoryBreakdown>
      battery(): Promise<BatteryStatus | null>
      copy?(text: string): Promise<void>
      setZoom(factor: number): void
      onTextSize(handler: (dir: 'larger' | 'smaller' | 'reset') => void): () => void
      onToggleNav?(handler: () => void): () => void
    }
  }
}

let connPromise: Promise<Conn> | null = null

// copyText puts text on the clipboard: through the app in Electron, where
// the page itself may not touch the clipboard, or the browser's own way
// in a plain browser.
export async function copyText(text: string): Promise<void> {
  if (window.cosmos?.copy) return window.cosmos.copy(text)
  await navigator.clipboard.writeText(text)
}

// In Electron the preload script says where the core is. In a plain
// browser (handy while developing) it comes from ?core=…&token=….
function conn(): Promise<Conn> {
  if (!connPromise) {
    if (window.cosmos) {
      connPromise = window.cosmos.conn()
    } else {
      const q = new URLSearchParams(location.search)
      connPromise = Promise.resolve({ baseUrl: q.get('core') ?? 'http://127.0.0.1:7717', token: q.get('token') ?? '' })
    }
  }
  return connPromise
}

export async function api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const c = await conn()
  let res: Response
  try {
    res = await fetch(c.baseUrl + path, {
      method: init?.method ?? 'GET',
      headers: { Authorization: `Bearer ${c.token}`, ...(init?.body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
      body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    })
  } catch {
    throw new Error('The core is not answering. Is it still running?')
  }
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new Error(data?.error ?? `The core answered ${res.status}.`)
  return data as T
}

// apiAudio asks the core for sound and returns the raw bytes.
export async function apiAudio(path: string, body: unknown): Promise<ArrayBuffer> {
  const c = await conn()
  let res: Response
  try {
    res = await fetch(c.baseUrl + path, { method: 'POST', headers: { Authorization: `Bearer ${c.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  } catch {
    throw new Error('The core is not answering. Is it still running?')
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new Error(data?.error ?? `The core answered ${res.status}.`)
  }
  return res.arrayBuffer()
}

// apiRecording sends a recording to the core, as for a voiceprint.
export async function apiRecording(path: string, wav: ArrayBuffer): Promise<void> {
  const c = await conn()
  let res: Response
  try {
    res = await fetch(c.baseUrl + path, { method: 'POST', headers: { Authorization: `Bearer ${c.token}`, 'Content-Type': 'audio/wav' }, body: wav })
  } catch {
    throw new Error('The core is not answering. Is it still running?')
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new Error(data?.error ?? `The core answered ${res.status}.`)
  }
}

// socketAddress is where to open a WebSocket to the core. A WebSocket
// cannot send the token as a header either.
export async function socketAddress(path: string): Promise<string> {
  const c = await conn()
  return `${c.baseUrl.replace(/^http/, 'ws')}${path}?token=${encodeURIComponent(c.token)}`
}

// useAsset gives an address an <img> can load from the core. An image
// tag cannot send the token as a header, so it goes in the address.
export function useAsset(path: string): string {
  const [url, setUrl] = useState('')
  useEffect(() => {
    let live = true
    void conn().then((c) => {
      if (live) setUrl(`${c.baseUrl}${path}?token=${encodeURIComponent(c.token)}`)
    })
    return () => {
      live = false
    }
  }, [path])
  return url
}

type Listener = (s: Signal) => void
const listeners = new Set<Listener>()
let opened = false

// One stream for the whole app. The flag is set before anything is awaited:
// several components subscribe at the same moment on start-up, and each
// would otherwise open its own connection and every signal would arrive
// several times.
async function openStream() {
  if (opened) return
  opened = true
  const c = await conn()
  const source = new EventSource(`${c.baseUrl}/api/events?token=${encodeURIComponent(c.token)}`)
  source.onmessage = (e) => {
    try {
      const signal = JSON.parse(e.data) as Signal
      listeners.forEach((l) => l(signal))
    } catch { /* ignore a malformed signal */ }
  }
}

// useSignals calls the handler for every signal from the core while the
// component is mounted. One connection is shared by the whole app.
export function useSignals(handler: Listener) {
  const ref = useRef(handler)
  ref.current = handler
  useEffect(() => {
    const l: Listener = (s) => ref.current(s)
    listeners.add(l)
    void openStream()
    return () => {
      listeners.delete(l)
    }
  }, [])
}

// useLoad fetches a path and gives back the data, any error, and a reload.
// An empty path loads nothing.
export function useLoad<T>(path: string) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const reload = useCallback(() => {
    if (!path) return
    api<T>(path)
      .then((d) => {
        setData(d)
        setError('')
      })
      .catch((e: Error) => setError(e.message))
  }, [path])
  useEffect(reload, [reload])
  return { data, error, reload }
}
