import { useCallback, useEffect, useRef, useState } from 'react'
import type { Signal } from './types'

interface Conn {
  baseUrl: string
  token: string
}

declare global {
  interface Window {
    cosmos?: { conn(): Promise<Conn> }
  }
}

let connPromise: Promise<Conn> | null = null

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

// apiHear sends a recording to the core and gets back the words in it.
export async function apiHear(wav: ArrayBuffer): Promise<string> {
  const c = await conn()
  let res: Response
  try {
    res = await fetch(c.baseUrl + '/api/hear', { method: 'POST', headers: { Authorization: `Bearer ${c.token}`, 'Content-Type': 'audio/wav' }, body: wav })
  } catch {
    throw new Error('The core is not answering. Is it still running?')
  }
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new Error(data?.error ?? `The core answered ${res.status}.`)
  return (data?.text as string) ?? ''
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
export function useLoad<T>(path: string) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const reload = useCallback(() => {
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
