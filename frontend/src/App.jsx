import { useState, useRef, useEffect, useCallback } from 'react'
import Markdown from 'react-markdown'
import Settings, { initSettings } from './Settings.jsx'
import PluginsPage, { getEnabledPluginIds } from './Plugins.jsx'
import PodcastApp from './PodcastApp.jsx'
import QuestionsApp from './QuestionsApp.jsx'
import CodeApp from './CodeApp.jsx'
import './App.css'

function newSessionId() {
  return 'cosmos-' + Math.random().toString(36).slice(2, 8)
}

function IconSettings() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3"/>
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
    </svg>
  )
}

function IconSend() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="12" y1="19" x2="12" y2="5"/>
      <polyline points="5 12 12 5 19 12"/>
    </svg>
  )
}

function IconStop() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <rect x="3" y="3" width="18" height="18" rx="3"/>
    </svg>
  )
}

function IconDots() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <circle cx="5" cy="12" r="2"/>
      <circle cx="12" cy="12" r="2"/>
      <circle cx="19" cy="12" r="2"/>
    </svg>
  )
}

function useModelStatus() {
  const [modelState, setModelState] = useState('unknown')

  useEffect(() => {
    async function check() {
      try {
        const res = await fetch('/model/status', { signal: AbortSignal.timeout(3000) })
        const data = await res.json()
        const s = data.status
        if (s === 'running') setModelState('running')
        else if (s?.startsWith('standby')) setModelState('sleeping')
        else if (s === 'loading' || s === 'starting') setModelState('loading')
        else if (s === 'down') setModelState('down')
        else setModelState(data.available ? 'running' : 'down')
      } catch { setModelState('down') }
    }
    check()
    const id = setInterval(check, 5000)
    return () => clearInterval(id)
  }, [])

  async function sleepModel() {
    setModelState('sleeping')
    await fetch('/model/sleep', { method: 'POST' }).catch(() => {})
  }

  async function wakeModel() {
    setModelState('loading')
    await fetch('/model/wake', { method: 'POST' }).catch(() => {})
  }

  async function stopModel() {
    setModelState('down')
    await fetch('/model/stop', { method: 'POST' }).catch(() => {})
  }

  return { modelState, sleepModel, wakeModel, stopModel }
}

function useNetwork() {
  const [net, setNet] = useState(null)
  useEffect(() => {
    async function fetch_() {
      try {
        const res = await fetch('/network')
        setNet(await res.json())
      } catch { setNet(null) }
    }
    fetch_()
    const id = setInterval(fetch_, 30000)
    return () => clearInterval(id)
  }, [])
  return net
}

