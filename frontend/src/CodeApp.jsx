import { useState, useEffect, useRef, useCallback } from 'react'
import Editor, { DiffEditor } from '@monaco-editor/react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import Markdown from 'react-markdown'

// ── Language detection ────────────────────────────────────────────────────────

const EXT_LANG = {
  js: 'javascript', jsx: 'javascript', ts: 'typescript', tsx: 'typescript',
  py: 'python', rb: 'ruby', go: 'go', rs: 'rust', java: 'java',
  c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp', cs: 'csharp',
  css: 'css', scss: 'scss', less: 'less',
  html: 'html', xml: 'xml', svg: 'xml',
  json: 'json', yaml: 'yaml', yml: 'yaml',
  md: 'markdown', sh: 'shell', bash: 'shell', zsh: 'shell',
  sql: 'sql', graphql: 'graphql', toml: 'ini', ini: 'ini',
  tf: 'hcl', vue: 'html', svelte: 'html', dockerfile: 'dockerfile',
}

function detectLang(path) {
  if (!path) return 'plaintext'
  const name = path.split('/').pop().toLowerCase()
  if (name === 'dockerfile') return 'dockerfile'
  return EXT_LANG[name.split('.').pop()] || 'plaintext'
}

// ── SVG icons ─────────────────────────────────────────────────────────────────

function IconDiff() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="5" x2="12" y2="19"/>
      <path d="M5 12l7-7 7 7"/>
      <path d="M5 19l7-7 7 7" opacity="0.5"/>
    </svg>
  )
}

function IconTerminal() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 17 10 11 4 5"/>
      <line x1="12" y1="19" x2="20" y2="19"/>
    </svg>
  )
}

// ── Terminal panel ────────────────────────────────────────────────────────────

function TerminalPane({ projectRoot, visible, onToggle }) {
  const containerRef = useRef(null)
  const termRef = useRef(null)
  const fitRef = useRef(null)
  const wsRef = useRef(null)
  const mountedRef = useRef(false)

  useEffect(() => {
    if (!projectRoot || mountedRef.current) return
    mountedRef.current = true

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: '"Menlo", "Monaco", "Courier New", monospace',
      lineHeight: 1.4,
      theme: {
        background: '#0a0907',
        foreground: '#ece6db',
        cursor: '#d4895a',
        selectionBackground: 'rgba(212,137,90,0.25)',
        black: '#0a0907', red: '#c0392b', green: '#6db87a',
        yellow: '#d4895a', blue: '#7bafd4', magenta: '#a78bca',
        cyan: '#6cc6c6', white: '#ece6db',
        brightBlack: '#5a5550', brightWhite: '#ece6db',
      },
    })
    termRef.current = term

    const fit = new FitAddon()
    fitRef.current = fit
    term.loadAddon(fit)
    term.open(containerRef.current)

    const ws = new WebSocket(`ws://localhost:8002/code/terminal?cwd=${encodeURIComponent(projectRoot)}`)
    wsRef.current = ws
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      fit.fit()
      ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }))
    }
    ws.onmessage = (e) => {
      term.write(e.data instanceof ArrayBuffer ? new Uint8Array(e.data) : e.data)
    }
    term.onData(data => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    })

    return () => {
      term.dispose()
      ws.close()
      mountedRef.current = false
    }
  }, [projectRoot])

  useEffect(() => {
    if (visible && fitRef.current) {
      setTimeout(() => {
        fitRef.current?.fit()
        const ws = wsRef.current
        const term = termRef.current
        if (ws?.readyState === WebSocket.OPEN && term) {
          ws.send(JSON.stringify({ type: 'resize', rows: term.rows, cols: term.cols }))
        }
      }, 50)
    }
  }, [visible])

  return (
    <div className="code-terminal" style={{ display: visible ? 'flex' : 'none', flexDirection: 'column' }}>
      <div className="code-terminal-bar">
        <span className="code-pane-label"><IconTerminal /> Terminal</span>
        <button className="code-pane-btn" onClick={onToggle}>×</button>
      </div>
      <div ref={containerRef} className="code-terminal-inner" />
    </div>
  )
}

// ── Chat panel ────────────────────────────────────────────────────────────────

function CodeThinking({ content, streaming }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="code-thinking">
      <button className="code-thinking-toggle" onClick={() => setOpen(o => !o)}>
        <span className={`thinking-icon${streaming ? ' spinning' : ''}`}>◈</span>
        <span>{streaming ? 'Thinking…' : 'Reasoning'}</span>
        <span className={`thinking-chevron${open ? ' open' : ''}`}>›</span>
      </button>
      {open && <pre className="code-thinking-content">{content}</pre>}
    </div>
  )
}

