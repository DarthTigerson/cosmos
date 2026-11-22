import { type ReactNode, useEffect, useRef, useState } from 'react'
import { api, useLoad, useSignals } from './api'
import { clock, fullDate, isZero } from './format'
import { type Blip, type BlipKind, notes, useNotes } from './notes'
import { DEFAULT_SIZE, textSize, useTextSize } from './textSize'
import type { CalendarItem, ModelsView, State, StreamEvent, TierName } from './types'

// The strip along the bottom of the window, after the one in vIDE: how
// the core and the companion are on the left; the text size, the notification bell
// and the clock on the right. Everything that opens from it rises from its
// top edge like a tab.

// How long a peeked row stays up; matches .peek-drain in styles.css.
const PEEK_MS = 5000
// Matches the 200ms fades below.
const FADE_MS = 200
// Matches .bell-ring in styles.css.
const RING_MS = 900

export interface Note {
  id: string
  text: string
  icon: ReactNode
  urgent?: boolean
  go?: () => void
  action?: { label: string; run: () => void }
}

const tierWords: Record<TierName, string> = { main: 'main', worker: 'worker', sleeper: 'sleeper', embed: 'memory' }

function openSession(id: number | undefined) {
  if (!id) {
    location.hash = '#nova'
    return
  }
  void api(`/api/sessions/${id}/open`, { method: 'POST' })
    .catch(() => undefined)
    .finally(() => {
      location.hash = '#nova'
    })
}

// useFooterNotes works out the notes that hold right now, and turns the
// core's signals into blips.
function useFooterNotes(state: State | null, down: boolean, screen: string): Note[] {
  const name = state?.name ?? 'Companion'
  const models = useLoad<ModelsView>('/api/models')
  const [unread, setUnread] = useState<StreamEvent[]>([])
  const onNova = screen === 'nova'

  // A failed model is noticed within half a minute, or as soon as she
  // falls asleep or wakes, which is when models stop and start.
  useEffect(() => {
    const timer = setInterval(models.reload, 30_000)
    return () => clearInterval(timer)
  }, [models.reload])

  // Her words are read once her screen is open.
  useEffect(() => {
    if (onNova) setUnread([])
  }, [onNova])

  useSignals((s) => {
    if (s.kind === 'message') {
      const ev = s.data as StreamEvent
      if (!onNova && (ev.kind === 'nova_msg' || ev.kind === 'initiative')) setUnread((u) => [...u, ev])
    }
    if (s.kind === 'state') {
      models.reload()
      if (s.data === 'asleep') notes.blip('asleep', `${name} fell asleep`)
      if (s.data === 'awake') notes.blip('awake', `${name} woke up`)
    }
    if (s.kind === 'sleep') {
      const err = (s.data as { error?: string } | null)?.error
      if (err) notes.blip('problem', `Her sleep ran into a problem: ${err}`)
    }
  })

  const out: Note[] = []
  if (down) out.push({ id: 'core-down', urgent: true, icon: <WarnIcon />, text: 'The core is not answering' })
  for (const ev of unread) {
    out.push({
      id: `msg-${ev.id}`,
      icon: <TalkIcon />,
      text: `${name}: ${ev.text}`,
      go: () => openSession(ev.session_id),
    })
  }
  const pending = state?.pending_proposals ?? 0
  if (pending > 0) {
    const go = () => {
      location.hash = '#sleep'
    }
    out.push({
      id: 'proposals',
      icon: <MoonIcon />,
      text: `${pending} change${pending === 1 ? '' : 's'} from her night to review`,
      go,
      action: { label: 'Review', run: go },
    })
  }
  for (const t of down ? [] : models.data?.tiers ?? []) {
    if (t.status.state !== 'failed') continue
    out.push({
      id: `model-${t.tier}`,
      icon: <ChipIcon />,
      text: `The ${tierWords[t.tier]} model failed${t.status.error ? `: ${t.status.error}` : ''}`,
      go: () => {
        location.hash = '#models'
      },
    })
  }
  return out
}

