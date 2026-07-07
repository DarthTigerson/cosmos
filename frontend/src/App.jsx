import { useState, useRef, useEffect, useCallback } from 'react'
import Markdown from 'react-markdown'
import Settings, { initSettings } from './Settings.jsx'
import PluginsPage, { getEnabledPluginIds } from './Plugins.jsx'
import PodcastApp from './PodcastApp.jsx'
import QuestionsApp from './QuestionsApp.jsx'
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

function useModelStatus() {
  const [available, setAvailable] = useState(false)
  useEffect(() => {
    async function check() {
      try {
        const res = await fetch('/model/status', { signal: AbortSignal.timeout(2000) })
        const data = await res.json()
        setAvailable(!!data.available)
      } catch { setAvailable(false) }
    }
    check()
    const id = setInterval(check, 10000)
    return () => clearInterval(id)
  }, [])
  return available
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
  const sysUsed = fmtGB(mem.system_used_bytes)
  const total = fmtGB(mem.total_bytes)
  const cosmos = fmtGB(mem.cosmos_bytes)
  const sysPct = mem.total_bytes > 0 ? Math.round((mem.system_used_bytes / mem.total_bytes) * 100) : 0
  return (
    <div className="memory-widget">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <rect x="2" y="6" width="20" height="12" rx="2"/>
        <path d="M6 6V4M10 6V4M14 6V4M18 6V4M6 18v2M10 18v2M14 18v2M18 18v2"/>
      </svg>
      <span className="memory-text">{sysUsed} <span className="memory-sep">/</span> {total} GB</span>
      <div className="memory-popover">
        <div className="bpop-title">RAM · {sysPct}% used</div>
        <div className="bpop-divider"/>
        <div className="bpop-row">
          <span className="bpop-label">System used</span>
          <span className="bpop-val">{sysUsed} GB</span>
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
          <span className="bpop-val">{sysUsed} GB</span>
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

function Message({ msg, isLast, onRegenerate, loading }) {
  const isUser = msg.role === 'user'
  const hasThinking = msg.thinking != null && msg.thinking.length > 0
  const streamingThinking = !!msg.streamingThinking
  const hasTools = msg.toolCalls && msg.toolCalls.length > 0
  const showRegenerate = !isUser && isLast && !loading && !msg.streaming

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
      </div>
      {showRegenerate && (
        <button className="regenerate-btn" onClick={onRegenerate} title="Regenerate response">↺</button>
      )}
      {isUser && <div className="avatar user-avatar">T</div>}
    </div>
  )
}

function Sidebar({ sessions, currentId, onSelect, onNew, onDelete, view, onViewChange, pendingQuestions }) {
  return (
    <aside className="sidebar">
      <div
        className={`sidebar-nav-item${view === 'plugins' ? ' active' : ''}`}
        onClick={() => onViewChange(view === 'plugins' ? 'chat' : 'plugins')}
      >
        <span className="sidebar-nav-icon">⬡</span>
        Plugins
      </div>
      <div
        className={`sidebar-nav-item${view === 'apps' || view === 'podcasts' || view === 'questions' ? ' active' : ''}`}
        onClick={() => onViewChange(view === 'apps' || view === 'podcasts' || view === 'questions' ? 'chat' : 'apps')}
      >
        <span className="sidebar-nav-icon">⊞</span>
        Apps
        {pendingQuestions > 0 && (
          <span className="sidebar-badge">{pendingQuestions}</span>
        )}
      </div>
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
    </aside>
  )
}

const APPS_REGISTRY = [
  { id: 'podcasts', icon: '📻', name: 'Podcasts', description: 'Track episodes and ingest transcripts for Cosmos to learn from.' },
  { id: 'questions', icon: '❓', name: 'Questions', description: 'Questions Cosmos has parked for you to answer and resolve into memory.' },
]

function AppsPage({ onOpen }) {
  return (
    <div className="apps-page">
      <div className="apps-page-header">
        <span className="apps-page-icon">⊞</span>
        <h2>Apps</h2>
      </div>
      <div className="apps-grid">
        {APPS_REGISTRY.map(app => (
          <div key={app.id} className="app-card" onClick={() => onOpen(app.id)}>
            <span className="app-card-icon">{app.icon}</span>
            <div className="app-card-name">{app.name}</div>
            <div className="app-card-desc">{app.description}</div>
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
  const modelAvailable = useModelStatus()
  const battery = useBattery()
  const net = useNetwork()
  const mem = useMemory()
  const bottomRef = useRef(null)
  const inputRef = useRef(null)
  const abortRef = useRef(null)

  useEffect(() => { initSettings() }, [])

  const fetchSessions = useCallback(async () => {
    try {
      const res = await fetch('/sessions')
      setSessions(await res.json())
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

  function abortStream() {
    abortRef.current?.abort()
    abortRef.current = null
  }

  async function selectSession(id) {
    abortStream()
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
    } catch {}
  }

  function newChat() {
    abortStream()
    setSessionId(newSessionId())
    setMessages([])
    inputRef.current?.focus()
  }

  async function deleteSession(id) {
    await fetch(`/sessions/${id}`, { method: 'DELETE' })
    if (id === sessionId) newChat()
    fetchSessions()
  }

  async function send(overrideText) {
    const text = (overrideText ?? input).trim()
    if (!text || loading) return

    abortRef.current = new AbortController()

    // Optimistically add session to sidebar before backend responds
    if (!overrideText && !sessions.find(s => s.id === sessionId)) {
      setSessions(s => [{
        id: sessionId,
        title: text.slice(0, 60),
        updated_at: new Date().toISOString().replace('Z', ''),
      }, ...s])
    }

    setMessages(m => [
      ...(overrideText ? m : [...m, { role: 'user', content: text }]),
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
          ? [...m.slice(0, -1), { ...last, streaming: false }]
          : m
      })
      fetchSessions()
      inputRef.current?.focus()
    }
  }

  async function regenerate() {
    // Find the last user message
    const lastUser = [...messages].reverse().find(m => m.role === 'user')
    if (!lastUser) return
    // Drop the last assistant message and re-send
    setMessages(m => m.slice(0, -1))
    await send(lastUser.content)
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  return (
    <div className="layout">
      <Sidebar
        sessions={sessions}
        currentId={sessionId}
        onSelect={selectSession}
        onNew={newChat}
        onDelete={deleteSession}
        view={view}
        onViewChange={setView}
        pendingQuestions={pendingQuestions}
      />
      <div className="app">
        <header className="header">
          <div className="header-title">
            <span className={`cosmos-glyph${loading ? ' thinking' : modelAvailable ? ' idle' : ' down'}`}>◈</span>
            Cosmos
          </div>
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
        ) : (
          <>
            <main className="messages">
              {messages.length === 0 && (
                <div className="empty-state">
                  <div className={`cosmos-glyph empty-glyph${loading ? ' thinking' : modelAvailable ? ' idle' : ' down'}`}>◈</div>
                  <p>Ready when you are.</p>
                </div>
              )}
              {messages.map((m, i) => (
            <Message
              key={i}
              msg={m}
              isLast={i === messages.length - 1}
              onRegenerate={regenerate}
              loading={loading}
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
                ? <button className="send-btn stop-btn" onClick={abortStream} title="Stop"><IconStop /></button>
                : <button className="send-btn" onClick={send} disabled={!input.trim()} title="Send"><IconSend /></button>
              }
            </footer>
          </>
        )}
      </div>
    </div>
  )
}