function WifiWidget({ net }) {
  if (!net) return null
  const { wifi, ssid, ip } = net
  return (
    <div className={`wifi-widget${wifi ? '' : ' wifi-widget--off'}`}>
      <svg width="18" height="14" viewBox="0 0 24 20" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M1.42 8a16 16 0 0 1 21.16 0"/>
        <path d="M5 12.55a11 11 0 0 1 14.08 0"/>
        <path d="M8.53 16.11a6 6 0 0 1 6.95 0"/>
        <circle cx="12" cy="20" r="1" fill="currentColor" stroke="none"/>
      </svg>
      <div className="wifi-popover">
        <div className="bpop-title">{ssid || (wifi ? 'Connected' : 'Not connected')}</div>
        {ip && (
          <>
            <div className="bpop-divider"/>
            <div className="bpop-row">
              <span className="bpop-label">IP</span>
              <span className="bpop-val">{ip}</span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

function useBattery() {
  const [battery, setBattery] = useState(null)
  useEffect(() => {
    async function fetch_() {
      try {
        const res = await fetch('/battery')
        const data = await res.json()
        if (data.level != null) setBattery(data)
        else setBattery(null)
      } catch { setBattery(null) }
    }
    fetch_()
    const id = setInterval(fetch_, 30000)
    return () => clearInterval(id)
  }, [])
  return battery
}

function useMemory() {
  const [mem, setMem] = useState(null)
  useEffect(() => {
    async function fetch_() {
      try {
        const res = await fetch('/memory')
        const data = await res.json()
        if (data.total_bytes > 0) setMem(data)
        else setMem(null)
      } catch { setMem(null) }
    }
    fetch_()
    const id = setInterval(fetch_, 10000)
    return () => clearInterval(id)
  }, [])
  return mem
}

function fmtGB(bytes) {
  return (bytes / 1073741824).toFixed(1)
}

function MemoryWidget({ mem }) {
  if (!mem) return null
  const totalUsed = fmtGB(mem.system_used_bytes)
  const total = fmtGB(mem.total_bytes)
  const cosmos = fmtGB(mem.cosmos_bytes)
  const otherApps = fmtGB(Math.max(0, mem.system_used_bytes - mem.cosmos_bytes))
  const sysPct = mem.total_bytes > 0 ? Math.round((mem.system_used_bytes / mem.total_bytes) * 100) : 0
  return (
    <div className="memory-widget">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <rect x="2" y="6" width="20" height="12" rx="2"/>
        <path d="M6 6V4M10 6V4M14 6V4M18 6V4M6 18v2M10 18v2M14 18v2M18 18v2"/>
      </svg>
      <span className="memory-text">{totalUsed} <span className="memory-sep">/</span> {total} GB</span>
      <div className="memory-popover">
        <div className="bpop-title">RAM · {sysPct}% used</div>
        <div className="bpop-divider"/>
        <div className="bpop-row">
          <span className="bpop-label">Other apps</span>
          <span className="bpop-val">{otherApps} GB</span>
        </div>
        <div className="bpop-divider"/>
        <div className="bpop-row">
          <span className="bpop-label">Cosmos total</span>
          <span className="bpop-val">{cosmos} GB</span>
        </div>
        {mem.model_bytes > 0 && (
          <div className="bpop-row">
            <span className="bpop-label bpop-indent">Model</span>
            <span className="bpop-val">{fmtGB(mem.model_bytes)} GB</span>
          </div>
        )}
        {mem.backend_bytes > 0 && (
          <div className="bpop-row">
            <span className="bpop-label bpop-indent">Backend</span>
            <span className="bpop-val">{fmtGB(mem.backend_bytes)} GB</span>
          </div>
        )}
        <div className="bpop-divider"/>
        <div className="bpop-row">
          <span className="bpop-label">Total used</span>
          <span className="bpop-val">{totalUsed} GB</span>
        </div>
        <div className="bpop-row">
          <span className="bpop-label">Total available</span>
          <span className="bpop-val">{total} GB</span>
        </div>
      </div>
    </div>
  )
}

function useClock() {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])
  return now
}

function ClockWidget() {
  const now = useClock()
  const time = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const date = now.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
  return (
    <div className="clock-widget">
      <span className="clock-time">{time}</span>
      <span className="clock-date">{date}</span>
    </div>
  )
}

function fmtTime(unixSec) {
  if (!unixSec) return null
  const d = new Date(unixSec * 1000)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  const tomorrow = new Date(now); tomorrow.setDate(now.getDate() + 1)
  const isTomorrow = d.toDateString() === tomorrow.toDateString()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (isToday) return time
  if (isTomorrow) return `tomorrow ${time}`
  return `${d.toLocaleDateString([], { weekday: 'short' })} ${time}`
}

function fmtTracked(seconds) {
  if (!seconds || seconds < 60) return `${seconds || 0}s`
  const m = Math.round(seconds / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60), rem = m % 60
  return rem ? `${h}h ${rem}m` : `${h}h`
}

function BatteryWidget({ battery }) {
  if (!battery) return null
  const { level, charging, time_remaining, stats } = battery
  const fillColor = level > 40 ? 'var(--battery-ok)' : level > 20 ? 'var(--battery-warn)' : 'var(--battery-low)'
  const fillW = Math.max(1, Math.round((level / 100) * 13))

  const hasStats = stats?.rate_pct_per_hour != null
  const rate = stats?.rate_pct_per_hour
  const draining = rate != null && rate < 0

  return (
    <div className="battery-widget">
      <svg width="22" height="12" viewBox="0 0 22 12" aria-hidden="true">
        <rect x="0.5" y="0.5" width="18" height="11" rx="2.5" fill="none" stroke="currentColor" strokeWidth="1.1"/>
        <rect x="19.2" y="3.5" width="2.3" height="5" rx="1" fill="currentColor"/>
        <rect x="2" y="2.5" width={fillW} height="7" rx="1.5" fill={fillColor}/>
        {charging && (
          <path d="M10.5 9 L12.5 6 L11 6 L13 3" stroke="white" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" fill="none"/>
        )}
      </svg>
      <span className="battery-pct">{level}%</span>

      <div className="battery-popover">
        <div className="bpop-title">
          {level}% · {charging ? 'Charging' : 'On battery'}
        </div>
        <div className="bpop-divider"/>
        {hasStats ? (
          <>
            <div className="bpop-row">
              <span className="bpop-label">Drain rate</span>
              <span className="bpop-val">{draining ? `${Math.abs(rate)}% / hr` : `+${rate}% / hr`}</span>
            </div>
            {time_remaining && (
              <div className="bpop-row">
                <span className="bpop-label">pmset estimate</span>
                <span className="bpop-val">{time_remaining}</span>
              </div>
            )}
            {draining && stats.predicted_empty && (
              <div className="bpop-row">
                <span className="bpop-label">Predicted empty</span>
                <span className="bpop-val">{fmtTime(stats.predicted_empty)}</span>
              </div>
            )}
            {!draining && stats.predicted_full && (
              <div className="bpop-row">
                <span className="bpop-label">Predicted full</span>
                <span className="bpop-val">{fmtTime(stats.predicted_full)}</span>
              </div>
            )}
            <div className="bpop-divider"/>
            <div className="bpop-footer">
              {stats.observations} readings · {fmtTracked(stats.tracked_seconds)}
            </div>
          </>
        ) : (
          <>
            {time_remaining && (
              <div className="bpop-row">
                <span className="bpop-label">Time left</span>
                <span className="bpop-val">{time_remaining}</span>
              </div>
            )}
            <div className="bpop-footer">
              {stats?.observations < 2
                ? 'Building history…'
                : `Tracking · ${fmtTracked(stats?.tracked_seconds)}`}
            </div>
          </>
        )}
      </div>
    </div>
  )
}


function relativeDate(iso) {
  const d = new Date(iso + 'Z')
  const diff = Date.now() - d.getTime()
  const m = Math.floor(diff / 60000)
  const h = Math.floor(diff / 3600000)
  const days = Math.floor(diff / 86400000)
  if (m < 1) return 'just now'
  if (m < 60) return `${m}m ago`
  if (h < 24) return `${h}h ago`
  if (days === 1) return 'yesterday'
  if (days < 7) return `${days}d ago`
  return d.toLocaleDateString()
}

function ThinkingBlock({ content, streamingPhase }) {
  const [open, setOpen] = useState(false)
  const contentRef = useRef(null)

  useEffect(() => {
    if (open && contentRef.current)
      contentRef.current.scrollTop = contentRef.current.scrollHeight
  }, [content, open])

  return (
    <div className="thinking-block">
      <button className="thinking-toggle" onClick={() => setOpen(o => !o)}>
        <span className={`thinking-icon${streamingPhase ? ' spinning' : ''}`}>◈</span>
        <span className="thinking-label">
          {streamingPhase ? 'Thinking…' : 'Reasoning'}
        </span>
        <span className={`thinking-chevron${open ? ' open' : ''}`}>›</span>
      </button>
      {open && (
        <pre className="thinking-content" ref={contentRef}>
          {content}
          {streamingPhase && <span className="cursor-inline" />}
        </pre>
      )}
    </div>
  )
}

function ToolCall({ call }) {
  const [open, setOpen] = useState(false)
  const done = call.result != null

  return (
    <div className={`tool-block${done ? ' tool-block--done' : ' tool-block--running'}`}>
      <button className="tool-block-header" onClick={() => done && setOpen(o => !o)}>
        <span className="tool-block-icon">⚡</span>
        <span className="tool-block-name">{call.name}</span>
        {Object.entries(call.args).map(([k, v]) => (
          <span key={k} className="tool-arg-inline">
            <span className="tool-arg-key">{k}:</span>
            <span className="tool-arg-val">{String(v).slice(0, 60)}</span>
          </span>
        ))}
        <span className="tool-block-spacer" />
        {done
          ? <span className="tool-block-status">✓</span>
          : <span className="tool-block-status tool-block-status--spin">◈</span>
        }
        {done && <span className={`thinking-chevron${open ? ' open' : ''}`}>›</span>}
      </button>
      {open && done && (
        <pre className="tool-block-result">{call.result}</pre>
      )}
    </div>
  )
}

function ToolBlock({ calls }) {
  return (
    <div className="tool-blocks">
      {calls.map((call, i) => <ToolCall key={i} call={call} />)}
    </div>
  )
}

function Message({ msg, onRetry }) {
  const isUser = msg.role === 'user'
  const hasThinking = msg.thinking != null && msg.thinking.length > 0
  const streamingThinking = !!msg.streamingThinking
  const hasTools = msg.toolCalls && msg.toolCalls.length > 0
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(msg.content || '').then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  const showActions = !msg.streaming && msg.content

  return (
    <div className={`message ${isUser ? 'user' : 'cosmos'}`}>
      {!isUser && <div className="avatar">◈</div>}
      <div className="bubble-wrap">
        {hasThinking && <ThinkingBlock content={msg.thinking} streamingPhase={streamingThinking} />}
        {hasTools && <ToolBlock calls={msg.toolCalls} />}
        {!streamingThinking && (msg.content || !msg.streaming) && (
          <div className="bubble">
            <Markdown>{msg.content?.trimStart()}</Markdown>
            {msg.streaming && <span className="cursor-inline" />}
          </div>
        )}
        {showActions && (
          <div className="msg-actions">
            <button className="msg-action-btn" onClick={handleCopy} title={copied ? 'Copied!' : 'Copy'}>
              {copied
                ? <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                : <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
              }
            </button>
            <button className="msg-action-btn" onClick={onRetry} title="Retry">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
            </button>
          </div>
        )}
      </div>
      {isUser && <div className="avatar user-avatar">T</div>}
    </div>
  )
}

function ModelMenu({ modelState, onSleep, onWake, onStop }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    function handleClick(e) {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [open])

  const sleeping = modelState === 'sleeping'
  const running = modelState === 'running'
  const loading = modelState === 'loading'

  const stateLabel = running ? 'Running' : sleeping ? 'Sleeping' : loading ? 'Loading…' : 'Offline'
  const stateDot = running ? 'dot--running' : sleeping ? 'dot--sleeping' : loading ? 'dot--loading' : 'dot--down'

  return (
    <div className="model-menu" ref={ref}>
      <button className="model-menu-btn" onClick={() => setOpen(o => !o)} title="Model options">
        <IconDots />
      </button>
      {open && (
        <div className="model-menu-dropdown">
          <div className="model-menu-status">
            <span className={`model-menu-dot ${stateDot}`} />
            <span className="model-menu-status-label">{stateLabel}</span>
          </div>
          <div className="model-menu-divider" />
          <button
            className="model-menu-item"
            disabled={!sleeping && !running}
            onClick={() => { setOpen(false); sleeping ? onWake() : onSleep() }}
          >
            {sleeping ? '↑ Wake model' : '↓ Sleep model'}
          </button>
          <div className="model-menu-divider" />
          <button
            className="model-menu-item model-menu-item--danger"
            disabled={modelState === 'down'}
            onClick={() => { setOpen(false); onStop() }}
          >
            ⏻ Shut down
          </button>
          <div className="model-menu-note">fully unloads from memory</div>
        </div>
      )}
    </div>
  )
}

function Sidebar({ sessions, currentId, onSelect, onNew, onDelete, view, onViewChange, pendingQuestions, glyphClass, modelState, onSleep, onWake, onStop, codeProject, codeTree, codeExpandedDirs, codeGitStatus, codeChangedPaths, onCodeFileOpen, onCodeToggleDir, onCodeChangeProject }) {
  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        <span className={`cosmos-glyph ${glyphClass}`}>◈</span>
        <span className="sidebar-brand-name">Cosmos</span>
        <ModelMenu modelState={modelState} onSleep={onSleep} onWake={onWake} onStop={onStop} />
      </div>
      <div
        className={`sidebar-nav-item${view === 'chat' ? ' active' : ''}`}
        onClick={() => onViewChange('chat')}
      >
        <span className="sidebar-nav-icon">◻</span>
        Chat
      </div>
      <div
        className={`sidebar-nav-item${view === 'apps' || view === 'podcasts' || view === 'questions' || view === 'code' ? ' active' : ''}`}
        onClick={() => onViewChange(view === 'apps' || view === 'podcasts' || view === 'questions' || view === 'code' ? 'chat' : 'apps')}
      >
        <span className="sidebar-nav-icon">⊞</span>
        Apps
        {pendingQuestions > 0 && (
          <span className="sidebar-badge">{pendingQuestions}</span>
        )}
      </div>
      <div
        className={`sidebar-nav-item${view === 'plugins' ? ' active' : ''}`}
        onClick={() => onViewChange(view === 'plugins' ? 'chat' : 'plugins')}
      >
        <span className="sidebar-nav-icon">⬡</span>
        Plugins
      </div>
      {view === 'chat' && (
        <>
          <div className="sidebar-divider" />
          <div className="session-list">
            {sessions.length === 0 && (
              <div className="session-empty">No previous chats yet.</div>
            )}
            {sessions.map(s => (
              <div
                key={s.id}
                className={`session-item${s.id === currentId ? ' active' : ''}`}
                onClick={() => onSelect(s.id)}
              >
                <div className="session-title">{s.title}</div>
                <div className="session-meta">{relativeDate(s.updated_at)}</div>
                <button
                  className="session-delete"
                  onClick={e => { e.stopPropagation(); onDelete(s.id) }}
                  title="Delete"
                >
                  ×
                </button>
              </div>
            ))}
          </div>
          <div className="sidebar-header">
            <button className="new-chat-btn" onClick={onNew}>
              <span>+</span> New chat
            </button>
          </div>
        </>
      )}
      {view === 'code' && (
        <>
          <div className="sidebar-divider" />
          {codeProject ? (
            <>
              <div className="sidebar-code-info">
                <span className="sidebar-code-name">{codeProject.name}</span>
                {codeGitStatus?.branch && (
                  <span className="sidebar-code-branch">
                    <IconGitBranch /> {codeGitStatus.branch}
                  </span>
                )}
              </div>
              <div className="session-list" style={{ flex: 1 }}>
                {!codeTree && <div className="session-empty">Loading…</div>}
                {codeTree?.children?.map(node => (
                  <SidebarFileNode
                    key={node.path}
                    node={node}
                    onOpen={onCodeFileOpen}
                    expandedDirs={codeExpandedDirs}
                    onToggle={onCodeToggleDir}
                    changedPaths={codeChangedPaths}
                  />
                ))}
              </div>
            </>
          ) : (
            <div className="session-empty" style={{ padding: '16px 12px' }}>No project open.</div>
          )}
          <div className="sidebar-header">
            <button className="new-chat-btn" onClick={onCodeChangeProject}>
              <span>⊞</span> {codeProject ? 'Change project' : 'Open project'}
            </button>
          </div>
        </>
      )}
    </aside>
  )
}

// ── Code file tree helpers ────────────────────────────────────────────────────

function SidebarFileNode({ node, onOpen, expandedDirs, onToggle, changedPaths, depth = 0 }) {
  const isOpen = expandedDirs.has(node.path)
  const isModified = changedPaths?.has(node.path)

  if (node.type === 'file') {
    return (
      <div
        className={`ftree-file${isModified ? ' ftree-modified' : ''}`}
        style={{ paddingLeft: 10 + depth * 12 }}
        onClick={() => onOpen(node.path)}
        title={node.path}
      >
        <span className="ftree-name">{node.name}</span>
        {isModified && <span className="ftree-dot" />}
      </div>
    )
  }

  return (
    <div>
      <div
        className="ftree-dir"
        style={{ paddingLeft: 6 + depth * 12 }}
        onClick={() => onToggle(node.path)}
      >
        <span className="ftree-chevron" style={{ transform: isOpen ? 'rotate(90deg)' : 'none', transition: 'transform 0.12s', display: 'inline-block', fontSize: 9, marginRight: 3 }}>›</span>
        <span className="ftree-name">{node.name}</span>
      </div>
      {isOpen && node.children?.map(child => (
        <SidebarFileNode
          key={child.path}
          node={child}
          onOpen={onOpen}
          expandedDirs={expandedDirs}
          onToggle={onToggle}
          changedPaths={changedPaths}
          depth={depth + 1}
        />
      ))}
    </div>
  )
}

function IconGitBranch() {
  return (
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="6" y1="3" x2="6" y2="15"/>
      <circle cx="18" cy="6" r="3"/>
      <circle cx="6" cy="18" r="3"/>
      <path d="M18 9a9 9 0 0 1-9 9"/>
    </svg>
  )
}

function CodeProjectModal({ projects, currentProject, onSelect, onAdd, onRemove, onClose }) {
  const [name, setName] = useState('')
  const [path, setPath] = useState('')

  function handleAdd() {
    if (!name.trim() || !path.trim()) return
    onAdd(name.trim(), path.trim())
    setName('')
    setPath('')
  }

  return (
    <div className="code-modal-overlay" onClick={onClose}>
      <div className="code-modal" onClick={e => e.stopPropagation()}>
        <div className="code-modal-header">
          <span className="code-modal-title">Projects</span>
          <button className="code-modal-close" onClick={onClose}>×</button>
        </div>
        <div className="code-modal-projects">
          {projects.length === 0 && (
            <div className="code-modal-empty">No projects yet. Add one below.</div>
          )}
          {projects.map((p, i) => (
            <div
              key={i}
              className={`code-modal-project${currentProject?.path === p.path ? ' active' : ''}`}
              onClick={() => onSelect(p)}
            >
              <div className="code-modal-pname">{p.name}</div>
              <div className="code-modal-ppath">{p.path}</div>
              <button
                className="code-modal-premove"
                onClick={e => { e.stopPropagation(); onRemove(i) }}
              >×</button>
            </div>
          ))}
        </div>
        <div className="code-modal-add">
          <input className="code-modal-input" placeholder="Project name" value={name} onChange={e => setName(e.target.value)} />
          <input className="code-modal-input" placeholder="/path/to/project" value={path} onChange={e => setPath(e.target.value)} onKeyDown={e => e.key === 'Enter' && handleAdd()} />
          <button className="code-modal-addbtn" onClick={handleAdd}>Add project</button>
        </div>
      </div>
    </div>
  )
}

function IconPodcast() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="2"/>
      <path d="M16.24 7.76a6 6 0 0 1 0 8.49m-8.48-.01a6 6 0 0 1 0-8.49m11.31-2.82a10 10 0 0 1 0 14.14m-14.14 0a10 10 0 0 1 0-14.14"/>
    </svg>
  )
}