export function Footer({ state, error, screen }: { state: State | null; error: string; screen: string }) {
  const items = useFooterNotes(state, !!error, screen)
  const ids = items.map((n) => n.id).join('\n')
  useEffect(() => notes.sync(ids ? ids.split('\n') : []), [ids])
  const asleep = state?.state === 'asleep'

  return (
    <footer className="footer">
      <div className="footer-left">
        {error ? (
          <span className="down">core not answering</span>
        ) : (
          <>
            <span>core running</span>
            {state && (
              <>
                <span className="footer-sep">·</span>
                <span>{asleep ? 'asleep' : isZero(state.awake_since) ? 'awake' : `awake since ${clock(state.awake_since)}`}</span>
              </>
            )}
          </>
        )}
      </div>
      <div className="footer-right">
        <TextSizeChip />
        <div className="bell-anchor">
          <Bell items={items} />
          <Peek items={items} />
          <Panel items={items} />
        </div>
        <Clock />
      </div>
    </footer>
  )
}

// Tip is the hover label for the footer's icons: a small tab rising from
// the footer after a short wait. hidden keeps it away while the icon's own
// panel holds the same spot.
function Tip({ label, hidden, children }: { label: ReactNode; hidden?: boolean; children: ReactNode }) {
  return (
    <span className="tip-host">
      {children}
      {!hidden && (
        <span role="tooltip" className="tip rise">
          {label}
        </span>
      )}
    </span>
  )
}

// Visible notes: still holding, not cleared, and not seen yet.
function visible(items: Note[], seen: string[], cleared: string[]) {
  return items.filter((n) => n.urgent || (!seen.includes(n.id) && !cleared.includes(n.id)))
}

// useArrival is the latest arrival for ms after it lands, so the bell rings
// and the peek opens once per note that appears.
function useArrival(ms: number) {
  const { arrival } = useNotes()
  const [live, setLive] = useState(arrival && Date.now() - arrival.at < ms ? arrival : null)
  useEffect(() => {
    if (!arrival) return
    const left = arrival.at + ms - Date.now()
    if (left <= 0) return
    setLive(arrival)
    const timer = setTimeout(() => setLive(null), left)
    return () => clearTimeout(timer)
  }, [arrival, ms])
  return live
}

// Bell and count in one pill: amber while something is unread, an outline
// once everything has been seen, and a quiet bell with nothing at all. Red
// while an urgent note is up.
function Bell({ items }: { items: Note[] }) {
  const { open, seen, cleared } = useNotes()
  const listed = items.filter((n) => n.urgent || !cleared.includes(n.id))
  const unread = visible(items, seen, cleared)
  const ringing = useArrival(RING_MS)
  const count = listed.length
  const look = count === 0 ? 'none' : unread.some((n) => n.urgent) ? 'urgent' : unread.length > 0 ? 'unread' : 'read'

  return (
    <Tip label="Notifications" hidden={open}>
      <button
        type="button"
        className={`pill bell ${look}`}
        data-notes-toggle=""
        disabled={count === 0}
        aria-label={count > 0 ? `${count} notification${count === 1 ? '' : 's'}` : 'Notifications'}
        onMouseUp={(e) => {
          if (count > 0 && e.button === 0) notes.toggle()
        }}
      >
        <span key={ringing?.seq} className={ringing ? 'bell-ring' : undefined}>
          <BellIcon />
        </span>
        {count > 0 && <span>{count}</span>}
      </button>
    </Tip>
  )
}

