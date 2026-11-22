import { useEffect, useRef, useState, type ReactNode } from 'react'
import { api, useAsset, useLoad, useSignals } from '../api'
import { isZero, longDate, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Episode, Feed, Memory, PodcastsView } from '../types'

function length(sec: number): string {
  if (!sec) return ''
  const h = Math.floor(sec / 3600)
  const m = Math.round((sec % 3600) / 60)
  return h ? `${h} h ${String(m).padStart(2, '0')} min` : `${m} min`
}

function meta(show: string, e: Episode): string {
  return [show, isZero(e.published_at) ? '' : longDate(new Date(e.published_at)), length(e.duration_sec)].filter(Boolean).join(' · ')
}

const order = ['downloading', 'transcribing', 'reading', 'thinking'] as const
const stageName = { downloading: 'Downloading', transcribing: 'Writing it out', reading: 'Reading it', thinking: 'Thinking it over' }
const stageHint = {
  downloading: 'the audio',
  transcribing: 'Whisper, on this Mac',
  reading: 'part by part',
  thinking: 'what to keep, what to tell you',
}
const listening = (e: Episode) => (order as readonly string[]).includes(e.status)

// How far through the stage she is, from the note she reports with it
// ("62%", "part 3 of 8"), or null when the stage gives no measure.
function stageFill(e: Episode): number | null {
  const pct = /^(\d+)%$/.exec(e.stage_note)
  if (pct) return Number(pct[1])
  const part = /^part (\d+) of (\d+)$/.exec(e.stage_note)
  if (part) return (Number(part[1]) / Number(part[2])) * 100
  return null
}

type View = { kind: 'all' } | { kind: 'show'; id: number } | { kind: 'episode'; id: number; from: 'all' | number }

function Art({ feed, size }: { feed?: Feed; size?: 'big' }) {
  const url = useAsset(`/api/podcasts/feeds/${feed?.id ?? 0}/art`)
  const cls = `pod-art ${size ?? ''}`
  if (!feed?.has_art || !url) return <span className={cls} aria-hidden="true">{(feed?.title || '?').replace(/^The /, '').slice(0, 1)}</span>
  return <img className={cls} src={url} alt="" />
}

