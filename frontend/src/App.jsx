import { useState, useRef, useEffect, useCallback } from 'react'
import Markdown from 'react-markdown'
import Settings, { initSettings } from './Settings.jsx'
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

function Message({ msg }) {
  const isUser = msg.role === 'user'
  const hasThinking = msg.thinking != null && msg.thinking.length > 0
  const streamingThinking = !!msg.streamingThinking

  return (
    <div className={`message ${isUser ? 'user' : 'cosmos'}`}>
      {!isUser && <div className="avatar">◈</div>}
      <div className="bubble-wrap">
        {hasThinking && <ThinkingBlock content={msg.thinking} streamingPhase={streamingThinking} />}
        {!streamingThinking && (
          <div className="bubble">
            <Markdown>{msg.content?.trimStart()}</Markdown>
            {msg.streaming && <span className="cursor-inline" />}
          </div>
        )}
      </div>
      {isUser && <div className="avatar user-avatar">T</div>}
    </div>
  )
}

function Sidebar({ sessions, currentId, onSelect, onNew, onDelete }) {
  return (
    <aside className="sidebar">
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

export default function App() {
  const [sessionId, setSessionId] = useState(newSessionId)
  const [sessions, setSessions] = useState([])
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
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

  async function send() {
    const text = input.trim()
    if (!text || loading) return

    abortRef.current = new AbortController()

    setMessages(m => [
      ...m,
      { role: 'user', content: text },
      { role: 'assistant', content: '', thinking: null, streaming: true },
    ])
    setInput('')
    setLoading(true)

    try {
      const res = await fetch('/chat/stream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text, session_id: sessionId }),
        signal: abortRef.current.signal,
      })

      // Session is created on the backend before the first byte — update sidebar now
      fetchSessions()

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let thinking = ''
      let content = ''

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
                role: 'assistant', content, thinking, streaming: true, streamingThinking: true,
              }])
              await Promise.resolve()
            }
            if (evt.delta) {
              content += evt.delta
              setMessages(m => [...m.slice(0, -1), {
                role: 'assistant', content, thinking: thinking || null, streaming: true, streamingThinking: false,
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
      />
      <div className="app">
        <header className="header">
          <div className="header-title">
            <span className="cosmos-dot" />
            Cosmos
          </div>
          <button className="settings-btn" onClick={() => setShowSettings(true)} title="Settings">⚙</button>
        </header>
        {showSettings && <Settings onClose={() => setShowSettings(false)} />}

        <main className="messages">
          {messages.length === 0 && (
            <div className="empty-state">
              <div className="empty-glyph">◈</div>
              <p>Ready when you are.</p>
            </div>
          )}
          {messages.map((m, i) => <Message key={i} msg={m} />)}
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
      </div>
    </div>
  )
}