function IconQuestions() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10"/>
      <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/>
      <line x1="12" y1="17" x2="12.01" y2="17"/>
    </svg>
  )
}

function IconCode() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 18 22 12 16 6"/>
      <polyline points="8 6 2 12 8 18"/>
    </svg>
  )
}

const APPS_REGISTRY = [
  { id: 'podcasts', Icon: IconPodcast, name: 'Podcasts', description: 'Track episodes and ingest transcripts for Cosmos to learn from.' },
  { id: 'questions', Icon: IconQuestions, name: 'Questions', description: 'Questions Cosmos has parked for you to answer and resolve into memory.' },
  { id: 'code', Icon: IconCode, name: 'Code', description: 'Code alongside Cosmos — file tree, editor, terminal, and git in one place.' },
]

function AppsPage({ onOpen }) {
  return (
    <div className="apps-page">
      <div className="apps-grid">
        {APPS_REGISTRY.map(({ id, Icon, name, description }) => (
          <div key={id} className="app-card" onClick={() => onOpen(id)}>
            <div className="app-card-icon"><Icon /></div>
            <div className="app-card-name">{name}</div>
            <div className="app-card-desc">{description}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

export default function App() {
  const [sessionId, setSessionId] = useState(newSessionId)
  const [sessions, setSessions] = useState([])
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
  const [view, setView] = useState('chat')
  const [pendingQuestions, setPendingQuestions] = useState(0)
  const { modelState, sleepModel, wakeModel, stopModel } = useModelStatus()
  const battery = useBattery()
  const net = useNetwork()
  const mem = useMemory()
  const bottomRef = useRef(null)
  const inputRef = useRef(null)
  const abortRef = useRef(null)

  // ── Code state ──────────────────────────────────────────────────────────────
  const [codeProjects, setCodeProjects] = useState([])
  const [codeProject, setCodeProject] = useState(null)
  const [codeTree, setCodeTree] = useState(null)
  const [codeExpandedDirs, setCodeExpandedDirs] = useState(new Set())
  const [codeGitStatus, setCodeGitStatus] = useState(null)
  const [codeShowModal, setCodeShowModal] = useState(false)
  const [codeFileToOpen, setCodeFileToOpen] = useState(null)

  useEffect(() => {
    fetch('/code/projects').then(r => r.json()).then(setCodeProjects).catch(() => {})
  }, [])

  const refreshCodeTree = useCallback(async () => {
    if (!codeProject) return
    try {
      const [tRes, gRes] = await Promise.all([
        fetch(`/code/fs/tree?root=${encodeURIComponent(codeProject.path)}`),
        fetch(`/code/git/status?root=${encodeURIComponent(codeProject.path)}`),
      ])
      setCodeTree(await tRes.json())
      setCodeGitStatus(await gRes.json())
    } catch {}
  }, [codeProject])

  useEffect(() => {
    if (!codeProject) return
    refreshCodeTree()
    const id = setInterval(refreshCodeTree, 15000)
    return () => clearInterval(id)
  }, [codeProject, refreshCodeTree])

  async function codeSelectProject(p) {
    setCodeProject(p)
    setCodeShowModal(false)
    setCodeTree(null)
    setCodeGitStatus(null)
    setCodeExpandedDirs(new Set([p.path]))
  }

  async function codeAddProject(name, path) {
    await fetch('/code/projects', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, path }),
    })
    const list = await fetch('/code/projects').then(r => r.json())
    setCodeProjects(list)
  }

  async function codeRemoveProject(idx) {
    await fetch(`/code/projects/${idx}`, { method: 'DELETE' })
    const list = await fetch('/code/projects').then(r => r.json())
    setCodeProjects(list)
    if (codeProject && !list.find(p => p.path === codeProject.path)) {
      setCodeProject(null)
    }
  }

  function codeToggleDir(path) {
    setCodeExpandedDirs(s => {
      const next = new Set(s)
      next.has(path) ? next.delete(path) : next.add(path)
      return next
    })
  }

  const codeChangedPaths = new Set(
    (codeGitStatus?.files || []).map(f => `${codeProject?.path}/${f.file}`)
  )

  useEffect(() => { initSettings() }, [])

  const fetchSessions = useCallback(async () => {
    try {
      const res = await fetch('/sessions')
      const all = await res.json()
      setSessions(all.filter(s => !s.id.startsWith('code:')))
    } catch {}
  }, [])

  const fetchPendingQuestions = useCallback(async () => {
    try {
      const res = await fetch('/questions/count')
      const data = await res.json()
      setPendingQuestions(data.pending || 0)
    } catch {}
  }, [])

  useEffect(() => {
    fetchPendingQuestions()
    const id = setInterval(fetchPendingQuestions, 15000)
    return () => clearInterval(id)
  }, [fetchPendingQuestions])

  useEffect(() => { fetchSessions() }, [fetchSessions])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // Close the SSE client only — backend generation keeps running
  function disconnectStream() {
    abortRef.current?.abort()
    abortRef.current = null
  }

  // Stop button: cancel backend task AND close SSE client
  function stopGeneration() {
    fetch(`/sessions/${sessionId}/generation`, { method: 'DELETE' }).catch(() => {})
    disconnectStream()
  }

  async function selectSession(id) {
    disconnectStream()
    setView('chat')
    try {
      const res = await fetch(`/sessions/${id}/history`)
      const data = await res.json()
      setSessionId(id)
      setMessages(data.history.map(m => ({
        role: m.role,
        content: m.content,
        thinking: m.thinking || null,
        toolCalls: m.tool_calls || null,
      })))
      // Reconnect if a generation is still running for this session
      const activeRes = await fetch(`/sessions/${id}/active`)
      const activeData = await activeRes.json()
      if (activeData.active) resume(id)
    } catch {}
  }

  function newChat() {
    disconnectStream()
    setSessionId(newSessionId())
    setMessages([])
    inputRef.current?.focus()
  }

  async function deleteSession(id) {
    await fetch(`/sessions/${id}`, { method: 'DELETE' })
    if (id === sessionId) newChat()
    fetchSessions()
  }

  async function resume(id) {
    setMessages(m => [...m, { role: 'assistant', content: '', thinking: null, toolCalls: [], streaming: true }])
    setLoading(true)
    abortRef.current = new AbortController()
    try {
      const res = await fetch(`/sessions/${id}/stream`, { signal: abortRef.current.signal })
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = '', thinking = '', content = '', toolCalls = []
      outer: while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n'); buf = lines.pop() ?? ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          try {
            const evt = JSON.parse(line.slice(6))
            if (evt.error || evt.done) break outer
            if (evt.reasoning_delta) { thinking += evt.reasoning_delta; setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking, toolCalls, streaming: true, streamingThinking: true }]); await Promise.resolve() }
            if (evt.tool_call) { toolCalls = [...toolCalls, { name: evt.tool_call.name, args: evt.tool_call.args, result: null }]; setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false }]); await Promise.resolve() }
            if (evt.tool_result) { toolCalls = toolCalls.map(c => c.name === evt.tool_result.name && c.result === null ? { ...c, result: evt.tool_result.output } : c); setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false }]); await Promise.resolve() }
            if (evt.delta) { content += evt.delta; setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false }]); await Promise.resolve() }
          } catch { break outer }
        }
      }
    } catch (e) {
      if (e?.name !== 'AbortError') setMessages(m => m.slice(0, -1))
    } finally {
      abortRef.current = null
      setLoading(false)
      setMessages(m => { const last = m[m.length - 1]; return last?.role === 'assistant' ? [...m.slice(0, -1), { ...last, streaming: false, streamingThinking: false }] : m })
      fetchSessions()
    }
  }

  async function send(overrideText, addUserMessage = false) {
    const text = (overrideText ?? input).trim()
    if (!text || loading) return

    abortRef.current = new AbortController()

    if (!overrideText && !sessions.find(s => s.id === sessionId)) {
      setSessions(s => [{
        id: sessionId,
        title: text.slice(0, 60),
        updated_at: new Date().toISOString().replace('Z', ''),
      }, ...s])
    }

    setMessages(m => [
      ...((overrideText && !addUserMessage) ? m : [...m, { role: 'user', content: text }]),
      { role: 'assistant', content: '', thinking: null, toolCalls: [], streaming: true },
    ])
    if (!overrideText) setInput('')
    setLoading(true)

    try {
      const res = await fetch('/chat/stream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: text,
          session_id: sessionId,
          enabled_plugins: getEnabledPluginIds(),
        }),
        signal: abortRef.current.signal,
      })

      // Session is created on the backend before the first byte — update sidebar now
      fetchSessions()

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let thinking = ''
      let content = ''
      let toolCalls = []

      outer: while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() ?? ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          try {
            const evt = JSON.parse(line.slice(6))
            if (evt.error) throw new Error(evt.error)
            if (evt.done) break outer
            if (evt.reasoning_delta) {
              thinking += evt.reasoning_delta
              setMessages(m => [...m.slice(0, -1), {
                role: 'assistant', content, thinking, toolCalls, streaming: true, streamingThinking: true,
              }])
              await Promise.resolve()
            }
            if (evt.tool_call) {
              toolCalls = [...toolCalls, { name: evt.tool_call.name, args: evt.tool_call.args, result: null }]
              setMessages(m => [...m.slice(0, -1), {
                role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false,
              }])
              await Promise.resolve()
            }
            if (evt.tool_result) {
              toolCalls = toolCalls.map(c =>
                c.name === evt.tool_result.name && c.result === null
                  ? { ...c, result: evt.tool_result.output }
                  : c
              )
              setMessages(m => [...m.slice(0, -1), {
                role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false,
              }])
              await Promise.resolve()
            }
            if (evt.delta) {
              content += evt.delta
              setMessages(m => [...m.slice(0, -1), {
                role: 'assistant', content, thinking: thinking || null, toolCalls, streaming: true, streamingThinking: false,
              }])
              await Promise.resolve()
            }
          } catch { break outer }
        }
      }
    } catch (e) {
      if (e?.name !== 'AbortError') {
        setMessages(m => [
          ...m.slice(0, -1),
          { role: 'assistant', content: 'Something went wrong. Is vmlx running?', thinking: null },
        ])
      }
    } finally {
      abortRef.current = null
      setLoading(false)
      setMessages(m => {
        const last = m[m.length - 1]
        return last?.role === 'assistant'
          ? [...m.slice(0, -1), { ...last, streaming: false, streamingThinking: false }]
          : m
      })
      fetchSessions()
      inputRef.current?.focus()
    }
  }

  async function retryFrom(index) {
    if (loading) return
    const msg = messages[index]
    if (msg.role === 'user') {
      setMessages(m => m.slice(0, index))
      await send(msg.content, true)
    } else {
      const prevUser = [...messages.slice(0, index)].reverse().find(m => m.role === 'user')
      if (!prevUser) return
      setMessages(m => m.slice(0, index))
      await send(prevUser.content)
    }
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  return (
    <div className="layout">
      <Sidebar
        sessions={sessions}
        currentId={view === 'chat' ? sessionId : null}
        onSelect={selectSession}
        onNew={newChat}
        onDelete={deleteSession}
        view={view}
        onViewChange={setView}
        pendingQuestions={pendingQuestions}
        glyphClass={loading ? 'thinking' : modelState === 'running' ? 'idle' : modelState === 'loading' ? 'waking' : modelState === 'sleeping' ? 'sleeping' : 'down'}
        modelState={modelState}
        onSleep={sleepModel}
        onWake={wakeModel}
        onStop={stopModel}
        codeProject={codeProject}
        codeTree={codeTree}
        codeExpandedDirs={codeExpandedDirs}
        codeGitStatus={codeGitStatus}
        codeChangedPaths={codeChangedPaths}
        onCodeFileOpen={path => setCodeFileToOpen(path)}
        onCodeToggleDir={codeToggleDir}
        onCodeChangeProject={() => setCodeShowModal(true)}
      />
      <div className="app">
        <header className="header">
          {(view === 'plugins' || view === 'apps') && <span className="header-title">{view === 'plugins' ? 'Plugins' : 'Apps'}</span>}
          {view === 'code' && <span className="header-title">Code</span>}
          <div className="header-right">
            <button className="settings-btn" onClick={() => setShowSettings(true)} title="Settings">
              <IconSettings />
            </button>
            <MemoryWidget mem={mem} />
            <WifiWidget net={net} />
            <BatteryWidget battery={battery} />
            <ClockWidget />
          </div>
        </header>
        {showSettings && <Settings onClose={() => setShowSettings(false)} />}

        {view === 'plugins' ? (
          <PluginsPage />
        ) : view === 'apps' ? (
          <AppsPage onOpen={setView} />
        ) : view === 'podcasts' ? (
          <PodcastApp onBack={() => setView('apps')} />
        ) : view === 'questions' ? (
          <QuestionsApp onBack={() => setView('apps')} />
        ) : view === 'code' ? (
          <CodeApp
            project={codeProject}
            fileToOpen={codeFileToOpen}
            onFileToOpenHandled={() => setCodeFileToOpen(null)}
            onRefreshTree={refreshCodeTree}
          />
        ) : (
          <>
            <main className="messages">
              {messages.length === 0 && (
                <div className="empty-state">
                  <div className={`cosmos-glyph empty-glyph${loading ? ' thinking' : modelState === 'running' ? ' idle' : modelState === 'loading' ? ' waking' : modelState === 'sleeping' ? ' sleeping' : ' down'}`}>◈</div>
                  <p>Ready when you are.</p>
                </div>
              )}
              {messages.map((m, i) => (
                <Message
                  key={i}
                  msg={m}
                  onRetry={() => retryFrom(i)}
                />
              ))}
              <div ref={bottomRef} />
            </main>

            <footer className="input-area">
              <textarea
                ref={inputRef}
                className="input"
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={onKey}
                placeholder="Talk to Cosmos…"
                rows={1}
                disabled={loading}
              />
              {loading
                ? <button className="send-btn stop-btn" onClick={stopGeneration} title="Stop"><IconStop /></button>
                : <button className="send-btn" onClick={send} disabled={!input.trim()} title="Send"><IconSend /></button>
              }
            </footer>
          </>
        )}
      </div>
      {codeShowModal && (
        <CodeProjectModal
          projects={codeProjects}
          currentProject={codeProject}
          onSelect={codeSelectProject}
          onAdd={codeAddProject}
          onRemove={codeRemoveProject}
          onClose={() => setCodeShowModal(false)}
        />
      )}
    </div>
  )
}
