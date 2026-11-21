// Small helpers for showing times the way a person would say them.

export function isZero(at: string | undefined): boolean {
  return !at || at.startsWith('0001-')
}

export function clock(at: string): string {
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
}

// "today 14:30", "tomorrow 10:00", "Sunday 09:00", "3 December 09:00".
export function when(at: string): string {
  const d = new Date(at)
  const days = Math.round((startOfDay(d) - startOfDay(new Date())) / 86_400_000)
  const time = clock(at)
  if (days === 0) return `today ${time}`
  if (days === 1) return `tomorrow ${time}`
  if (days === -1) return `yesterday ${time}`
  if (days > 1 && days < 7) return `${d.toLocaleDateString([], { weekday: 'long' })} ${time}`
  return `${d.toLocaleDateString([], { day: 'numeric', month: 'long' })} ${time}`
}

export function longDate(d: Date): string {
  return d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long' })
}

export function importanceWord(n: number): string {
  if (n >= 4) return 'high'
  if (n === 3) return 'medium'
  return 'low'
}

export function closenessWord(c: number): string {
  if (c >= 0.8) return 'very close'
  if (c >= 0.6) return 'close'
  if (c >= 0.4) return 'familiar'
  return 'distant'
}