function SearchBox({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  return (
    <label className="mem-search pod-search">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
        <circle cx="7" cy="7" r="5" />
        <path d="M11 11l3.5 3.5" />
      </svg>
      <input type="search" placeholder={label} aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
    </label>
  )
}

// The episode she is working through, with the four stages it passes.
function Listening({ feed, e }: { feed?: Feed; e: Episode }) {
  const at = order.indexOf(e.status as (typeof order)[number])
  const fill = stageFill(e)
  return (
    <section className="pod-now" aria-label="Listening now">
      <Art feed={feed} size="big" />
      <div className="pod-now-body">
        <div className="pod-now-top">
          <span className="sub strong">Listening now</span>
          <span className="sub">{meta(feed?.title ?? '', e)}</span>
        </div>
        <h2>{e.title}</h2>
        <ol className="pod-stages">
          {order.map((s, i) => (
            <li key={s} className={i < at ? 'done' : i === at ? 'now' : undefined}>
              <span className={i === at && fill === null ? 'bar busy' : 'bar'}>
                <i style={{ width: `${i < at ? 100 : i === at ? (fill ?? 100) : 0}%` }} />
              </span>
              <span className="stage-name">{stageName[s]}</span>
              <span className="sub">{i === at && e.stage_note ? e.stage_note : i < at ? 'done' : stageHint[s]}</span>
            </li>
          ))}
        </ol>
        <p className="sub">After the download nothing leaves this computer. If you write to her she answers you between parts.</p>
      </div>
    </section>
  )
}

function EpisodeRow({ feed, e, children }: { feed?: Feed; e: Episode; children?: ReactNode }) {
  return (
    <li>
      <Art feed={feed} />
      <span className="pod-row-text">
        <b>{e.title}</b>
        <span className="sub">{e.status === 'failed' && e.error ? e.error : meta(feed?.title ?? '', e)}</span>
      </span>
      <span className="pod-row-acts">{children}</span>
    </li>
  )
}

function Heard({ feed, e, onOpen }: { feed?: Feed; e: Episode; onOpen: () => void }) {
  return (
    <button type="button" className="pod-heard" onClick={onOpen}>
      <span className="pod-heard-head">
        <Art feed={feed} />
        <span>
          <b>{e.title}</b>
          <span className="sub">{meta(feed?.title ?? '', e)}</span>
        </span>
      </span>
      <span className="pod-heard-summary">{e.summary}</span>
      <span className="pod-chips">
        <span className="pod-chip">{e.takeaways.length} picked up</span>
        {(e.thoughts ?? []).length > 0 && <span className="pod-chip hers">{e.thoughts.length} her view</span>}
        {e.share && <span className="pod-chip tell">wants to tell you</span>}
      </span>
    </button>
  )
}

function AllShows({ view, feed, act, open }: { view: PodcastsView; feed: (id: number) => Feed | undefined; act: (path: string, done: string) => void; open: (id: number) => void }) {
  const [query, setQuery] = useState('')
  const q = query.trim().toLowerCase()
  const heard = view.done.filter((e) => !q || [e.title, e.summary, ...e.takeaways, ...(e.thoughts ?? [])].join(' ').toLowerCase().includes(q))
  return (
    <>
      <div className="pod-head">
        <div>
          <span className="eyebrow">All shows</span>
          <h2>Listening</h2>
        </div>
        <SearchBox value={query} onChange={setQuery} label="Search what she heard" />
      </div>
      {view.current && <Listening feed={feed(view.current.feed_id)} e={view.current} />}
      {view.queued.length > 0 && (
        <section className="pod-section">
          <h3 className="label">Up next</h3>
          <ul className="pod-rows">
            {view.queued.map((e) => (
              <EpisodeRow key={e.id} feed={feed(e.feed_id)} e={e}>
                <button type="button" className="secondary" onClick={() => act(`/api/podcasts/episodes/${e.id}/unqueue`, 'Taken out of the queue.')}>
                  Take out of queue
                </button>
              </EpisodeRow>
            ))}
          </ul>
        </section>
      )}
      {view.failed.length > 0 && (
        <section className="pod-section">
          <h3 className="label pod-failed">Did not work</h3>
          <ul className="pod-rows">
            {view.failed.map((e) => (
              <EpisodeRow key={e.id} feed={feed(e.feed_id)} e={e}>
                <button type="button" className="secondary" onClick={() => act(`/api/podcasts/episodes/${e.id}/listen`, 'Added to Up next.')}>
                  Try again
                </button>
              </EpisodeRow>
            ))}
          </ul>
        </section>
      )}
      <section className="pod-section">
        <div className="section-head">
          <h3 className="label">Listened to</h3>
          <span className="sub">newest first</span>
        </div>
        {heard.length === 0 ? (
          <Empty>{q ? `Nothing she heard matches “${query.trim()}”.` : view.feeds.length > 0 ? 'Nothing finished yet.' : 'Nothing yet. Add a show and she will start on its newest episode.'}</Empty>
        ) : (
          <div className="pod-heard-grid">
            {heard.map((e) => (
              <Heard key={e.id} feed={feed(e.feed_id)} e={e} onOpen={() => open(e.id)} />
            ))}
          </div>
        )}
      </section>
    </>
  )
}

const statusWord: Record<Episode['status'], string> = {
  waiting: 'not heard',
  queued: 'up next',
  downloading: 'listening now',
  transcribing: 'listening now',
  reading: 'listening now',
  thinking: 'listening now',
  done: 'listened',
  failed: 'did not work',
}

// One show: what it is, and its episodes newest first, each with the one
// thing that can be done with it.
function Show({ feed, act, open, onRemoved }: { feed: Feed; act: (path: string, done: string) => void; open: (id: number) => void; onRemoved: () => void }) {
  const { data, error, reload } = useLoad<Episode[]>(`/api/podcasts/feeds/${feed.id}/episodes`)
  const [filter, setFilter] = useState<'all' | 'waiting' | 'done'>('all')
  const [confirm, setConfirm] = useState(false)
  const [problem, setProblem] = useState('')
  useSignals((s) => {
    if (s.kind === 'podcasts') reload()
  })
  const episodes = (data ?? []).filter((e) => filter === 'all' || e.status === filter)
  const remove = () =>
    void api(`/api/podcasts/feeds/${feed.id}`, { method: 'DELETE' })
      .then(onRemoved)
      .catch((e: Error) => setProblem(e.message))

  return (
    <>
      <header className="pod-show-head">
        <Art feed={feed} size="big" />
        <div>
          <span className="eyebrow">Show</span>
          <h2>{feed.title || feed.url}</h2>
          {feed.description && <p className="pod-desc">{feed.description}</p>}
          <span className={feed.error ? 'sub pod-failed' : 'sub'}>{feed.error ? `Last check failed: ${feed.error}` : isZero(feed.checked_at) ? 'Not checked yet' : `Checked ${when(feed.checked_at)}`}</span>
        </div>
        <button type="button" className="secondary danger" onClick={() => setConfirm(true)}>
          Stop following
        </button>
      </header>
      {confirm && (
        <div className="pod-confirm">
          <span>Stop following {feed.title || 'this show'}? What she learned from it stays with her.</span>
          <button type="button" className="secondary" onClick={() => setConfirm(false)}>
            Keep it
          </button>
          <button type="button" className="secondary danger" onClick={remove}>
            Stop following
          </button>
        </div>
      )}
      <Problem message={error || problem} />
      <section className="pod-section">
        <div className="section-head">
          <h3 className="label">Episodes, newest first</h3>
          <div className="mem-tabs" role="group" aria-label="Which episodes">
            {(
              [
                ['all', 'All'],
                ['waiting', 'Not heard'],
                ['done', 'Listened'],
              ] as const
            ).map(([k, label]) => (
              <button key={k} type="button" aria-pressed={filter === k} onClick={() => setFilter(k)}>
                {label}
              </button>
            ))}
          </div>
        </div>
        {data && episodes.length === 0 && <Empty>No episodes here.</Empty>}
        <ul className="pod-rows">
          {episodes.map((e) => (
            <EpisodeRow key={e.id} feed={feed} e={e}>
              <span className={`pod-status ${listening(e) ? 'live' : e.status}`}>{statusWord[e.status]}</span>
              {e.status === 'waiting' && (
                <button type="button" className="primary" onClick={() => act(`/api/podcasts/episodes/${e.id}/listen`, 'Added to Up next.')}>
                  Listen to this one
                </button>
              )}
              {e.status === 'failed' && (
                <button type="button" className="secondary" onClick={() => act(`/api/podcasts/episodes/${e.id}/listen`, 'Added to Up next.')}>
                  Try again
                </button>
              )}
              {e.status === 'queued' && (
                <button type="button" className="secondary" onClick={() => act(`/api/podcasts/episodes/${e.id}/unqueue`, 'Taken out of the queue.')}>
                  Take out of queue
                </button>
              )}
              {e.status === 'done' && (
                <button type="button" className="secondary" onClick={() => open(e.id)}>
                  Open
                </button>
              )}
            </EpisodeRow>
          ))}
        </ul>
      </section>
    </>
  )
}

// The transcript in its own scroll box, with a search that marks and
// counts what it finds.
function Transcript({ id }: { id: number }) {
  const [text, setText] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [problem, setProblem] = useState('')
  if (text === null)
    return (
      <>
        <Problem message={problem} />
        <button
          type="button"
          className="linkish"
          onClick={() =>
            void api<{ text: string }>(`/api/podcasts/episodes/${id}/transcript`)
              .then((t) => setText(t.text))
              .catch((err: Error) => setProblem(err.message))
          }
        >
          Read the full transcript
        </button>
      </>
    )
  const q = query.trim()
  const parts = q ? text.split(new RegExp(`(${q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi')) : [text]
  const hits = (parts.length - 1) / 2
  return (
    <>
      <div className="section-head">
        <SearchBox value={query} onChange={setQuery} label="Search the transcript" />
        <span className="sub">{q ? `${hits} ${hits === 1 ? 'match' : 'matches'}` : 'search to find a moment'}</span>
      </div>
      <div className="pod-transcript">{parts.map((p, i) => (i % 2 === 1 ? <mark key={i}>{p}</mark> : p))}</div>
    </>
  )
}

// Her notes as a timeline. Each part shows two lines until opened.
function Parts({ notes }: { notes: Episode['notes'] }) {
  const [open, setOpen] = useState<Set<number>>(() => new Set([0]))
  const toggle = (i: number) =>
    setOpen((was) => {
      const next = new Set(was)
      if (!next.delete(i)) next.add(i)
      return next
    })
  return (
    <ol className="pod-timeline">
      {notes.map((n, i) => (
        <li key={i} className={open.has(i) ? 'open' : undefined}>
          <span className="at">
            <span>{n.at}</span>
          </span>
          <button type="button" aria-expanded={open.has(i)} onClick={() => toggle(i)}>
            <span className="text">{n.text}</span>
            <span className="hint">{open.has(i) ? 'fold' : 'read part'}</span>
          </button>
        </li>
      ))}
    </ol>
  )
}

const sections = [
  ['short', 'In short'],
  ['hers', 'Her thoughts'],
  ['hosts', 'Hosts said'],
  ['transcript', 'Transcript'],
] as const
type Section = (typeof sections)[number][0]

// One episode opened. The header and the section tabs stay put; below
// them one scroll holds what she kept, with her notes beside it. The tabs
// jump to each section and follow the one being read.
function EpisodePage({ id, back, backLabel, feed }: { id: number; back: () => void; backLabel: string; feed: (id: number) => Feed | undefined }) {
  const { data, error, reload } = useLoad<{ episode: Episode; show: string; kept: Memory[] }>(`/api/podcasts/episodes/${id}`)
  const [problem, setProblem] = useState('')
  const [current, setCurrent] = useState<Section>('short')
  const [scrolled, setScrolled] = useState(false)
  const body = useRef<HTMLDivElement>(null)
  const forget = (m: Memory) =>
    void api(`/api/memories/${m.id}/forget`, { method: 'POST' })
      .then(reload)
      .catch((err: Error) => setProblem(err.message))
  const kept = (kind: string) => (data?.kept ?? []).filter((m) => (m.kind as string) === kind)
  const again = () =>
    void api(`/api/podcasts/episodes/${id}/listen`, { method: 'POST' })
      .then(back)
      .catch((err: Error) => setProblem(err.message))
  const e = data?.episode
  const shown = sections.filter(([k]) => k !== 'transcript' || e?.has_transcript)
  const count: Partial<Record<Section, number>> = { hers: kept('view').length, hosts: kept('heard').length }

  const section = (k: Section) => body.current?.querySelector<HTMLElement>(`[data-section="${k}"]`)
  const jump = (k: Section) => {
    const el = section(k)
    if (!el || !body.current) return
    body.current.scrollTo({ top: el.offsetTop - 8, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
  }
  // The section being read is the last one whose top has passed the top
  // of the scroll; at the very bottom it is the last one.
  const onScroll = () => {
    const el = body.current
    if (!el) return
    setScrolled(el.scrollTop > 0)
    let at: Section = 'short'
    for (const [k] of shown) {
      const s = section(k)
      if (s && s.offsetTop - 40 <= el.scrollTop) at = k
    }
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 4) at = shown[shown.length - 1][0]
    setCurrent(at)
  }

  const keptList = (kind: string, hers: boolean) => (
    <ol className={hers ? 'pod-kept hers' : 'pod-kept'}>
      {kept(kind).map((m, i) => (
        <li key={m.id}>
          {!hers && <span className="num">{String(i + 1).padStart(2, '0')}</span>}
          <span>{m.content}</span>
          <button type="button" className="mem-act" title="Forget" aria-label="Forget" onClick={() => forget(m)}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </li>
      ))}
    </ol>
  )

  return (
    <div className="pod-ep">
      <div className={scrolled ? 'pod-ep-top scrolled' : 'pod-ep-top'}>
        <button type="button" className="linkish pod-back" onClick={back}>
          ← {backLabel}
        </button>
        {e && (
          <header className="pod-show-head">
            <Art feed={feed(e.feed_id)} size="big" />
            <div>
              <span className="eyebrow">{meta(data.show, e)}</span>
              <h2>{e.title}</h2>
            </div>
            <button type="button" className="secondary" title="She rereads the transcript and replaces what she kept from it. Nothing is downloaded again." onClick={again}>
              Have her go over it again
            </button>
          </header>
        )}
        {e && (
          <nav className="pod-jump" aria-label="Sections">
            {shown.map(([k, label]) => (
              <button key={k} type="button" aria-current={current === k} onClick={() => jump(k)}>
                {label}
                {count[k] !== undefined && <span className="n">{count[k]}</span>}
              </button>
            ))}
          </nav>
        )}
      </div>
      <Problem message={error || problem} />
      {e && (
        <div className="pod-ep-body" ref={body} onScroll={onScroll}>
          <div className="pod-episode">
            <div className="pod-col">
              <section className="pod-section" data-section="short">
                <h3 className="label">In short</h3>
                <p className="pod-summary">{e.summary}</p>
              </section>
              {e.share && (
                <div className="pod-bring">
                  <span className="label">She wants to bring up</span>
                  <p>{e.share}</p>
                </div>
              )}
              <section className="pod-section" data-section="hers">
                <h3 className="label">What she thought</h3>
                <p className="note">Her own views on it. They are hers, not the show's.</p>
                {kept('view').length === 0 ? <Empty>She did not form a view of her own on this one.</Empty> : keptList('view', true)}
              </section>
              <section className="pod-section" data-section="hosts">
                <div className="section-head">
                  <h3 className="label">What the hosts said</h3>
                  <span className="sub">{kept('heard').length} kept</span>
                </div>
                <p className="note">These sit in her memory marked as heard on this show, apart from what you have told her.</p>
                {kept('heard').length === 0 ? <Empty>She is not holding anything from this episode.</Empty> : keptList('heard', false)}
              </section>
              {e.has_transcript && (
                <section className="pod-section" data-section="transcript">
                  <h3 className="label">Transcript</h3>
                  <Transcript id={id} />
                </section>
              )}
            </div>
            <aside className="pod-notes">
              <div className="section-head">
                <h3 className="label">Her notes, part by part</h3>
                <span className="sub">tap to open</span>
              </div>
              <Parts notes={e.notes} />
            </aside>
          </div>
        </div>
      )}
    </div>
  )
}

export function Podcasts() {
  const { data, error, reload } = useLoad<PodcastsView>('/api/podcasts')
  const [view, setView] = useState<View>({ kind: 'all' })
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const [toast, setToast] = useState('')
  useSignals((s) => {
    if (s.kind === 'podcasts') reload()
  })
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(''), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const feed = (id: number) => data?.feeds.find((f) => f.id === id)
  const act = (path: string, done: string) =>
    void api(path, { method: 'POST' })
      .then(() => {
        setProblem('')
        setToast(done)
        reload()
      })
      .catch((e: Error) => setProblem(e.message))
  const open = (from: 'all' | number) => (id: number) => setView({ kind: 'episode', id, from })

  async function add() {
    setBusy(true)
    setProblem('')
    try {
      const added = await api<Feed>('/api/podcasts/feeds', { method: 'POST', body: { url: url.trim() } })
      setUrl('')
      reload()
      setView({ kind: 'show', id: added.id })
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const shown = view.kind === 'show' ? feed(view.id) : undefined
  const selected = view.kind === 'show' ? view.id : view.kind === 'episode' && view.from !== 'all' ? view.from : 0

  return (
    <Screen eyebrow="What she listens to while you are busy" title="Podcasts" className="pod-screen">
      <div className="pod-layout">
        <nav className="pod-rail" aria-label="Shows">
          <ul>
            <li>
              <button type="button" aria-pressed={selected === 0} onClick={() => setView({ kind: 'all' })}>
                <span className="pod-art all" aria-hidden="true">∗</span>
                <span className="pod-row-text">
                  <b>All shows</b>
                  <span className="sub">{data?.done.length ?? 0} listened</span>
                </span>
                <span />
              </button>
            </li>
            {(data?.feeds ?? []).map((f) => {
              const unheard = data?.unheard[String(f.id)] ?? 0
              return (
                <li key={f.id}>
                  <button type="button" aria-pressed={selected === f.id} onClick={() => setView({ kind: 'show', id: f.id })}>
                    <Art feed={f} />
                    <span className="pod-row-text">
                      <b>{f.title || f.url}</b>
                      <span className={f.error ? 'sub pod-failed' : 'sub'}>{f.error ? 'last check failed' : `${data?.listened[String(f.id)] ?? 0} listened`}</span>
                    </span>
                    {unheard > 0 ? (
                      <span className="pod-count" title={`${unheard} not heard yet`}>
                        {unheard}
                      </span>
                    ) : (
                      <span />
                    )}
                  </button>
                </li>
              )
            })}
          </ul>
          <form
            className="pod-add"
            onSubmit={(e) => {
              e.preventDefault()
              if (url.trim()) void add()
            }}
          >
            <label htmlFor="feed" className="label">
              Add a show
            </label>
            <input id="feed" type="url" value={url} placeholder="Paste an RSS feed address" onChange={(e) => setUrl(e.target.value)} />
            <button type="submit" className="secondary" disabled={busy || !url.trim()}>
              {busy ? 'Reading the feed' : 'Add show'}
            </button>
            <p className="sub">She starts with its newest episode, then picks up each new one. Older episodes wait until you ask.</p>
          </form>
        </nav>

        <div className={view.kind === 'episode' ? 'pod-main episode' : 'pod-main'}>
          <Problem message={error || problem} />
          {data?.missing && <Problem message={`She cannot write episodes out yet: ${data.missing}.`} />}
          {data && !data.enabled && <Problem message="Listening on her own is switched off in Settings." />}
          {data && view.kind === 'all' && <AllShows view={data} feed={feed} act={act} open={open('all')} />}
          {data && view.kind === 'show' && shown && (
            <Show
              key={shown.id}
              feed={shown}
              act={act}
              open={open(shown.id)}
              onRemoved={() => {
                setToast(`Stopped following ${shown.title || 'the show'}.`)
                setView({ kind: 'all' })
                reload()
              }}
            />
          )}
          {view.kind === 'episode' && (
            <EpisodePage
              key={view.id}
              id={view.id}
              feed={feed}
              backLabel={view.from === 'all' ? 'All shows' : (feed(view.from)?.title ?? 'Back')}
              back={() => setView(view.from === 'all' ? { kind: 'all' } : { kind: 'show', id: view.from })}
            />
          )}
        </div>
      </div>
      {toast && (
        <div className="mem-toast" role="status">
          <span>{toast}</span>
        </div>
      )}
    </Screen>
  )
}
