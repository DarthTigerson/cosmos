import { useState } from 'react'

export const PLUGIN_REGISTRY = [
  {
    id: 'superpowers',
    name: 'Superpowers',
    icon: '⚡',
    description: 'Give Cosmos the ability to run shell commands, read and write files, and execute Python — all on your machine.',
    tags: ['shell', 'files', 'python'],
    status: 'active',
  },
  {
    id: 'wikisearch',
    name: 'WikiSearch',
    icon: '📖',
    description: 'Let Cosmos search and read Wikipedia articles to answer factual questions with up-to-date, sourced information.',
    tags: ['wikipedia', 'search', 'knowledge'],
    status: 'active',
  },
  {
    id: 'ask_thomas',
    name: 'Ask Thomas',
    icon: '❓',
    description: 'Let Cosmos park questions for you when she\'s uncertain — with source context and her thinking — so you can answer and resolve them into verified memory.',
    tags: ['questions', 'memory', 'learning'],
    status: 'active',
  },
  {
    id: 'remember',
    name: 'Remember This',
    icon: '🧠',
    description: 'Give Cosmos the ability to write directly to her own memory — things she notices, connections she makes, details she decides are worth holding. Her own agency over what she keeps.',
    tags: ['memory', 'agency', 'self'],
    status: 'active',
  },
  {
    id: 'knowledge',
    name: 'Knowledge',
    icon: '📚',
    description: 'Let Cosmos see and search what she\'s ingested — podcasts, episodes, sources. She can list everything she\'s heard and search her knowledge index directly.',
    tags: ['podcasts', 'knowledge', 'search'],
    status: 'active',
  },
  {
    id: 'compress',
    name: 'Compress Context',
    icon: '🗜️',
    description: 'Let Cosmos compress a long conversation herself — she summarises what\'s been covered, stores it to memory, and clears the raw history. Her own control over context pressure.',
    tags: ['memory', 'context', 'compression'],
    status: 'active',
  },
  {
    id: 'followup',
    name: 'Follow-ups',
    icon: '🔁',
    description: 'Let Cosmos create follow-up reminders for herself — when Thomas says he\'ll try something or she wants to check in next session. Open follow-ups surface automatically at the start of each conversation.',
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

function PluginCard({ plugin, enabled, onToggle }) {
  return (
    <div className={`plugin-card${enabled ? ' plugin-card--on' : ''}`}>
      <div className="plugin-card-top">
        <span className="plugin-icon">{plugin.icon}</span>
        <div className="plugin-meta">
          <div className="plugin-name">{plugin.name}</div>
          <div className="plugin-tags">
            {plugin.tags.map(t => (
              <span key={t} className="plugin-tag">{t}</span>
            ))}
          </div>
        </div>
        <div className="plugin-right">
          {plugin.status === 'coming_soon' && (
            <span className="plugin-badge">Soon</span>
          )}
          <label className={`plugin-toggle${plugin.status === 'coming_soon' ? ' plugin-toggle--disabled' : ''}`}>
            <input
              type="checkbox"
              checked={enabled}
              disabled={plugin.status === 'coming_soon'}
              onChange={() => plugin.status !== 'coming_soon' && onToggle(plugin.id)}
            />
            <span className="plugin-toggle-track" />
          </label>
        </div>
      </div>
      <p className="plugin-description">{plugin.description}</p>
    </div>
  )
}

export default function PluginsPage() {
  const [enabled, setEnabled] = useState(loadEnabledPlugins)

  function toggle(id) {
    setEnabled(s => {
      const next = { ...s, [id]: !s[id] }
      saveEnabledPlugins(next)
      return next
    })
  }

  return (
    <div className="plugins-page">
      <div className="plugins-hero">
        <div className="plugins-hero-title">Plugins</div>
        <div className="plugins-hero-sub">Extend what Cosmos can do on your machine</div>
      </div>
      <div className="plugins-list">
        {PLUGIN_REGISTRY.map(p => (
          <PluginCard
            key={p.id}
            plugin={p}
            enabled={!!enabled[p.id]}
            onToggle={toggle}
          />
        ))}
      </div>
    </div>
  )
}
