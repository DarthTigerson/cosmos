import { useState, useEffect } from 'react'

const STATUS_LABEL = {
  none: 'No transcript',
  fetching: 'Fetching…',
  no_transcript: 'Unavailable',
  ready: 'Ready',
  ingesting: 'Ingesting…',
  ingested: 'Ingested',
  error: 'Error',
}

const STATUS_COLOR = {
  none: 'var(--text-dim)',
  fetching: 'var(--cosmos-accent)',
  no_transcript: 'var(--text-dim)',
  ready: '#6899c4',
  ingesting: 'var(--cosmos-accent)',
  ingested: '#6db87a',
  error: '#e07070',
}

const STEP_LABEL = {
  checking: 'Checking…',
  downloading: 'Downloading audio…',
  transcribing: 'Transcribing with Whisper…',
  parsing: 'Parsing transcript…',
  chunking: 'Chunking transcript…',
  embedding: 'Embedding into knowledge base…',
  done: 'Done',
  error: 'Error',
  no_transcript: 'No transcript available',
}

function useStreamAction(onDone) {
  const [state, setState] = useState(null) // null | { pct, step, extra }

  async function run(url) {
    setState({ pct: 0, step: 'checking', extra: {} })
    const res = await fetch(url)
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      const lines = buf.split('\n')
      buf = lines.pop() ?? ''
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue
        try {
          const evt = JSON.parse(line.slice(6))
          setState({ pct: evt.pct ?? 0, step: evt.status, extra: evt })
          if (evt.status === 'done' || evt.status === 'error' || evt.status === 'no_transcript') {
            setState(null)
            onDone(evt.status, evt)
          }
        } catch {}
      }
    }
  }

  return [state, run]
}

function ProgressRow({ state }) {
  const isPulse = state.step === 'transcribing' || state.step === 'embedding'
  const label = STEP_LABEL[state.step] || state.step
  const extra = state.step === 'transcribing' && state.extra?.model
    ? ` · ${state.extra.model}` : ''
  const counter = state.step === 'embedding' && state.extra?.chunk
    ? ` (${state.extra.chunk}/${state.extra.total})` : ''

  return (
    <div className="pod-progress-wrap">
      <div className="pod-progress-bar">
        <div
          className={`pod-progress-fill${isPulse ? ' pod-progress-fill--pulse' : ''}`}
          style={{ width: `${state.pct}%` }}
        />
      </div>
      <span className="pod-progress-label">
        {label}{extra}{counter}
      </span>
    </div>
  )
}

function EpisodeRow({ ep, onUpdate }) {
  const [fetchState, runFetch] = useStreamAction((status) => {
    onUpdate(ep.id, { transcript_status: status === 'done' ? 'ready' : status })
  })
  const [ingestState, runIngest] = useStreamAction((status, evt) => {
    onUpdate(ep.id, {
      transcript_status: status === 'done' ? 'ingested' : ep.transcript_status,
      chunks_count: evt.chunks || ep.chunks_count,
    })
  })

  async function handleFetch() { await runFetch(`/episodes/${ep.id}/fetch`) }
  async function handleIngest() { await runIngest(`/episodes/${ep.id}/ingest`) }

  const active = fetchState || ingestState
  const status = active
    ? (fetchState ? 'fetching' : 'ingesting')
    : ep.transcript_status

  return (
    <div className="pod-episode">
      <div className="pod-ep-main">
        <div className="pod-ep-title">{ep.title}</div>
        <div className="pod-ep-meta">
          {ep.pub_date && <span>{formatDate(ep.pub_date)}</span>}
          {ep.duration && <span>{formatDuration(ep.duration)}</span>}
          <span className="pod-ep-status" style={{ color: STATUS_COLOR[status] }}>
            {STATUS_LABEL[status]}
            {ep.transcript_status === 'ingested' && ep.chunks_count > 0 && (
              <span className="pod-ep-chunks"> · {ep.chunks_count} chunks</span>
            )}
          </span>
        </div>
        {active && <ProgressRow state={fetchState || ingestState} />}
      </div>
      <div className="pod-ep-action">
        {!active && (ep.transcript_status === 'none' || ep.transcript_status === 'error') && (
          <button className="pod-fetch-btn" onClick={handleFetch}>Fetch</button>
        )}
        {!active && ep.transcript_status === 'ready' && (
          <button className="pod-fetch-btn pod-ingest-btn" onClick={handleIngest}>
            Ingest
          </button>
        )}
        {!active && ep.transcript_status === 'ingested' && (
          <span className="pod-ready-check">✓</span>
        )}
        {!active && ep.transcript_status === 'no_transcript' && (
          <span className="pod-no-transcript">—</span>
        )}
      </div>
    </div>
  )
}

