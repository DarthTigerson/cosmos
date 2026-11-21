import { useEffect, useState } from 'react'
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

// The episode she is working through, with the four stages it passes.
function Listening({ show, e }: { show: string; e: Episode }) {
  const at = order.indexOf(e.status as (typeof order)[number])
  return (
    <section className="pod-card" aria-label="Listening now">
      <div className="pod-top">
        <span className="sub pod-now">Listening now</span>
        <span className="sub">{meta(show, e)}</span>
      </div>
      <h3>{e.title}</h3>
      <ol className="stages">
        {order.map((s, i) => (
          <li key={s} className={i < at ? 'done' : i === at ? 'now' : undefined}>
            <span className="bar" />
            <span className="stage-name">{stageName[s]}</span>
            <span className="sub">{i === at && e.stage_note ? e.stage_note : stageHint[s]}</span>
          </li>
        ))}
      </ol>
      <p className="muted" style={{ fontSize: 13 }}>
        After the download nothing leaves this computer. If you write to her she answers you between parts.
      </p>
    </section>
  )
}

function Listened({ show, e, onOpen }: { show: string; e: Episode; onOpen: () => void }) {
  return (
    <article className="pod-card">
      <div className="pod-top">
        <h3>{e.title}</h3>
        <span className="sub">{meta(show, e)}</span>
      </div>
      <p className="muted">{e.summary}</p>
      <ul className="took">
        {e.takeaways.slice(0, 3).map((t) => (
          <li key={t}>
            <span className="tag">picked up </span>
            {t}
          </li>
        ))}
        {(e.thoughts ?? []).slice(0, 2).map((t) => (
          <li key={t}>
            <span className="tag hers">her view </span>
            {t}
          </li>
        ))}
        {e.share && (
          <li>
            <span className="tag hers">wants to tell you </span>
            {e.share}
          </li>
        )}
      </ul>
      <div className="pod-links">
        <button type="button" className="linkish" onClick={onOpen}>
          Everything she took from it
        </button>
      </div>
    </article>
  )
}

// One episode opened: all she kept, her notes part by part, the transcript.
function EpisodePage({ id, onBack }: { id: number; onBack: () => void }) {
  const { data, error, reload } = useLoad<{ episode: Episode; show: string; kept: Memory[] }>(`/api/podcasts/episodes/${id}`)
  const forget = (m: Memory) =>
    void api(`/api/memories/${m.id}/forget`, { method: 'POST' })
      .then(reload)
      .catch((err: Error) => setProblem(err.message))
  const kept = (kind: string) => (data?.kept ?? []).filter((m) => (m.kind as string) === kind)
  const again = () =>
    void api(`/api/podcasts/episodes/${id}/listen`, { method: 'POST' })
      .then(onBack)
      .catch((err: Error) => setProblem(err.message))
  const [transcript, setTranscript] = useState('')
  const [problem, setProblem] = useState('')
  const e = data?.episode
  return (
    <Screen eyebrow={e ? meta(data.show, e) : 'Podcast episode'} title={e?.title ?? 'Episode'} action={<button type="button" className="secondary" onClick={onBack}>Back to podcasts</button>}>
      <Problem message={error || problem} />
      {e && (
        <div className="pod">
          <section className="pod">
            <h2>In short</h2>
            <p>{e.summary}</p>
          </section>
          <section className="pod" style={{ gap: 10 }}>
            <h2>What the hosts said</h2>
            <p className="muted" style={{ fontSize: 13 }}>
              These sit in her memory marked as heard on this show, apart from what you have told her. Forget any she should not keep.
            </p>
            {kept('heard').length === 0 && <Empty>She is not holding anything from this episode.</Empty>}
            <ul className="cards">
              {kept('heard').map((m) => (
                <li key={m.id} className="keepable">
                  <span>{m.content}</span>
                  <button type="button" className="secondary" onClick={() => forget(m)}>
                    Forget
                  </button>
                </li>
              ))}
            </ul>
          </section>
          <section className="pod" style={{ gap: 10 }}>
            <h2>What she thought</h2>
            <p className="muted" style={{ fontSize: 13 }}>Her own views on it. They are hers, not the show's.</p>
            {kept('view').length === 0 && <Empty>She did not form a view of her own on this one.</Empty>}
            <ul className="cards">
              {kept('view').map((m) => (
                <li key={m.id} className="keepable hers">
                  <span>{m.content}</span>
                  <button type="button" className="secondary" onClick={() => forget(m)}>
                    Forget
                  </button>
                </li>
              ))}
            </ul>
          </section>
          {e.share && (
            <section className="pod" style={{ gap: 10 }}>
              <h2>What she wants to bring up with you</h2>
              <div className="bring-up">
                <p>{e.share}</p>
              </div>
            </section>
          )}
          <section className="pod" style={{ gap: 4 }}>
            <h2>Her notes, part by part</h2>
            <ul className="part-notes">
              {e.notes.map((n, i) => (
                <li key={i}>
                  <span className="at">{n.at}</span>
                  <span className="said">{n.text}</span>
                </li>
              ))}
            </ul>
            {e.has_transcript && !transcript && (
              <button
                type="button"
                className="linkish"
                onClick={() =>
                  void api<{ text: string }>(`/api/podcasts/episodes/${id}/transcript`)
                    .then((t) => setTranscript(t.text))
                    .catch((err: Error) => setProblem(err.message))
                }
              >
                Read the full transcript
              </button>
            )}
            {transcript && <div className="transcript">{transcript}</div>}
          </section>
          <section className="pod" style={{ gap: 8 }}>
            <button type="button" className="secondary" style={{ alignSelf: 'flex-start' }} onClick={again}>
              Have her go over it again
            </button>
            <p className="muted" style={{ fontSize: 13 }}>She rereads the transcript and replaces what she kept from it. Nothing is downloaded again.</p>
          </section>
        </div>
      )}
    </Screen>
  )
}

