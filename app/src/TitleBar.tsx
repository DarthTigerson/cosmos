import { type ReactNode, useEffect, useState } from 'react'
import { useLoad } from './api'
import type { BatteryStatus, MemoryBreakdown, MemoryUsage, ModelsView, TierName } from './types'

// The bar along the top of the window, after vIDE's: it holds the window's
// buttons and lets the window be dragged, names where you are in the middle,
// and on the right shows the Mac's memory and battery. Memory matters most
// here: her models are most of what this Mac holds, and they do not all fit
// at once.

// Memory changes quickly while a model loads; the battery does not.
const MEMORY_POLL_MS = 3000
const BATTERY_POLL_MS = 30_000
const LOW_BATTERY = 20

// Read once, so the polls below keep the same function between renders.
const readMemory = window.cosmos?.memory
const readBreakdown = window.cosmos?.memoryBreakdown
const readBattery = window.cosmos?.battery

function usePoll<T>(read: (() => Promise<T>) | undefined, ms: number, on = true): T | null {
  const [value, setValue] = useState<T | null>(null)
  useEffect(() => {
    if (!read || !on) return
    const poll = () => {
      if (document.visibilityState !== 'visible') return
      read().then(setValue).catch(() => undefined)
    }
    poll()
    const timer = setInterval(poll, ms)
    document.addEventListener('visibilitychange', poll)
    return () => {
      clearInterval(timer)
      document.removeEventListener('visibilitychange', poll)
    }
  }, [read, ms, on])
  return value
}

export function TitleBar({ title }: { title: string }) {
  // Outside Electron (a plain browser while developing) there is nothing to
  // read, so the pills stay away.
  const memory = usePoll(readMemory, MEMORY_POLL_MS)
  const battery = usePoll(readBattery, BATTERY_POLL_MS)

  return (
    <header className="titlebar">
      <span className="titlebar-title">{title}</span>
      <div className="titlebar-right">
        {memory && <MemoryPill usage={memory} />}
        {memory && battery && <span aria-hidden="true" className="titlebar-rule" />}
        {battery && <BatteryPill status={battery} />}
      </div>
    </header>
  )
}

const gb = (bytes: number) => (bytes / 1024 ** 3).toFixed(1)

