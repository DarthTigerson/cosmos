import type { ReactNode } from 'react'
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

export function Screen({ eyebrow, title, action, children }: { eyebrow: string; title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <main className="screen">
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