function Art({ feed }: { feed: Feed }) {
  const url = useAsset(`/api/podcasts/feeds/${feed.id}/art`)
  if (!feed.has_art || !url) return <div className="show-art" aria-hidden="true">{(feed.title || '?').slice(0, 1)}</div>
  return <img className="show-art" src={url} alt={`Artwork for ${feed.title}`} />
}

// A show's other episodes, so an older one can be asked for.
function Earlier({ feed, onChange, onOpen }: { feed: Feed; onChange: () => void; onOpen: (id: number) => void }) {
  const { data, error, reload } = useLoad<Episode[]>(`/api/podcasts/feeds/${feed.id}/episodes`)
  const [problem, setProblem] = useState('')
  useSignals((s) => {
    if (s.kind === 'podcasts') reload()
  })
  const word = { waiting: '', queued: 'in the queue', downloading: 'listening now', transcribing: 'listening now', reading: 'listening now', thinking: 'listening now', done: 'listened', failed: 'did not work' }
  return (
    <section className="pod-card">
      <h3>Episodes of {feed.title}</h3>
      <Problem message={error || problem} />
      <ul className="ep-rows">
        {(data ?? []).map((e) => (
          <li key={e.id}>
            <span style={{ display: 'flex', flexDirection: 'column', minWidth: 0, flex: '1 1 320px' }}>
              <span>{e.title}</span>
              <span className="sub">
                {meta('', e)}
                {word[e.status] && ` · ${word[e.status]}`}
                {e.status === 'failed' && e.error && `: ${e.error}`}
              </span>
            </span>
            {(e.status === 'waiting' || e.status === 'failed') && (
              <button
                type="button"
                className="secondary"
                onClick={() =>
                  void api(`/api/podcasts/episodes/${e.id}/listen`, { method: 'POST' })
                    .then(() => {
                      reload()
                      onChange()
                    })
                    .catch((err: Error) => setProblem(err.message))
                }
              >
                {e.status === 'failed' ? 'Try again' : 'Listen to this one'}
              </button>
            )}
            {e.status === 'done' && (
              <button type="button" className="secondary" onClick={() => onOpen(e.id)}>
                Open
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

function Shows({ view, onChange, onOpen }: { view: PodcastsView; onChange: () => void; onOpen: (id: number) => void }) {
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const [open, setOpen] = useState(0)
  const feed = view.feeds.find((f) => f.id === open)

  async function add() {
    setBusy(true)
    setProblem('')
    try {
      await api('/api/podcasts/feeds', { method: 'POST', body: { url: url.trim() } })
      setUrl('')
      onChange()
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="pod" style={{ maxWidth: 'none' }}>
      <form
        className="add-show"
        onSubmit={(e) => {
          e.preventDefault()
          if (url.trim()) void add()
        }}
      >
        <label htmlFor="feed" className="visually-hidden">
          RSS feed address
        </label>
        <input id="feed" type="url" value={url} placeholder="Paste a podcast's RSS feed address" onChange={(e) => setUrl(e.target.value)} />
        <button type="submit" className="primary" disabled={busy || !url.trim()}>
          {busy ? 'Reading the feed' : 'Add show'}
        </button>
      </form>
      <Problem message={problem} />
      <p className="muted" style={{ fontSize: 13, maxWidth: '64ch' }}>
        When you add a show she starts with its newest episode, then picks up each new one as it comes out. Older episodes wait until you ask for them.
      </p>
      {view.feeds.length === 0 && <Empty>No shows yet.</Empty>}
      <div className="shows">
        {view.feeds.map((f) => (
          <article key={f.id} className="show">
            <Art feed={f} />
            <h3>{f.title || f.url}</h3>
            <p>{f.description}</p>
            <span className="sub">
              {view.listened[String(f.id)] ?? 0} listened{!isZero(f.checked_at) && ` · checked ${when(f.checked_at)}`}
            </span>
            {f.error && <Problem message={`Last check failed: ${f.error}`} />}
            <div className="show-actions">
              <button type="button" className="secondary" aria-expanded={open === f.id} onClick={() => setOpen(open === f.id ? 0 : f.id)}>
                Episodes
              </button>
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  if (!window.confirm(`Stop following ${f.title || 'this show'}? What she learned from it stays with her.`)) return
                  void api(`/api/podcasts/feeds/${f.id}`, { method: 'DELETE' })
                    .then(onChange)
                    .catch((e: Error) => setProblem(e.message))
                }}
              >
                Remove
              </button>
            </div>
          </article>
        ))}
      </div>
      {feed && <Earlier feed={feed} onChange={onChange} onOpen={onOpen} />}
    </div>
  )
}

export function Podcasts() {
  const { data, error, reload } = useLoad<PodcastsView>('/api/podcasts')
  const [tab, setTab] = useState<'listening' | 'shows'>('listening')
  const [episode, setEpisode] = useState(0)
  useSignals((s) => {
    if (s.kind === 'podcasts') reload()
  })
  // With nothing followed yet, the first thing to do is add a show.
  useEffect(() => {
    if (data && data.feeds.length === 0) setTab('shows')
  }, [data?.feeds.length === 0])

  if (episode) return <EpisodePage id={episode} onBack={() => setEpisode(0)} />
  const show = (e: Episode) => data?.feeds.find((f) => f.id === e.feed_id)?.title ?? ''

  return (
    <Screen eyebrow="What she listens to while you are busy" title="Podcasts">
      <Problem message={error} />
      {data?.missing && <Problem message={`She cannot write episodes out yet: ${data.missing}.`} />}
      {data && !data.enabled && <Problem message="Listening on her own is switched off in Settings." />}
      <div className="tabs" role="tablist">
        <button type="button" role="tab" aria-selected={tab === 'listening'} onClick={() => setTab('listening')}>
          Listening
        </button>
        <button type="button" role="tab" aria-selected={tab === 'shows'} onClick={() => setTab('shows')}>
          Shows
        </button>
      </div>
      {data && tab === 'shows' && <Shows view={data} onChange={reload} onOpen={setEpisode} />}
      {data && tab === 'listening' && (
        <div className="pod">
          {data.current && <Listening show={show(data.current)} e={data.current} />}
          {data.queued.length > 0 && (
            <section className="pod" style={{ gap: 8 }}>
              <h2>Up next</h2>
              <ul className="ep-rows">
                {data.queued.map((e) => (
                  <li key={e.id}>
                    <span>{e.title}</span>
                    <span className="sub">{meta(show(e), e)}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {data.failed.length > 0 && (
            <section className="pod" style={{ gap: 8 }}>
              <h2>Did not work</h2>
              <ul className="ep-rows">
                {data.failed.map((e) => (
                  <li key={e.id}>
                    <span style={{ display: 'flex', flexDirection: 'column', minWidth: 0, flex: '1 1 320px' }}>
                      <span>{e.title}</span>
                      <span className="sub">{e.error}</span>
                    </span>
                    <button type="button" className="secondary" onClick={() => void api(`/api/podcasts/episodes/${e.id}/listen`, { method: 'POST' }).then(reload)}>
                      Try again
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
          <section className="pod" style={{ gap: 12 }}>
            <h2>Listened to</h2>
            {data.done.length === 0 && <Empty>{data.feeds.length > 0 ? 'Nothing finished yet.' : 'Nothing yet. Add a show and she will start on its newest episode.'}</Empty>}
            {data.done.map((e) => (
              <Listened key={e.id} show={show(e)} e={e} onOpen={() => setEpisode(e.id)} />
            ))}
          </section>
        </div>
      )}
    </Screen>
  )
}
