import { type ReactNode, useState } from 'react'
import type { Mood } from './types'

// The mark: a body with an orbit. The second orbit and the moon appear in
// the large version next to her state.
export function Orbit({ size, asleep }: { size: number; asleep?: boolean }) {
  const big = size > 30
  return (
    <svg aria-hidden="true" width={size} height={size} viewBox="0 0 56 56" fill="none" className={asleep ? 'orbit asleep' : 'orbit'}>
      <ellipse cx="28" cy="28" rx="25" ry="11" stroke="var(--sys)" strokeWidth={big ? 1.3 : 2.4} transform="rotate(-28 28 28)" />
      {big && <ellipse cx="28" cy="28" rx="25" ry="11" stroke="var(--line-strong)" strokeWidth="1.3" transform="rotate(34 28 28)" />}
      <circle cx="28" cy="28" r={big ? 9 : 10} fill="var(--nova)" />
      {big && <circle cx="48" cy="16" r="2.5" fill="var(--sys)" />}
    </svg>
  )
}

export function Meters({ mood }: { mood: Mood }) {
  const rows: [string, number][] = [
    ['Warmth', mood.warmth],
    ['Energy', mood.energy],
    ['Confidence', mood.confidence],
  ]
  return (
    <div className="meters">
      {rows.map(([label, value]) => (
        <div className="meter" key={label}>
          <span>{label}</span>
          <div className="track" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)}>
            <div className="fill" style={{ width: `${Math.round(value * 100)}%` }} />
          </div>
        </div>
      ))}
    </div>
  )
}

export function Screen({ eyebrow, title, action, className, children }: { eyebrow: string; title: string; action?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <main className={className ? `screen ${className}` : 'screen'}>
      <header className="screen-head">
        <div>
          <span className="eyebrow">{eyebrow}</span>
          <h1>{title}</h1>
        </div>
        {action}
      </header>
      {children}
    </main>
  )
}

export function Problem({ message }: { message: string }) {
  if (!message) return null
  return (
    <p className="problem" role="alert">
      {message}
    </p>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>
}

const linkPattern = /https?:\/\/[^\s<>"'`]+/gi

// Linked is chat text with each web address made into a chip: the left
// part opens it in the browser, the right part copies the whole address.
export function Linked({ text }: { text: string }) {
  const out: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(linkPattern)) {
    // The full stop or bracket after an address belongs to the sentence.
    const url = m[0].replace(/[.,;:!?)\]}*]+$/, '')
    const at = m.index ?? 0
    if (at > last) out.push(text.slice(last, at))
    out.push(<WebLink key={at} url={url} />)
    last = at + url.length
  }
  if (last < text.length) out.push(text.slice(last))
  return <>{out}</>
}

function WebLink({ url }: { url: string }) {
  const [copied, setCopied] = useState(false)
  const shown = url.replace(/^https?:\/\//i, '').replace(/\/$/, '')
  const feed = /rss|feed|atom|\.xml\b/i.test(url)
  async function copy() {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    } catch {
      // Nothing to fall back to: the address is still in the chip's tooltip.
    }
  }
  return (
    <span className="weblink">
      {/* The window hands any target="_blank" link to the browser. */}
      <a href={url} target="_blank" rel="noopener noreferrer" title={`${url} · opens in your browser`}>
        {feed ? (
          <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none">
            <circle cx="4" cy="12" r="1.5" fill="currentColor" />
            <path d="M3 7.5a5.5 5.5 0 0 1 5.5 5.5M3 3.5a9.5 9.5 0 0 1 9.5 9.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          </svg>
        ) : (
          <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none">
            <circle cx="8" cy="8" r="5.5" stroke="currentColor" strokeWidth="1.5" />
            <path d="M2.5 8h11M8 2.5c1.6 1.6 2.3 3.6 2.3 5.5S9.6 11.9 8 13.5C6.4 11.9 5.7 9.9 5.7 8S6.4 4.1 8 2.5z" stroke="currentColor" strokeWidth="1.3" />
          </svg>
        )}
        <span className="weblink-text">{shown}</span>
      </a>
      <button type="button" onClick={() => void copy()} aria-label={copied ? 'Copied' : 'Copy link'} title={copied ? 'Copied' : 'Copy link'}>
        {copied ? (
          <svg aria-hidden="true" width="13" height="13" viewBox="0 0 16 16" fill="none">
            <path d="M3.5 8.5l3 3 6-7" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        ) : (
          <svg aria-hidden="true" width="13" height="13" viewBox="0 0 16 16" fill="none">
            <rect x="5.5" y="5.5" width="8" height="8" rx="2" stroke="currentColor" strokeWidth="1.5" />
            <path d="M10.5 3.5v-.5a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        )}
      </button>
    </span>
  )
}