// "1.9 GB" from 1 GB up, whole megabytes below that ("650 MB").
function size(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${gb(bytes)} GB`
  return `${Math.round(bytes / 1024 ** 2)} MB`
}

const tierLabels: Record<TierName, string> = { main: 'Main model', worker: 'Worker model', sleeper: 'Sleeper model', embed: 'Memory model' }

function portOf(baseUrl: string): number {
  try {
    const u = new URL(baseUrl)
    return Number(u.port || (u.protocol === 'https:' ? 443 : 80))
  } catch {
    return 0
  }
}

function local(baseUrl: string): boolean {
  try {
    return ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(baseUrl).hostname)
  } catch {
    return false
  }
}

interface Row {
  key: string
  label: string
  value: string
  dim?: boolean
}

// The rows under "Cosmos total": the app, the core, then each model. A
// loaded model shows what it holds; one at rest shows how big its file is,
// which is roughly what it will take when it loads.
function cosmosRows(b: MemoryBreakdown, models: ModelsView | null): { total: number; rows: Row[] } {
  const rows: Row[] = [
    { key: 'app', label: 'Cosmos app', value: size(b.appBytes) },
    { key: 'core', label: 'Core', value: size(b.coreBytes) },
  ]
  let total = b.appBytes + b.coreBytes
  const claimed = new Set<number>()
  for (const t of models?.tiers ?? []) {
    if (t.shared_with || !t.endpoint.base_url) continue
    const port = portOf(t.endpoint.base_url)
    const server = local(t.endpoint.base_url) ? b.models.find((m) => m.port === port) : undefined
    if (server) {
      claimed.add(port)
      total += server.bytes
      rows.push({ key: t.tier, label: tierLabels[t.tier], value: size(server.bytes) })
      continue
    }
    const file = models?.files.find((f) => f.path === t.endpoint.launch?.model_path)
    const state = t.status.state === 'external' ? 'elsewhere' : t.status.state
    rows.push({ key: t.tier, label: tierLabels[t.tier], value: file ? `${state} · ${size(file.size)} file` : state, dim: true })
  }
  for (const m of b.models) {
    if (claimed.has(m.port)) continue
    total += m.bytes
    rows.push({ key: `port-${m.port}`, label: `llama-server :${m.port}`, value: size(m.bytes) })
  }
  return { total, rows }
}

// The pill leads with memory pressure, coloured, with a fill bar, and the
// used/total number trails. Hovering opens the details, including what each
// of her models holds, which is only measured while it is open.
function MemoryPill({ usage }: { usage: MemoryUsage }) {
  const [open, setOpen] = useState(false)
  const breakdown = usePoll(readBreakdown, MEMORY_POLL_MS, open)
  const models = useLoad<ModelsView>('/api/models')
  const used = usage.totalBytes > 0 ? usage.usedBytes / usage.totalBytes : 0
  const pressureWord = usage.pressure === 'critical' ? 'Critical' : usage.pressure === 'warn' ? 'Warn' : 'Normal'
  const cosmos = breakdown && cosmosRows(breakdown, models.data)

  return (
    <span
      className={`tb-pill ram ${usage.pressure}`}
      onMouseEnter={() => {
        setOpen(true)
        models.reload()
      }}
      onMouseLeave={() => setOpen(false)}
    >
      <RamIcon />
      <span className="tb-bar">
        <span style={{ width: `${Math.min(100, Math.max(4, used * 100))}%` }} />
      </span>
      {gb(usage.usedBytes)}/{gb(usage.totalBytes)} GB
      {open && (
        <div className="tb-panel wide">
          <Line label="System" value={`${gb(usage.usedBytes)}/${gb(usage.totalBytes)} GB`} />
          <Line label="Pressure" value={<span className={`pressure ${usage.pressure}`}>{pressureWord} · {Math.round(used * 100)}%</span>} />
          {usage.swapUsedBytes > 0 && <Line label="Swap" value={size(usage.swapUsedBytes)} />}
          {usage.compressedBytes > 0 && <Line label="Compressed" value={size(usage.compressedBytes)} />}
          <div className="tb-total">
            <span>Cosmos total</span>
            <span>{cosmos ? size(cosmos.total) : '…'}</span>
          </div>
          {cosmos?.rows.map((r) => (
            <div key={r.key} className={r.dim ? 'tb-sub dim' : 'tb-sub'}>
              <span>{r.label}</span>
              <span>{r.value}</span>
            </div>
          ))}
        </div>
      )}
    </span>
  )
}

function Line({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="tb-line">
      <span>{label}</span>
      <span>{value}</span>
    </div>
  )
}

function minutesWords(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return h > 0 ? `${h} h ${m} min` : `${m} min`
}

// Icon and percentage, details on hover. A Mac without a battery shows none.
function BatteryPill({ status }: { status: BatteryStatus }) {
  const [open, setOpen] = useState(false)
  const low = status.percent <= LOW_BATTERY && !status.charging
  const power = status.charging ? 'Charging' : status.pluggedIn ? 'Plugged in' : 'On battery'

  return (
    <span className={low ? 'tb-pill battery low' : 'tb-pill battery'} onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}>
      <BatteryIcon percent={status.percent} charging={status.charging} low={low} />
      {status.percent}%
      {open && (
        <div className="tb-panel">
          <Line label="Battery" value={`${status.percent}%`} />
          <Line label="Power" value={power} />
          {status.minutesLeft != null && <Line label="Time left" value={minutesWords(status.minutesLeft)} />}
        </div>
      )}
    </span>
  )
}

function BatteryIcon({ percent, charging, low }: { percent: number; charging: boolean; low: boolean }) {
  const fill = Math.max(1, (14 * Math.min(100, Math.max(0, percent))) / 100)
  return (
    <svg aria-hidden="true" width="22" height="12" viewBox="0 0 22 12" fill="none">
      <rect x="0.75" y="1.75" width="17.5" height="8.5" rx="2.25" stroke="currentColor" strokeWidth="1.5" />
      <rect x="19.2" y="4.25" width="1.8" height="3.5" rx="0.9" fill="currentColor" />
      <rect x="2.5" y="3.5" width={fill} height="5" rx="1" fill={charging ? 'var(--nova)' : low ? 'var(--danger)' : 'currentColor'} />
      {charging && <path d="M10.3 2.3L7 6.6H9.3L8.4 9.7L11.7 5.4H9.4L10.3 2.3Z" fill="var(--text)" stroke="var(--nav)" strokeWidth="0.8" strokeLinejoin="round" />}
    </svg>
  )
}

function RamIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
      <rect x="3" y="7" width="18" height="10" rx="1.5" />
      <path d="M7 7V4.5M11 7V4.5M13 7V4.5M17 7V4.5M7 17V19.5M11 17V19.5M13 17V19.5M17 17V19.5" />
    </svg>
  )
}
