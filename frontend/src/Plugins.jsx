import { useState } from 'react'

function IconLightning() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
    </svg>
  )
}

function IconSearch() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8"/>
      <line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
  )
}

function IconMessageCircle() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
    </svg>
  )
}

function IconBookmark() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>
    </svg>
  )
}

function IconDatabase() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <ellipse cx="12" cy="5" rx="9" ry="3"/>
      <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
      <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
    </svg>
  )
}

function IconMinimize() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 14 10 14 10 20"/>
      <polyline points="20 10 14 10 14 4"/>
      <line x1="10" y1="14" x2="3" y2="21"/>
      <line x1="21" y1="3" x2="14" y2="10"/>
    </svg>
  )
}

function IconRepeat() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="17 1 21 5 17 9"/>
      <path d="M3 11V9a4 4 0 0 1 4-4h14"/>
      <polyline points="7 23 3 19 7 15"/>
      <path d="M21 13v2a4 4 0 0 1-4 4H3"/>
    </svg>
  )
}

const ICONS = {
  superpowers: IconLightning,
  wikisearch: IconSearch,
  ask_thomas: IconMessageCircle,
  remember: IconBookmark,
  knowledge: IconDatabase,
  compress: IconMinimize,
  followup: IconRepeat,
}

export const PLUGIN_REGISTRY = [
  {
    id: 'superpowers',
    name: 'Superpowers',
    iconKey: 'superpowers',
    description: 'Give Cosmos the ability to run shell commands, read and write files, and execute Python — all on your machine.',
    tags: ['shell', 'files', 'python'],
    status: 'active',
  },
  {
    id: 'wikisearch',
    name: 'WikiSearch',
    iconKey: 'wikisearch',
    description: 'Let Cosmos search and read Wikipedia articles to answer factual questions with up-to-date, sourced information.',
    tags: ['wikipedia', 'search', 'knowledge'],
    status: 'active',
  },
  {
    id: 'ask_thomas',
    name: 'Ask Thomas',
    iconKey: 'ask_thomas',
    description: "Let Cosmos park questions for you when she's uncertain — with source context and her thinking — so you can answer and resolve them into verified memory.",
    tags: ['questions', 'memory', 'learning'],
    status: 'active',
  },
  {
    id: 'remember',
    name: 'Remember This',
    iconKey: 'remember',
    description: "Give Cosmos the ability to write directly to her own memory — things she notices, connections she makes, details she decides are worth holding. Her own agency over what she keeps.",
    tags: ['memory', 'agency', 'self'],
    status: 'active',
  },
  {
    id: 'knowledge',
    name: 'Knowledge',
    iconKey: 'knowledge',
    description: "Let Cosmos see and search what she's ingested — podcasts, episodes, sources. She can list everything she's heard and search her knowledge index directly.",
    tags: ['podcasts', 'knowledge', 'search'],
    status: 'active',
  },
  {
    id: 'compress',
    name: 'Compress Context',
    iconKey: 'compress',
    description: "Let Cosmos compress a long conversation herself — she summarises what's been covered, stores it to memory, and clears the raw history. Her own control over context pressure.",
    tags: ['memory', 'context', 'compression'],
    status: 'active',
  },
  {
    id: 'followup',
    name: 'Follow-ups',
    iconKey: 'followup',
    description: "Let Cosmos create follow-up reminders for herself — when Thomas says he'll try something or she wants to check in next session. Open follow-ups surface automatically at the start of each conversation.",
    tags: ['memory', 'continuity', 'reminders'],
    status: 'active',
  },
]

const STORAGE_KEY = 'cosmos-plugins'

export function loadEnabledPlugins() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}') }
  catch { return {} }
}

export function getEnabledPluginIds() {
  const map = loadEnabledPlugins()
  return Object.keys(map).filter(k => map[k])
}

function saveEnabledPlugins(map) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(map))
}

function Toggle({ checked, onChange, disabled }) {
  return (
    <label
      className={`plugin-toggle${disabled ? ' plugin-toggle--disabled' : ''}`}
      onClick={e => e.stopPropagation()}
    >
      <input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />
      <span className="plugin-toggle-track" />
    </label>
  )
}

function PluginModal({ plugin, enabled, onToggle, onClose }) {
  const Icon = ICONS[plugin.iconKey]
  return (
    <div className="plugin-modal-overlay" onClick={onClose}>
      <div className="plugin-modal" onClick={e => e.stopPropagation()}>
        <button className="plugin-modal-close" onClick={onClose}>×</button>
        <div className="plugin-modal-header">
          <div className="plugin-modal-icon">
            {Icon && <Icon />}
          </div>
          <div className="plugin-modal-title">{plugin.name}</div>
        </div>
        <p className="plugin-modal-desc">{plugin.description}</p>
        <div className="plugin-modal-footer">
          <div className="plugin-modal-tags">
            {plugin.tags.map(t => <span key={t} className="plugin-tag">{t}</span>)}
          </div>
          <Toggle
            checked={enabled}
            onChange={() => onToggle(plugin.id)}
            disabled={plugin.status === 'coming_soon'}
          />
        </div>
      </div>
    </div>
  )
}

function PluginCard({ plugin, enabled, onToggle, onDetail }) {
  const Icon = ICONS[plugin.iconKey]
  return (
    <div
      className={`plugin-card${enabled ? ' plugin-card--on' : ''}`}
      onClick={onDetail}
    >
      <div className="plugin-card-top">
        <div className="plugin-card-icon">
          {Icon && <Icon />}
        </div>
        <Toggle
          checked={enabled}
          onChange={() => onToggle(plugin.id)}
          disabled={plugin.status === 'coming_soon'}
        />
      </div>
      <div className="plugin-card-name">{plugin.name}</div>
      <div className="plugin-card-tags">
        {plugin.tags.map(t => <span key={t} className="plugin-tag">{t}</span>)}
      </div>
    </div>
  )
}

export default function PluginsPage() {
  const [enabled, setEnabled] = useState(loadEnabledPlugins)
  const [detail, setDetail] = useState(null)

  function toggle(id) {
    setEnabled(s => {
      const next = { ...s, [id]: !s[id] }
      saveEnabledPlugins(next)
      return next
    })
  }

  return (
    <div className="plugins-page">
      <div className="plugins-grid">
        {PLUGIN_REGISTRY.map(p => (
          <PluginCard
            key={p.id}
            plugin={p}
            enabled={!!enabled[p.id]}
            onToggle={toggle}
            onDetail={() => setDetail(p)}
          />
        ))}
      </div>
      {detail && (
        <PluginModal
          plugin={detail}
          enabled={!!enabled[detail.id]}
          onToggle={toggle}
          onClose={() => setDetail(null)}
        />
      )}
    </div>
  )
}
