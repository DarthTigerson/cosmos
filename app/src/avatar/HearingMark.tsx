import { type MutableRefObject, useEffect, useRef } from 'react'

// The mark in the listen button while her ears are open: a ring around
// the microphone that swells with the voice it hears and sends out a
// ripple on each loud syllable. Its colour says whose voice it takes it
// to be: yours (blue), someone else's (grey), or, once you stop, hers
// (amber) while what you said is written out.
export type HearingTone = 'rest' | 'you' | 'other' | 'writing'

const colours: Record<HearingTone, string> = {
  rest: '#3a4560',
  you: '#7ab8e0',
  other: '#6b7488',
  writing: '#e0a458',
}

const alpha = (hex: string, a: number) => {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${n >> 16}, ${(n >> 8) & 255}, ${n & 255}, ${a})`
}

export function HearingMark({ level, tone, size = 40 }: { level: MutableRefObject<number>; tone: HearingTone; size?: number }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const toneRef = useRef(tone)
  toneRef.current = tone

  useEffect(() => {
    const c = canvas.current
    if (!c) return
    const ctx = c.getContext('2d')!
    const still = matchMedia('(prefers-reduced-motion: reduce)').matches
    const dpr = Math.min(2, window.devicePixelRatio || 1)
    c.width = size * dpr
    c.height = size * dpr
    let shown = 0
    let prev = 0
    let last = 0
    let ripples: number[] = []
    let frame = 0
    const draw = (t: number) => {
      const tone = toneRef.current
      const colour = colours[tone]
      // Smoothed, so it breathes with the voice instead of flickering.
      const target = tone === 'you' ? level.current : tone === 'other' ? level.current * 0.45 : tone === 'rest' ? 0.04 + 0.03 * Math.sin(t / 900) : 0
      shown += (target - shown) * (target > shown ? 0.35 : 0.15)
      const pulse = tone === 'writing' ? 0.5 + 0.5 * Math.sin(t / 380) : 0
      const S = size * dpr
      const cx = S / 2
      const line = Math.max(1.2, S * 0.045)
      const r = S * 0.24 + shown * S * 0.12 + pulse * S * 0.03
      ctx.clearRect(0, 0, S, S)
      if (!still && tone === 'you' && shown > 0.55 && prev <= 0.55 && t - last > 160) {
        ripples.push(t)
        last = t
      }
      prev = shown
      ripples = ripples.filter((b) => t - b < 900)
      for (const b of ripples) {
        const k = (t - b) / 900
        ctx.strokeStyle = alpha(colour, (1 - k) * 0.55)
        ctx.lineWidth = line
        ctx.beginPath()
        ctx.arc(cx, cx, Math.min(S / 2 - line, r + k * S * 0.16), 0, Math.PI * 2)
        ctx.stroke()
      }
      ctx.fillStyle = alpha(colour, 0.12 + shown * 0.18 + pulse * 0.12)
      ctx.beginPath()
      ctx.arc(cx, cx, r, 0, Math.PI * 2)
      ctx.fill()
      ctx.strokeStyle = colour
      ctx.lineWidth = line * 1.2
      ctx.beginPath()
      ctx.arc(cx, cx, r, 0, Math.PI * 2)
      ctx.stroke()
      // The microphone.
      const s = S * 0.26
      const glyph = tone === 'rest' ? '#ece7dc' : colour
      ctx.strokeStyle = glyph
      ctx.lineWidth = line
      ctx.lineCap = 'round'
      const bw = s * 0.36
      const bh = s * 0.62
      ctx.beginPath()
      ctx.roundRect(cx - bw / 2, cx - bh * 0.72, bw, bh, bw / 2)
      ctx.stroke()
      ctx.beginPath()
      ctx.arc(cx, cx - bh * 0.1, s * 0.34, 0.15 * Math.PI, 0.85 * Math.PI)
      ctx.moveTo(cx, cx + s * 0.24)
      ctx.lineTo(cx, cx + s * 0.4)
      ctx.stroke()
      frame = requestAnimationFrame(draw)
    }
    frame = requestAnimationFrame(draw)
    return () => cancelAnimationFrame(frame)
  }, [level, size])

  return <canvas ref={canvas} aria-hidden="true" style={{ width: size, height: size, display: 'block' }} />
}
