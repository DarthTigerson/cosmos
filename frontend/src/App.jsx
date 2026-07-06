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
  const [connected, setConnected] = useState(false)
  const [pendingQuestions, setPendingQuestions] = useState(0)
  const bottomRef = useRef(null)
  const inputRef = useRef(null)
  const abortRef = useRef(null)

  useEffect(() => { initSettings() }, [])

  useEffect(() => {
    async function checkHealth() {
      try {
        const res = await fetch('/health', { signal: AbortSignal.timeout(2500) })
        setConnected(res.ok)
      } catch {
        setConnected(false)
      }
    }
    checkHealth()
    const id = setInterval(checkHealth, 5000)
    return () => clearInterval(id)
  }, [])

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
            <span className={`cosmos-glyph${loading ? ' fast' : connected ? ' slow' : ' offline'}`}>◈</span>
            Cosmos
          </div>
          <button className="settings-btn" onClick={() => setShowSettings(true)} title="Settings">⚙</button>
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
                  <div className={`cosmos-glyph empty-glyph${loading ? ' fast' : connected ? ' slow' : ' offline'}`}>◈</div>
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
                ? <button className="send-btn stop-btn" onClick={abortStream}>■</button>
                : <button className="send-btn" onClick={send} disabled={!input.trim()}>↑</button>
              }
            </footer>
          </>
        )}
      </div>
    </div>
  )
}