function Panel({ items }: { items: Note[] }) {
  const { open, seen, cleared } = useNotes()
  const ref = useRef<HTMLDivElement>(null)
  const [mounted, setMounted] = useState(open)
  const listed = items.filter((n) => n.urgent || !cleared.includes(n.id))
  const clearable = listed.filter((n) => !n.urgent)

  // Closing it, however that happens, marks what it showed as seen.
  const wasOpen = useRef(open)
  const shown = useRef<string[]>([])
  shown.current = listed.map((n) => n.id)
  useEffect(() => {
    if (wasOpen.current && !open) notes.see(shown.current)
    wasOpen.current = open
  }, [open])

  // Kept in the page while it fades out, then gone, so a stray click can
  // never land on a hidden row.
  useEffect(() => {
    if (open) {
      setMounted(true)
      return
    }
    const timer = setTimeout(() => setMounted(false), FADE_MS)
    return () => clearTimeout(timer)
  }, [open])

  // Nothing left to show: whatever it was has sorted itself out.
  useEffect(() => {
    if (open && listed.length === 0) notes.close()
  }, [open, listed.length])

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const target = e.target as Element
      // The bell toggles the panel itself on mouseup.
      if (target.closest?.('[data-notes-toggle]')) return
      if (ref.current && !ref.current.contains(target)) notes.close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') notes.close()
    }
    window.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={ref} className={open ? 'notes-panel rise open' : 'notes-panel rise'}>
      {mounted && (
        <>
          <div className="notes-head">
            <span>
              <b>Notifications</b> · {listed.length}
            </span>
            {clearable.length > 0 && (
              <button type="button" onMouseUp={(e) => e.button === 0 && notes.clear(clearable.map((n) => n.id))}>
                Dismiss all
              </button>
            )}
          </div>
          <ul className="notes-rows">
            {listed.map((n) => (
              <li key={n.id} className={n.urgent ? 'urgent' : undefined}>
                <button
                  type="button"
                  className="note-main"
                  disabled={!n.go}
                  onMouseUp={(e) => {
                    if (e.button !== 0) return
                    n.go?.()
                    notes.close()
                  }}
                >
                  <i className={n.urgent ? 'dot urgent' : seen.includes(n.id) ? 'dot read' : 'dot'} />
                  <span className="note-icon">{n.icon}</span>
                  <span className="note-text">{n.text}</span>
                </button>
                <RowAction note={n} after={notes.close} />
                {n.urgent ? (
                  <span className="note-x-space" />
                ) : (
                  <button type="button" className="note-x" aria-label="Dismiss notification" onMouseUp={(e) => e.button === 0 && notes.clear([n.id])}>
                    ×
                  </button>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}

function RowAction({ note, after }: { note: Note; after?: () => void }) {
  if (!note.action) return null
  const { label, run } = note.action
  return (
    <button
      type="button"
      className={note.urgent ? 'note-action urgent' : 'note-action'}
      onMouseUp={(e) => {
        if (e.button !== 0) return
        e.stopPropagation()
        run()
        after?.()
      }}
    >
      {label}
    </button>
  )
}

interface PeekEntry {
  id: string
  // When the row goes. Pushed back by however long the peek was hovered.
  deadline: number
  leaving: boolean
}

const blipIcons: Record<BlipKind, () => ReactNode> = {
  asleep: () => <MoonIcon />,
  awake: () => <SunIcon />,
  problem: () => <WarnIcon />,
  info: () => <BellIcon />,
}

function blipNote(b: Blip): Note {
  return { id: b.id, text: b.text, icon: blipIcons[b.kind](), urgent: b.kind === 'problem' }
}

// A short look at what just arrived, where the panel opens. Each arrival
// gets its own row and its own countdown; later ones stack beneath. Letting
// a row run out does not mark it seen: the bell stays lit until the panel
// is opened. Hovering pauses every row.
function Peek({ items }: { items: Note[] }) {
  const { open, seen, cleared, blips } = useNotes()
  const arrival = useArrival(PEEK_MS)
  const [entries, setEntries] = useState<PeekEntry[]>([])
  const [hovered, setHovered] = useState(false)
  const hoveredAt = useRef<number | null>(null)
  const unread = visible(items, seen, cleared)

  const lastSeq = useRef<number | null>(null)
  useEffect(() => {
    if (!arrival || lastSeq.current === arrival.seq) return
    lastSeq.current = arrival.seq
    const deadline = arrival.at + PEEK_MS
    setEntries((prev) => [...prev.filter((e) => !arrival.ids.includes(e.id)), ...arrival.ids.map((id) => ({ id, deadline, leaving: false }))])
  }, [arrival])

  const lastBlip = useRef(0)
  useEffect(() => {
    const fresh = blips.filter((b) => b.seq > lastBlip.current)
    if (fresh.length === 0) return
    lastBlip.current = Math.max(...fresh.map((b) => b.seq))
    setEntries((prev) => [...prev, ...fresh.map((b) => ({ id: b.id, deadline: b.at + PEEK_MS, leaving: false }))])
  }, [blips])

  // The panel replaces the peek outright. Blips are not in the panel, so
  // they are simply gone.
  useEffect(() => {
    if (!open) return
    setEntries([])
    notes.dropAll()
  }, [open])

  // One timer for whatever is due next: a row starting to fade, or a faded
  // row being taken away.
  useEffect(() => {
    if (hovered || entries.length === 0) return
    const due = (e: PeekEntry) => (e.leaving ? e.deadline + FADE_MS : e.deadline)
    const next = Math.min(...entries.map(due))
    const timer = setTimeout(() => {
      const now = Date.now()
      for (const e of entries) if (e.leaving && e.deadline + FADE_MS <= now) notes.drop(e.id)
      setEntries((prev) =>
        prev.filter((e) => !(e.leaving && e.deadline + FADE_MS <= now)).map((e) => (!e.leaving && e.deadline <= now ? { ...e, leaving: true } : e)),
      )
    }, Math.max(0, next - Date.now()))
    return () => clearTimeout(timer)
  }, [entries, hovered])

  // A note leaves the peek as soon as it is seen or its condition clears.
  const rows = entries
    .map((entry) => {
      const blip = blips.find((b) => b.id === entry.id)
      const note = blip ? blipNote(blip) : unread.find((n) => n.id === entry.id)
      return note && { entry, note, isBlip: !!blip }
    })
    .filter((r): r is { entry: PeekEntry; note: Note; isBlip: boolean } => !!r)
  if (rows.length === 0 || open) return null
  const closing = rows.every((r) => r.entry.leaving)
  const others = unread.length - rows.filter((r) => !r.isBlip).length

  return (
    <div
      className={closing ? 'notes-peek rise closing' : 'notes-peek rise'}
      data-notes-toggle=""
      onMouseEnter={() => {
        hoveredAt.current = Date.now()
        setHovered(true)
      }}
      onMouseLeave={() => {
        const paused = hoveredAt.current === null ? 0 : Date.now() - hoveredAt.current
        hoveredAt.current = null
        setEntries((prev) => prev.map((e) => (e.leaving ? e : { ...e, deadline: e.deadline + paused })))
        setHovered(false)
      }}
    >
      {others > 0 && (
        <button type="button" className="peek-more" onMouseUp={(e) => e.button === 0 && notes.toggle()}>
          +{others} more
        </button>
      )}
      <ul className="notes-rows">
        {rows.map(({ entry, note, isBlip }) => (
          <li key={note.id} className={[entry.leaving ? 'leaving' : '', note.urgent ? 'urgent' : ''].join(' ')}>
            <button
              type="button"
              className="note-main"
              onMouseUp={(e) => {
                if (e.button !== 0) return
                if (isBlip) notes.drop(note.id)
                else {
                  note.go?.()
                  notes.see([note.id])
                }
                setEntries((prev) => prev.filter((p) => p.id !== note.id))
              }}
            >
              <i className={note.urgent ? 'dot urgent' : 'dot'} />
              <span className="note-icon lit">{note.icon}</span>
              <span className="note-text">{note.text}</span>
            </button>
            <RowAction note={note} after={() => notes.see([note.id])} />
            <span className="peek-drain" />
          </li>
        ))}
      </ul>
    </div>
  )
}

// How long the chip stays lit after the size changes, so the new number
// is noticed when it changed by shortcut.
const FLASH_MS = 1500

function TextSizeChip() {
  const size = useTextSize()
  const [open, setOpen] = useState(false)
  const [flash, setFlash] = useState(false)

  const prev = useRef(size)
  useEffect(() => {
    if (prev.current === size) return
    prev.current = size
    setFlash(true)
    const timer = setTimeout(() => setFlash(false), FLASH_MS)
    return () => clearTimeout(timer)
  }, [size])

  useEffect(() => {
    if (!open) return
    const close = () => setOpen(false)
    window.addEventListener('click', close)
    return () => window.removeEventListener('click', close)
  }, [open])

  return (
    <div className="chip-anchor">
      <Tip label="Text size" hidden={open}>
        <button
          type="button"
          className={['pill size-chip', flash ? 'flash' : '', open ? 'open' : ''].join(' ')}
          aria-label={`Text size ${size}`}
          aria-expanded={open}
          onClick={(e) => {
            e.stopPropagation()
            setOpen(!open)
          }}
        >
          <TextIcon />
          <span>{size}</span>
        </button>
      </Tip>
      {open && (
        <div className="size-panel rise" onClick={(e) => e.stopPropagation()}>
          <span className="label">Text size</span>
          <div className="size-row">
            <span>Size</span>
            <span className="stepper">
              <button type="button" aria-label="Smaller" disabled={size <= textSize.min} onClick={textSize.smaller}>
                −
              </button>
              <span className="stepper-value">{size}</span>
              <button type="button" aria-label="Larger" disabled={size >= textSize.max} onClick={textSize.larger}>
                +
              </button>
            </span>
          </div>
          <p className="size-keys">
            <kbd>⌘⇧+</kbd> larger · <kbd>⌘⇧−</kbd> smaller · <kbd>⌘⇧0</kbd> reset
          </p>
          {size !== DEFAULT_SIZE && (
            <button type="button" className="size-reset" onClick={textSize.reset}>
              Back to {DEFAULT_SIZE}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// The clock owns the far-right corner. Hovering shows the date and what is
// next on the calendar; clicking opens the calendar.
function Clock() {
  const [now, setNow] = useState(() => new Date())
  const calendar = useLoad<CalendarItem[]>('/api/calendar')

  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(timer)
  }, [])
  // Nothing signals a calendar change, so it is looked at again each minute.
  useEffect(() => {
    const timer = setInterval(calendar.reload, 60_000)
    return () => clearInterval(timer)
  }, [calendar.reload])

  const next = (calendar.data ?? []).find((c) => new Date(c.at).getTime() > now.getTime())
  const soon = next && new Date(next.at).getTime() - now.getTime() < 86_400_000

  return (
    <span className="tip-host">
      <button
        type="button"
        className="clock"
        aria-label="Open calendar"
        onClick={() => {
          location.hash = '#calendar'
        }}
      >
        {soon && (
          <span className="clock-next-mark">
            {next.kind === 'reminder' ? <BellIcon /> : <CalendarIcon />}
          </span>
        )}
        <span className="clock-time">{clock(now.toISOString())}</span>
      </button>
      <span role="tooltip" className="tip clock-tip rise">
        <span className="tip-line">
          <CalendarIcon />
          {fullDate(now)}
        </span>
        {soon && (
          <span className="tip-line">
            {next.kind === 'reminder' ? <BellIcon /> : <CalendarIcon />}
            <span>
              <b>{next.title}</b> at {clock(next.at)}
              <span className="muted"> · {untilWords(new Date(next.at).getTime() - now.getTime())}</span>
            </span>
          </span>
        )}
      </span>
    </span>
  )
}

function untilWords(ms: number): string {
  const min = Math.max(1, Math.round(ms / 60_000))
  if (min < 60) return `in ${min} min`
  const h = Math.floor(min / 60)
  const m = min % 60
  return m === 0 ? `in ${h} h` : `in ${h} h ${m} min`
}

// Icons, drawn on the same 24-unit box with a 2-unit stroke.
function Svg({ children }: { children: ReactNode }) {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      {children}
    </svg>
  )
}

function BellIcon() {
  return (
    <Svg>
      <path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15L6 16Z" />
      <path d="M10 21h4" />
    </Svg>
  )
}

function CalendarIcon() {
  return (
    <Svg>
      <rect x="3.5" y="5" width="17" height="15" rx="2" />
      <path d="M3.5 10h17M8 3v4M16 3v4" />
    </Svg>
  )
}

function MoonIcon() {
  return (
    <Svg>
      <path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5Z" />
    </Svg>
  )
}

function SunIcon() {
  return (
    <Svg>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" />
    </Svg>
  )
}

function TalkIcon() {
  return (
    <Svg>
      <path d="M4 5h16v11H9l-5 4V5Z" />
    </Svg>
  )
}

function ChipIcon() {
  return (
    <Svg>
      <rect x="6" y="6" width="12" height="12" rx="1.5" />
      <path d="M9 2.5v3.5M15 2.5v3.5M9 18v3.5M15 18v3.5M2.5 9H6M2.5 15H6M18 9h3.5M18 15h3.5" />
    </Svg>
  )
}

function WarnIcon() {
  return (
    <Svg>
      <path d="M12 3.5 21.5 20h-19L12 3.5Z" />
      <path d="M12 10v4.5M12 17.5v.01" />
    </Svg>
  )
}

function TextIcon() {
  return (
    <Svg>
      <path d="M4 7V5h11v2M9.5 5v14M7.5 19h4M14 12v-1.5h7V12M17.5 10.5V19M16 19h3" />
    </Svg>
  )
}