function CodeToolCall({ call }) {
  const [open, setOpen] = useState(false)
  const done = call.result != null
  return (
    <div className={`code-tool${done ? ' code-tool--done' : ' code-tool--running'}`}>
      <button className="code-tool-header" onClick={() => done && setOpen(o => !o)}>
        <span className="code-tool-icon">{done ? '✓' : '◈'}</span>
        <span className="code-tool-name">{call.name}</span>
        {call.args?.path && <span className="code-tool-arg">{String(call.args.path).split('/').pop()}</span>}
        {call.args?.command && <span className="code-tool-arg">{String(call.args.command).slice(0, 40)}</span>}
        {done && <span className={`thinking-chevron${open ? ' open' : ''}`} style={{ marginLeft: 'auto' }}>›</span>}
      </button>
      {open && done && <pre className="code-tool-result">{call.result}</pre>}
    </div>
  )
}

function CodeMessage({ msg }) {
  const isUser = msg.role === 'user'
  return (
    <div className={`code-msg${isUser ? ' code-msg--user' : ''}`}>
      {!isUser && msg.thinking && (
        <CodeThinking content={msg.thinking} streaming={!!msg.streamingThinking} />
      )}
      {!isUser && msg.toolCalls?.length > 0 && (
        <div className="code-tool-list">
          {msg.toolCalls.map((c, i) => <CodeToolCall key={i} call={c} />)}
        </div>
      )}
      {!isUser && msg.fileWritten?.length > 0 && (
        <div className="code-file-written">
          {msg.fileWritten.map(f => (
            <span key={f} className="code-file-written-pill">✎ {f.split('/').pop()}</span>
          ))}
        </div>
      )}
      {(msg.content || isUser) && (
        <div className="code-bubble">
          <Markdown>{msg.content || ''}</Markdown>
          {msg.streaming && !msg.streamingThinking && <span className="cursor-inline" />}
        </div>
      )}
    </div>
  )
}

function ChatPanel({ project, onFileWritten }) {
  const [messages, setMessages] = useState([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const abortRef = useRef(null)
  const bottomRef = useRef(null)
  const inputRef = useRef(null)
  const sessionId = project ? `code:${project.path}` : null

  useEffect(() => {
    if (!sessionId) return
    setMessages([])
    fetch(`/code/chat/history?session_id=${encodeURIComponent(sessionId)}`)
      .then(r => r.json())
      .then(d => {
        if (d.history?.length) setMessages(d.history.map(m => ({ role: m.role, content: m.content })))
      })
      .catch(() => {})
  }, [sessionId])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  async function send() {
    const text = input.trim()
    if (!text || loading || !project) return
    setInput('')
    setMessages(m => [
      ...m,
      { role: 'user', content: text },
      { role: 'assistant', content: '', thinking: null, toolCalls: [], fileWritten: [], streaming: true },
    ])
    setLoading(true)
    abortRef.current = new AbortController()

    try {
      const res = await fetch('/code/chat/stream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId, message: text, project_root: project.path, project_name: project.name }),
        signal: abortRef.current.signal,
      })

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = '', thinking = '', content = '', toolCalls = [], fileWritten = []

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
            if (evt.reasoning_delta) {
              thinking += evt.reasoning_delta
              setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking, toolCalls, fileWritten, streaming: true, streamingThinking: true }])
              await Promise.resolve()
            }
            if (evt.tool_call) {
              toolCalls = [...toolCalls, { name: evt.tool_call.name, args: evt.tool_call.args, result: null }]
              setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, fileWritten, streaming: true, streamingThinking: false }])
              await Promise.resolve()
            }
            if (evt.tool_result) {
              toolCalls = toolCalls.map(c => c.name === evt.tool_result.name && c.result === null ? { ...c, result: evt.tool_result.output } : c)
              setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, fileWritten, streaming: true, streamingThinking: false }])
              await Promise.resolve()
            }
            if (evt.file_written) {
              fileWritten = [...fileWritten, evt.file_written]
              onFileWritten?.(evt.file_written)
              setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, fileWritten, streaming: true, streamingThinking: false }])
              await Promise.resolve()
            }
            if (evt.delta) {
              content += evt.delta
              setMessages(m => [...m.slice(0, -1), { role: 'assistant', content, thinking: thinking || null, toolCalls, fileWritten, streaming: true, streamingThinking: false }])
              await Promise.resolve()
            }
          } catch { break outer }
        }
      }
    } catch (e) {
      if (e?.name !== 'AbortError') {
        setMessages(m => [...m.slice(0, -1), { role: 'assistant', content: 'Something went wrong.', thinking: null }])
      }
    } finally {
      abortRef.current = null
      setLoading(false)
      setMessages(m => {
        const last = m[m.length - 1]
        return last?.role === 'assistant' ? [...m.slice(0, -1), { ...last, streaming: false, streamingThinking: false }] : m
      })
      inputRef.current?.focus()
    }
  }

  function stop() {
    if (sessionId) fetch(`/code/chat/${encodeURIComponent(sessionId)}/generation`, { method: 'DELETE' }).catch(() => {})
    abortRef.current?.abort()
  }

  return (
    <div className="code-chat">
      <div className="code-chat-header">
        <span className="code-pane-label">◈ Cosmos</span>
        {project && <span className="code-chat-project">{project.name}</span>}
      </div>
      <div className="code-chat-messages">
        {messages.length === 0 && (
          <div className="code-chat-empty">
            <div className="code-chat-empty-glyph">◈</div>
            <p>Ask Cosmos to help with this project.</p>
          </div>
        )}
        {messages.map((m, i) => <CodeMessage key={i} msg={m} />)}
        <div ref={bottomRef} />
      </div>
      <div className="code-chat-input-area">
        <textarea
          ref={inputRef}
          className="code-chat-input"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() } }}
          placeholder={project ? 'Ask Cosmos…' : 'Open a project first'}
          rows={1}
          disabled={!project || loading}
        />
        {loading
          ? <button className="code-chat-send stop" onClick={stop}>■</button>
          : <button className="code-chat-send" onClick={send} disabled={!input.trim() || !project}>↑</button>
        }
      </div>
    </div>
  )
}

