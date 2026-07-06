import { useState, useEffect, useCallback } from 'react'

const FONTS = [
  { label: 'Ubuntu', value: '"Ubuntu", system-ui, sans-serif' },
  { label: 'System', value: '-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif' },
  { label: 'Inter',  value: '"Inter", system-ui, sans-serif' },
  { label: 'Georgia', value: 'Georgia, "Times New Roman", serif' },
  { label: 'Mono',   value: '"SF Mono", ui-monospace, "Fira Code", monospace' },
]

const THEMES = [
  {
    label: 'Ember',
    vars: {
      '--bg': '#0d0b09', '--bg-surface': '#161210', '--bg-input': '#1e1914',
      '--border': '#2c2318', '--text': '#ece6db', '--text-dim': '#7a6b59',
      '--cosmos-accent': '#d4895a', '--cosmos-accent-glow': 'rgba(212,137,90,0.18)',
      '--cosmos-accent-dim': '#2b1608', '--user-accent': '#6899c4',
      '--user-accent-dim': '#0c1e30', '--thinking': '#9a7450', '--thinking-bg': '#120e0a',
    },
  },
  {
    label: 'Slate',
    vars: {
      '--bg': '#0b0d11', '--bg-surface': '#131720', '--bg-input': '#1a1f2e',
      '--border': '#222840', '--text': '#dde3f0', '--text-dim': '#5a6480',
      '--cosmos-accent': '#7b9fd4', '--cosmos-accent-glow': 'rgba(123,159,212,0.18)',
      '--cosmos-accent-dim': '#0d1628', '--user-accent': '#89c4a4',
      '--user-accent-dim': '#0c2018', '--thinking': '#5a7ba0', '--thinking-bg': '#0b0f16',
    },
  },
  {
    label: 'Forest',
    vars: {
      '--bg': '#090d09', '--bg-surface': '#101510', '--bg-input': '#171e17',
      '--border': '#1f2e1f', '--text': '#ddeedd', '--text-dim': '#5a7a5a',
      '--cosmos-accent': '#6db87a', '--cosmos-accent-glow': 'rgba(109,184,122,0.18)',
      '--cosmos-accent-dim': '#0d1e0d', '--user-accent': '#c49a6d',
      '--user-accent-dim': '#1e1208', '--thinking': '#5a8a5a', '--thinking-bg': '#0a0f0a',
    },
  },
  {
    label: 'Light',
    vars: {
      '--bg': '#f5f2ee', '--bg-surface': '#ffffff', '--bg-input': '#ede9e3',
      '--border': '#d8d0c5', '--text': '#2a2018', '--text-dim': '#8a7a68',
      '--cosmos-accent': '#b56c3a', '--cosmos-accent-glow': 'rgba(181,108,58,0.15)',
      '--cosmos-accent-dim': '#f5e8dc', '--user-accent': '#3a78b5',
      '--user-accent-dim': '#dceaf5', '--thinking': '#9a7a5a', '--thinking-bg': '#ede8e2',
    },
  },
  {
    label: 'Solar',
    vars: {
      '--bg': '#0b0b0a', '--bg-surface': '#131310', '--bg-input': '#1a1a16',
      '--border': '#2a2a20', '--text': '#f5eec0', '--text-dim': '#7a7240',
      '--cosmos-accent': '#ffe033', '--cosmos-accent-glow': 'rgba(255,224,51,0.18)',
      '--cosmos-accent-dim': '#1f1900', '--user-accent': '#e8a030',
      '--user-accent-dim': '#1f1000', '--thinking': '#c8960a', '--thinking-bg': '#0e0c00',
    },
  },
]

const DEFAULTS = {
  theme: 'Ember',
  font: '"Ubuntu", system-ui, sans-serif',
  fontSize: 15,
  lineHeight: 1.65,
  bubbleWidth: 680,
}

function load() {
  try { return { ...DEFAULTS, ...JSON.parse(localStorage.getItem('cosmos-settings') || '{}') } }
  catch { return DEFAULTS }
}

function save(s) {
  localStorage.setItem('cosmos-settings', JSON.stringify(s))
}

export function applySettings(s) {
  const root = document.documentElement.style
  const theme = THEMES.find(t => t.label === s.theme) || THEMES[0]
  Object.entries(theme.vars).forEach(([k, v]) => root.setProperty(k, v))
  // derive user bubble border from user-accent hex
  const ua = theme.vars['--user-accent'] || '#6899c4'
  const r = parseInt(ua.slice(1,3),16), g = parseInt(ua.slice(3,5),16), b = parseInt(ua.slice(5,7),16)
  root.setProperty('--user-accent-border', `rgba(${r},${g},${b},0.35)`)
  root.setProperty('--font-ui', s.font)
  root.setProperty('--font-size-base', `${s.fontSize}px`)
  root.setProperty('--line-height-base', s.lineHeight)
  root.setProperty('--bubble-max-width', `${s.bubbleWidth}px`)
}

export function initSettings() {
  applySettings(load())
}

export default function Settings({ onClose }) {
  const [s, setS] = useState(load)

  const apply = useCallback((next) => {
    setS(next)
    save(next)
    applySettings(next)
  }, [])

  function set(key, val) { apply({ ...s, [key]: val }) }

  function reset() { apply(DEFAULTS) }

  return (
    <div className="settings-overlay" onClick={onClose}>
      <aside className="settings-panel" onClick={e => e.stopPropagation()}>
        <div className="settings-header">
          <span>Settings</span>
          <button className="settings-close" onClick={onClose}>×</button>
        </div>

        <div className="settings-body">
          <section className="settings-section">
            <div className="settings-label">Theme</div>
            <div className="settings-swatches">
              {THEMES.map(t => (
                <button
                  key={t.label}
                  className={`swatch${s.theme === t.label ? ' active' : ''}`}
                  style={{ background: t.vars['--bg-surface'], borderColor: t.vars['--cosmos-accent'] }}
                  onClick={() => set('theme', t.label)}
                  title={t.label}
                >
                  <span className="swatch-dot" style={{ background: t.vars['--cosmos-accent'] }} />
                  <span className="swatch-label" style={{ color: t.vars['--text'] }}>{t.label}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="settings-section">
            <div className="settings-label">Font</div>
            <div className="settings-font-list">
              {FONTS.map(f => (
                <button
                  key={f.label}
                  className={`font-option${s.font === f.value ? ' active' : ''}`}
                  style={{ fontFamily: f.value }}
                  onClick={() => set('font', f.value)}
                >
                  {f.label} — The quick brown fox
                </button>
              ))}
            </div>
          </section>

          <section className="settings-section">
            <div className="settings-label">
              Font size <span className="settings-value">{s.fontSize}px</span>
            </div>
            <input type="range" min="12" max="20" step="1"
              value={s.fontSize}
              onChange={e => set('fontSize', Number(e.target.value))}
            />
          </section>

          <section className="settings-section">
            <div className="settings-label">
              Line height <span className="settings-value">{s.lineHeight.toFixed(2)}</span>
            </div>
            <input type="range" min="1.2" max="2.2" step="0.05"
              value={s.lineHeight}
              onChange={e => set('lineHeight', Number(e.target.value))}
            />
          </section>

          <section className="settings-section">
            <div className="settings-label">
              Bubble width <span className="settings-value">{s.bubbleWidth}px</span>
            </div>
            <input type="range" min="400" max="900" step="20"
              value={s.bubbleWidth}
              onChange={e => set('bubbleWidth', Number(e.target.value))}
            />
          </section>

          <button className="settings-reset" onClick={reset}>Reset to defaults</button>
        </div>
      </aside>
    </div>
  )
}