export default function PodcastApp({ onBack }) {
  const [podView, setPodView] = useState('shows')
  const [podcasts, setPodcasts] = useState([])
  const [selectedPod, setSelectedPod] = useState(null)
  const [episodes, setEpisodes] = useState([])
  const [episodesLoading, setEpisodesLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [results, setResults] = useState([])
  const [searching, setSearching] = useState(false)

  useEffect(() => { loadPodcasts() }, [])

  async function loadPodcasts() {
    const res = await fetch('/podcasts')
    setPodcasts(await res.json())
  }

  async function doSearch() {
    if (!search.trim()) return
    setSearching(true)
    setResults([])
    try {
      const res = await fetch(`/podcasts/search?q=${encodeURIComponent(search)}`)
      setResults(await res.json())
    } finally {
      setSearching(false)
    }
  }

  async function addPodcast(rssUrl) {
    await fetch('/podcasts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ rss_url: rssUrl }),
    })
    await loadPodcasts()
    setResults([])
    setSearch('')
  }

  async function removePodcast(id, e) {
    e.stopPropagation()
    await fetch(`/podcasts/${id}`, { method: 'DELETE' })
    await loadPodcasts()
  }

  async function openPodcast(pod) {
    setSelectedPod(pod)
    setPodView('episodes')
    setEpisodesLoading(true)
    const res = await fetch(`/podcasts/${pod.id}/episodes`)
    setEpisodes(await res.json())
    setEpisodesLoading(false)
  }

  function handleEpisodeUpdate(epId, patch) {
    setEpisodes(eps => eps.map(e => e.id === epId ? { ...e, ...patch } : e))
  }

  // ── Shows view ─────────────────────────────────────────────────────────────
  if (podView === 'shows') {
    const subscribedUrls = new Set(podcasts.map(p => p.rss_url))
    return (
      <div className="pod-app">
        <div className="pod-page-header">
          {onBack && <button className="pod-back-btn" onClick={onBack} title="Back to Apps">←</button>}
          <span className="pod-page-icon">📻</span>
          <h2>Podcasts</h2>
        </div>

        <div className="pod-search-row">
          <input
            className="pod-search-input"
            placeholder="Search for a podcast…"
            value={search}
            onChange={e => setSearch(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && doSearch()}
          />
          <button className="pod-search-btn" onClick={doSearch} disabled={searching}>
            {searching ? '…' : 'Search'}
          </button>
        </div>

        {results.length > 0 && (
          <div className="pod-section">
            <div className="pod-section-label">Results</div>
            {results.map(r => (
              <div key={r.rss_url} className="pod-search-item">
                {r.artwork_url
                  ? <img src={r.artwork_url} className="pod-thumb" alt="" />
                  : <div className="pod-thumb pod-thumb-placeholder">📻</div>
                }
                <div className="pod-search-info">
                  <div className="pod-search-title">{r.title}</div>
                  <div className="pod-search-author">{r.author}</div>
                </div>
                <button
                  className="pod-add-btn"
                  onClick={() => addPodcast(r.rss_url)}
                  disabled={subscribedUrls.has(r.rss_url)}
                >
                  {subscribedUrls.has(r.rss_url) ? '✓' : '+'}
                </button>
              </div>
            ))}
          </div>
        )}

        {podcasts.length > 0 && (
          <div className="pod-section">
            <div className="pod-section-label">Subscribed</div>
            <div className="pod-shows-grid">
              {podcasts.map(p => (
                <div key={p.id} className="pod-show-card" onClick={() => openPodcast(p)}>
                  <button className="pod-remove-btn" onClick={e => removePodcast(p.id, e)}>×</button>
                  {p.artwork_url
                    ? <img src={p.artwork_url} className="pod-artwork" alt="" />
                    : <div className="pod-artwork pod-artwork-placeholder">📻</div>
                  }
                  <div className="pod-show-name">{p.title}</div>
                  <div className="pod-show-author">{p.author}</div>
                </div>
              ))}
            </div>
          </div>
        )}

        {podcasts.length === 0 && results.length === 0 && (
          <div className="pod-empty">Search for a podcast above to get started.</div>
        )}
      </div>
    )
  }

  // ── Episodes view ──────────────────────────────────────────────────────────
  const ready = episodes.filter(e => e.transcript_status === 'ready').length
  return (
    <div className="pod-app">
      <div className="pod-page-header">
        <button className="pod-back-btn" onClick={() => { setPodView('shows'); setSelectedPod(null); setEpisodes([]) }}>←</button>
        {selectedPod?.artwork_url && (
          <img src={selectedPod.artwork_url} className="pod-header-art" alt="" />
        )}
        <div className="pod-header-info">
          <h2 className="pod-header-title">{selectedPod?.title}</h2>
          <div className="pod-header-sub">{selectedPod?.author} · {episodes.length} episodes · {ready} ready</div>
        </div>
      </div>

      {episodesLoading
        ? <div className="pod-loading">Loading episodes…</div>
        : (
          <div className="pod-episode-list">
            {episodes.map(ep => (
              <EpisodeRow key={ep.id} ep={ep} onUpdate={handleEpisodeUpdate} />
            ))}
          </div>
        )
      }
    </div>
  )
}

// ── Formatters ────────────────────────────────────────────────────────────────

function formatDate(str) {
  try {
    return new Date(str).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
  } catch { return str }
}

function formatDuration(str) {
  if (!str) return ''
  // Already formatted like "1:23:45" — return as-is
  if (/^\d+:\d{2}(:\d{2})?$/.test(str)) return str
  // Seconds integer
  const s = parseInt(str)
  if (!isNaN(s)) {
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    return h ? `${h}h ${m}m` : `${m}m`
  }
  return str
}