// ── Main Code app (editor + chat, no file tree — that lives in sidebar) ───────

const DEFAULT_FONT_SIZE = 13
const MIN_FONT_SIZE = 9
const MAX_FONT_SIZE = 30

export default function CodeApp({ project, fileToOpen, onFileToOpenHandled, onRefreshTree }) {
  const [openFiles, setOpenFiles] = useState([])
  const [activeFilePath, setActiveFilePath] = useState(null)
  const [diffMode, setDiffMode] = useState(false)
  const [originalContents, setOriginalContents] = useState({})
  const [terminalOpen, setTerminalOpen] = useState(false)
  const [fontSize, setFontSize] = useState(DEFAULT_FONT_SIZE)
  const editorRef = useRef(null)
  const fontSizeRef = useRef(DEFAULT_FONT_SIZE)

  function adjustFontSize(delta) {
    setFontSize(prev => {
      const next = Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, prev + delta))
      fontSizeRef.current = next
      editorRef.current?.updateOptions({ fontSize: next })
      return next
    })
  }

  const activeFile = openFiles.find(f => f.path === activeFilePath)

  // Open a file when signalled from the sidebar
  useEffect(() => {
    if (!fileToOpen) return
    openFile(fileToOpen)
    onFileToOpenHandled?.()
  }, [fileToOpen])

  async function openFile(path) {
    if (openFiles.find(f => f.path === path)) {
      setActiveFilePath(path)
      return
    }
    try {
      const res = await fetch(`/code/fs/file?path=${encodeURIComponent(path)}`)
      const data = await res.json()
      if (data.error) return
      setOpenFiles(f => [...f, { path, content: data.content, savedContent: data.content }])
      setActiveFilePath(path)
    } catch {}
  }

  function closeFile(path) {
    const remaining = openFiles.filter(x => x.path !== path)
    setOpenFiles(remaining)
    if (activeFilePath === path) {
      setActiveFilePath(remaining.length > 0 ? remaining[remaining.length - 1].path : null)
    }
  }

  function updateFileContent(path, content) {
    setOpenFiles(f => f.map(x => x.path === path ? { ...x, content, isDirty: content !== x.savedContent } : x))
  }

  async function saveFile(path) {
    const file = openFiles.find(f => f.path === path)
    if (!file) return
    await fetch('/code/fs/file', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content: file.content }),
    })
    setOpenFiles(f => f.map(x => x.path === path ? { ...x, savedContent: x.content, isDirty: false } : x))
  }

  async function fetchOriginalContent(path) {
    if (!project) return
    try {
      const res = await fetch(`/code/git/original?root=${encodeURIComponent(project.path)}&path=${encodeURIComponent(path)}`)
      const data = await res.json()
      setOriginalContents(o => ({ ...o, [path]: data.content || '' }))
    } catch {}
  }

  function toggleDiff() {
    if (!diffMode && activeFilePath) fetchOriginalContent(activeFilePath)
    setDiffMode(d => !d)
  }

  const handleFileWritten = useCallback(async (path) => {
    try {
      const res = await fetch(`/code/fs/file?path=${encodeURIComponent(path)}`)
      const data = await res.json()
      if (data.error) return
      setOpenFiles(f => {
        const exists = f.find(x => x.path === path)
        if (exists) return f.map(x => x.path === path ? { ...x, content: data.content, savedContent: data.content, isDirty: false } : x)
        return [...f, { path, content: data.content, savedContent: data.content }]
      })
      setActiveFilePath(path)
    } catch {}
    onRefreshTree?.()
  }, [project, onRefreshTree])

  // Ctrl/Cmd+S, Cmd+=/-, Cmd+0
  useEffect(() => {
    function handleKeyDown(e) {
      if ((e.metaKey || e.ctrlKey) && e.key === 's' && activeFilePath) {
        e.preventDefault()
        saveFile(activeFilePath)
      }
      if ((e.metaKey || e.ctrlKey) && (e.key === '=' || e.key === '+')) {
        e.preventDefault()
        adjustFontSize(1)
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '-') {
        e.preventDefault()
        adjustFontSize(-1)
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '0') {
        e.preventDefault()
        setFontSize(DEFAULT_FONT_SIZE)
        fontSizeRef.current = DEFAULT_FONT_SIZE
        editorRef.current?.updateOptions({ fontSize: DEFAULT_FONT_SIZE })
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [activeFilePath, openFiles])

  return (
    <div className="code-app">
      {/* ── Center: tabs + editor + terminal ── */}
      <div className="code-center">
        <div className="code-tabs">
          {openFiles.map(f => {
            const name = f.path.split('/').pop()
            return (
              <div
                key={f.path}
                className={`code-tab${f.path === activeFilePath ? ' active' : ''}${f.isDirty ? ' dirty' : ''}`}
                onClick={() => setActiveFilePath(f.path)}
              >
                <span className="code-tab-name">{name}</span>
                {f.isDirty && <span className="code-tab-dirty">•</span>}
                <button className="code-tab-close" onClick={e => { e.stopPropagation(); closeFile(f.path) }}>×</button>
              </div>
            )
          })}
          <div className="code-tabs-actions">
            <button className="code-tabs-action code-zoom-btn" onClick={() => adjustFontSize(-1)} title="Zoom out (⌘-)">−</button>
            <span className="code-zoom-size">{fontSize}</span>
            <button className="code-tabs-action code-zoom-btn" onClick={() => adjustFontSize(1)} title="Zoom in (⌘+)">+</button>
            <button
              className={`code-tabs-action${diffMode ? ' active' : ''}`}
              onClick={toggleDiff}
              title="Toggle diff view"
              disabled={!activeFilePath}
            ><IconDiff /></button>
            <button
              className={`code-tabs-action${terminalOpen ? ' active' : ''}`}
              onClick={() => setTerminalOpen(o => !o)}
              title="Toggle terminal"
            ><IconTerminal /></button>
          </div>
        </div>

        <div className="code-editor-wrap">
          {!activeFile && (
            <div className="code-editor-empty">
              {project ? 'Select a file from the sidebar.' : 'Open a project to get started.'}
            </div>
          )}
          {activeFile && !diffMode && (
            <Editor
              path={activeFilePath}
              language={detectLang(activeFilePath)}
              value={activeFile.content}
              onChange={val => updateFileContent(activeFilePath, val ?? '')}
              theme="vs-dark"
              height="100%"
              onMount={(editor, monaco) => {
                editorRef.current = editor
                editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Equal, () => adjustFontSize(1))
                editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Minus, () => adjustFontSize(-1))
                editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Digit0, () => {
                  setFontSize(DEFAULT_FONT_SIZE)
                  fontSizeRef.current = DEFAULT_FONT_SIZE
                  editor.updateOptions({ fontSize: DEFAULT_FONT_SIZE })
                })
              }}
              options={{
                minimap: { enabled: false },
                fontSize,
                lineHeight: 20,
                fontFamily: '"Menlo", "Monaco", "Courier New", monospace',
                wordWrap: 'off',
                scrollBeyondLastLine: false,
                renderLineHighlight: 'line',
                smoothScrolling: true,
                padding: { top: 12, bottom: 12 },
                tabSize: 2,
              }}
            />
          )}
          {activeFile && diffMode && (
            <DiffEditor
              original={originalContents[activeFilePath] ?? ''}
              modified={activeFile.content}
              language={detectLang(activeFilePath)}
              theme="vs-dark"
              height="100%"
              options={{
                minimap: { enabled: false },
                fontSize,
                lineHeight: 20,
                fontFamily: '"Menlo", "Monaco", "Courier New", monospace',
                renderSideBySide: true,
                readOnly: true,
                padding: { top: 12 },
              }}
            />
          )}
        </div>

        <TerminalPane
          projectRoot={project?.path}
          visible={terminalOpen}
          onToggle={() => setTerminalOpen(false)}
        />
      </div>

      {/* ── Right: chat ── */}
      <ChatPanel project={project} onFileWritten={handleFileWritten} />
    </div>
  )
}
