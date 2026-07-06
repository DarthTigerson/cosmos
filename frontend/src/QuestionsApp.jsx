import { useState, useEffect } from 'react'

const STATUS_COLOR = {
  pending: '#d4895a',
  answered: '#6899c4',
  resolved: '#6db87a',
}

const STATUS_LABEL = {
  pending: 'Pending',
  answered: 'Answered',
  resolved: 'Resolved',
}

function QuestionCard({ q, onUpdate }) {
  const [answerText, setAnswerText] = useState(q.thomas_answer || '')
  const [sourceOpen, setSourceOpen] = useState(false)
  const [thinkingOpen, setThinkingOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  async function submitAnswer() {
    if (!answerText.trim()) return
    setSaving(true)
    await fetch(`/questions/${q.id}/answer`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ answer: answerText }),
    })
    setSaving(false)
    onUpdate()
  }

  async function resolve() {
    await fetch(`/questions/${q.id}/resolve`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ store_memory: true }),
    })
    onUpdate()
  }

  async function discard() {
    await fetch(`/questions/${q.id}`, { method: 'DELETE' })
    onUpdate()
  }

  return (
    <div className={`qcard qcard--${q.status}`}>
      <div className="qcard-top">
        <span className="qcard-status" style={{ color: STATUS_COLOR[q.status] }}>
          {STATUS_LABEL[q.status]}
        </span>
        <span className="qcard-date">{formatDate(q.created_at)}</span>
        {q.source_ref && <span className="qcard-ref">{q.source_ref}</span>}
        <button className="qcard-discard" onClick={discard} title="Discard">×</button>
      </div>

      <div className="qcard-question">{q.question}</div>

      {q.source_text && (
        <div className="qcard-collapsible">
          <button className="qcard-toggle" onClick={() => setSourceOpen(o => !o)}>
            <span>Source clip</span>
            <span className={`thinking-chevron${sourceOpen ? ' open' : ''}`}>›</span>
          </button>
          {sourceOpen && <pre className="qcard-clip">{q.source_text}</pre>}
        </div>
      )}

      {q.cosmos_thinking && (
        <div className="qcard-collapsible">
          <button className="qcard-toggle" onClick={() => setThinkingOpen(o => !o)}>
            <span className="qcard-toggle-cosmos">◈ Cosmos's thinking</span>
            <span className={`thinking-chevron${thinkingOpen ? ' open' : ''}`}>›</span>
          </button>
          {thinkingOpen && <pre className="qcard-clip qcard-clip--thinking">{q.cosmos_thinking}</pre>}
        </div>
      )}

      {q.status !== 'resolved' && (
        <div className="qcard-answer-area">
          <textarea
            className="qcard-answer-input"
            placeholder="Your answer…"
            value={answerText}
            onChange={e => setAnswerText(e.target.value)}
            rows={3}
          />
          <div className="qcard-actions">
            <button
              className="qcard-btn qcard-btn--answer"
              onClick={submitAnswer}
              disabled={saving || !answerText.trim()}
            >
              {saving ? 'Saving…' : q.status === 'answered' ? 'Update answer' : 'Answer'}
            </button>
            {q.status === 'answered' && (
              <button className="qcard-btn qcard-btn--resolve" onClick={resolve}>
                Resolve + store in memory
              </button>
            )}
          </div>
        </div>
      )}

      {q.status === 'resolved' && q.thomas_answer && (
        <div className="qcard-resolved-answer">
          <span className="qcard-resolved-label">Your answer</span>
          <p>{q.thomas_answer}</p>
        </div>
      )}
    </div>
  )
}

export default function QuestionsApp({ onBack }) {
  const [questions, setQuestions] = useState([])
  const [filter, setFilter] = useState('pending')
  const [counts, setCounts] = useState({ pending: 0 })

  useEffect(() => {
    load()
  }, [filter])

  async function load() {
    const [qs, ct] = await Promise.all([
      fetch(`/questions?status=${filter}`).then(r => r.json()),
      fetch('/questions/count').then(r => r.json()),
    ])
    setQuestions(qs)
    setCounts(ct)
  }

  return (
    <div className="qapp">
      <div className="pod-page-header">
        {onBack && <button className="pod-back-btn" onClick={onBack}>←</button>}
        <span className="pod-page-icon">❓</span>
        <h2>Questions for Thomas</h2>
        {counts.pending > 0 && (
          <span className="q-pending-badge">{counts.pending}</span>
        )}
      </div>

      <div className="q-filters">
        {['pending', 'answered', 'resolved'].map(s => (
          <button
            key={s}
            className={`q-filter-btn${filter === s ? ' active' : ''}`}
            onClick={() => setFilter(s)}
          >
            {STATUS_LABEL[s]}
            {s === 'pending' && counts.pending > 0 && (
              <span className="q-filter-count">{counts.pending}</span>
            )}
          </button>
        ))}
      </div>

      <div className="q-list">
        {questions.length === 0 ? (
          <div className="pod-empty">
            {filter === 'pending'
              ? 'No pending questions. Cosmos will add them here when she\'s uncertain about something.'
              : `No ${filter} questions.`}
          </div>
        ) : (
          questions.map(q => (
            <QuestionCard key={q.id} q={q} onUpdate={load} />
          ))
        )}
      </div>
    </div>
  )
}

function formatDate(str) {
  try {
    return new Date(str + 'Z').toLocaleDateString(undefined, {
      month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
    })
  } catch { return str }
}
