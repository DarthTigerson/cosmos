import { useState, useRef, useEffect } from 'react'
import './App.css'

const SESSION_ID = 'cosmos-' + Math.random().toString(36).slice(2, 8)

function parseStream(text) {
  const trimmed = text.trimStart()
  const complete = trimmed.match(/^<think>([\s\S]*?)<\/think>([\s\S]*)$/)
  if (complete) {
    return { content: complete[2].trimStart(), thinking: complete[1].trim() }
  }
  if (trimmed.startsWith('<think>')) {
    return { content: '', thinking: trimmed.slice(7) }
  }
  return { content: text, thinking: null }
}

function ThinkingBlock({ content, streamingPhase }) {
  const [open, setOpen] = useState(true)
  const prevPhase = useRef(streamingPhase)
  const contentRef = useRef(null)

  useEffect(() => {
    if (prevPhase.current && !streamingPhase) setOpen(false)
    prevPhase.current = streamingPhase
  }, [streamingPhase])

  useEffect(() => {
    if (open && streamingPhase && contentRef.current) {
      contentRef.current.scrollTop = contentRef.current.scrollHeight
    }
  }, [content, open, streamingPhase])

  return (
    <div className="thinking-block">
      <button className="thinking-toggle" onClick={() => setOpen(o => !o)}>
        <span className={`thinking-icon${streamingPhase ? ' spinning' : ''}`}>◈</span>
        {streamingPhase ? 'Reasoning…' : open ? 'Hide reasoning' : 'Show reasoning'}
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
  const inThinkingPhase = msg.streaming && hasThinking && !msg.content

  return (
    <div className={`message ${isUser ? 'user' : 'cosmos'}`}>
      {!isUser && <div className="avatar">◈</div>}
      <div className="bubble-wrap">
        {hasThinking && (
          <ThinkingBlock content={msg.thinking} streamingPhase={inThinkingPhase} />
        )}
        {!inThinkingPhase && (
          <div className="bubble">
            {msg.content}
            {msg.streaming && <span className="cursor-inline" />}
          </div>
        )}
      </div>
      {isUser && <div className="avatar user-avatar">T</div>}
    </div>
  )
}

export default function App() {
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const bottomRef = useRef(null)
  const inputRef = useRef(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  async function send() {
    const text = input.trim()
    if (!text || loading) return

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
        body: JSON.stringify({ message: text, session_id: SESSION_ID }),
      })

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let accumulated = ''

      outer: while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() ?? ''
        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const payload = line.slice(6)
          try {
            const evt = JSON.parse(payload)
            if (evt.error) throw new Error(evt.error)
            if (evt.done) break outer
            if (evt.delta) {
              accumulated += evt.delta
              const parsed = parseStream(accumulated)
              setMessages(m => [
                ...m.slice(0, -1),
                { role: 'assistant', ...parsed, streaming: true },
              ])
            }
          } catch { break outer }
        }
      }
    } catch {
      setMessages(m => [
        ...m.slice(0, -1),
        { role: 'assistant', content: 'Something went wrong. Is vmlx running?', thinking: null },
      ])
    } finally {
      setLoading(false)
      setMessages(m => {
        const last = m[m.length - 1]
        if (last?.role === 'assistant') {
          return [...m.slice(0, -1), { ...last, streaming: false }]
        }
        return m
      })
      inputRef.current?.focus()
    }
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className="app">
      <header className="header">
        <div className="header-title">
          <span className="cosmos-dot" />
          Cosmos
        </div>
        <div className="header-sub">local · private · yours</div>
      </header>

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
        <button className="send-btn" onClick={send} disabled={loading || !input.trim()}>
          ↑
        </button>
      </footer>
    </div>
  )
}
