import { useEffect, useRef, useState, type ReactNode } from 'react'
import { api, upload, useAsset, useLoad, useSignals } from '../api'
import { isZero, when } from '../format'
import { Empty, Problem, Screen } from '../parts'
import type { Book, BooksView, Memory } from '../types'

const kindWord = (b: Book) => (b.kind === 'fiction' ? 'fiction' : 'non-fiction')

function words(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k words` : `${n} words`
}

function meta(b: Book): string {
  return [b.author, kindWord(b), b.chapters.length ? `${b.chapters.length} chapters` : '', b.words ? words(b.words) : ''].filter(Boolean).join(' · ')
}

const order = ['opening', 'reading', 'digesting', 'thinking'] as const
const stageName = { opening: 'Opening', reading: 'Reading', digesting: 'Chapters', thinking: 'Thinking it over' }
const stageHint = {
  opening: 'unpacking the book',
  reading: 'part by part',
  digesting: 'a digest of each',
  thinking: 'what to keep, what to tell you',
}
const reading = (b: Book) => (order as readonly string[]).includes(b.status)

// How far through the stage she is, or null when the stage gives no measure.
function stageFill(b: Book): number | null {
  if (b.status === 'reading') return b.parts ? (b.part / b.parts) * 100 : null
  if (b.status === 'digesting') {
    const ch = /chapter (\d+) of (\d+)/.exec(b.stage_note)
    if (ch) return (Number(ch[1]) / Number(ch[2])) * 100
  }
  return null
}

// The last note she wrote, with the title of the chapter it belongs to.
function latestNote(b: Book): { title: string; text: string } | null {
  for (let i = b.chapters.length - 1; i >= 0; i--) {
    const notes = b.chapters[i].notes.filter(Boolean)
    if (notes.length > 0) return { title: `Chapter ${b.chapters[i].n} · ${b.chapters[i].title}`, text: notes[notes.length - 1] }
  }
  return null
}

function Cover({ book, size }: { book?: Book; size?: 'big' }) {
  const url = useAsset(`/api/books/${book?.id ?? 0}/cover`)
  const cls = `pod-art ${size ?? ''}`
  if (!book?.has_cover || !url) return <span className={cls} aria-hidden="true">{(book?.title || '?').replace(/^(The|A|An) /, '').slice(0, 1)}</span>
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

// Marks every place the query occurs in a piece of text.
function marked(text: string, q: string): ReactNode[] {
  if (!q) return [text]
  const parts = text.split(new RegExp(`(${q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi'))
  return parts.map((p, i) => (i % 2 === 1 ? <mark key={i}>{p}</mark> : p))
}

// Whether the book is read as fiction or not changes how she keeps it.
function ReadAs({ book, onDone, onProblem }: { book: Book; onDone: () => void; onProblem: (m: string) => void }) {
  return (
    <label className="book-readas">
      read as
      <select
        aria-label={`Read ${book.title} as`}
        value={book.kind}
        onChange={(e) =>
          void api(`/api/books/${book.id}/kind`, { method: 'POST', body: { kind: e.target.value } })
            .then(onDone)
            .catch((err: Error) => onProblem(err.message))
        }
      >
        <option value="nonfiction">non-fiction</option>
        <option value="fiction">fiction</option>
      </select>
    </label>
  )
}

// The book she is working through, with the four stages it passes.
function Reading({ book, paused }: { book: Book; paused: boolean }) {
  const at = order.indexOf(book.status as (typeof order)[number])
  const fill = stageFill(book)
  const note = latestNote(book)
  return (
    <section className="pod-now" aria-label="Reading now">
      <Cover book={book} size="big" />
      <div className="pod-now-body">
        <div className="pod-now-top">
          <span className="sub strong">Reading now</span>
          <span className="sub">{meta(book)}</span>
          {paused && <span className="pod-status live">paused · goes on while she sleeps</span>}
        </div>
        <h2>{book.title}</h2>
        <ol className="pod-stages">
          {order.map((s, i) => (
            <li key={s} className={i < at ? 'done' : i === at ? 'now' : undefined}>
              <span className={i === at && fill === null ? 'bar busy' : 'bar'}>
                <i style={{ width: `${i < at ? 100 : i === at ? (fill ?? 100) : 0}%` }} />
              </span>
              <span className="stage-name">{stageName[s]}</span>
              <span className="sub">{i === at && book.stage_note ? book.stage_note : i < at ? 'done' : stageHint[s]}</span>
            </li>
          ))}
        </ol>
        {note && (
          <div className="pod-bring">
            <span className="label">{note.title}</span>
            <p>Latest note: {note.text}</p>
          </div>
        )}
        <p className="sub">The book never leaves this Mac. If you write to her she answers you between parts, then carries on.</p>
      </div>
    </section>
  )
}

function BookRow({ book, children }: { book: Book; children?: ReactNode }) {
  return (
    <li>
      <Cover book={book} />
      <span className="pod-row-text">
        <b>{book.title}</b>
        <span className="sub">{book.status === 'failed' && book.error ? book.error : meta(book)}</span>
      </span>
      <span className="pod-row-acts">{children}</span>
    </li>
  )
}

function Finished({ book, onOpen }: { book: Book; onOpen: () => void }) {
  return (
    <button type="button" className="pod-heard" onClick={onOpen}>
      <span className="pod-heard-head">
        <Cover book={book} />
        <span>
          <b>{book.title}</b>
          <span className="sub">{[book.author, isZero(book.finished_at) ? '' : `read ${when(book.finished_at)}`].filter(Boolean).join(' · ')}</span>
        </span>
      </span>
      <span className="pod-heard-summary">{book.summary}</span>
      <span className="pod-chips">
        <span className="pod-chip">{kindWord(book)}</span>
        <span className="pod-chip">{book.chapters.reduce((n, c) => n + c.learned.length, 0)} learned</span>
        {(book.thoughts ?? []).length > 0 && <span className="pod-chip hers">{book.thoughts.length} her view</span>}
        {book.share && <span className="pod-chip tell">wants to tell you</span>}
      </span>
    </button>
  )
}

function AllBooks({ view, act, reload, onProblem, open }: { view: BooksView; act: (path: string, done: string) => void; reload: () => void; onProblem: (m: string) => void; open: (id: number) => void }) {
  const [query, setQuery] = useState('')
  const q = query.trim().toLowerCase()
  const waiting = view.books.filter((b) => b.status === 'waiting')
  const read = view.done.filter((b) => !q || [b.title, b.author, b.summary, ...(b.thoughts ?? [])].join(' ').toLowerCase().includes(q))
  return (
    <>
      <div className="pod-head">
        <div>
          <span className="eyebrow">All books</span>
          <h2>Reading</h2>
        </div>
        <SearchBox value={query} onChange={setQuery} label="Search what she read" />
      </div>
      {view.current && <Reading book={view.current} paused={view.paused} />}
      {view.queued.length > 0 && (
        <section className="pod-section">
          <h3 className="label">Tonight, while she sleeps</h3>
          <ul className="pod-rows">
            {view.queued.map((b) => (
              <BookRow key={b.id} book={b}>
                <ReadAs book={b} onDone={reload} onProblem={onProblem} />
                <button type="button" className="secondary" onClick={() => act(`/api/books/${b.id}/read?now=1`, 'She starts on it now.')}>
                  Read now
                </button>
                <button type="button" className="secondary" onClick={() => act(`/api/books/${b.id}/unqueue`, 'Taken out of the queue.')}>
                  Take out of queue
                </button>
              </BookRow>
            ))}
          </ul>
        </section>
      )}
      {waiting.length > 0 && (
        <section className="pod-section">
          <h3 className="label">Not read yet</h3>
          <ul className="pod-rows">
            {waiting.map((b) => (
              <BookRow key={b.id} book={b}>
                <ReadAs book={b} onDone={reload} onProblem={onProblem} />
                <button type="button" className="primary" onClick={() => act(`/api/books/${b.id}/read`, 'She will get to it tonight, while she sleeps.')}>
                  Read tonight
                </button>
                <button type="button" className="secondary" onClick={() => act(`/api/books/${b.id}/read?now=1`, 'She starts on it now.')}>
                  Read now
                </button>
              </BookRow>
            ))}
          </ul>
        </section>
      )}
      {view.failed.length > 0 && (
        <section className="pod-section">
          <h3 className="label pod-failed">Did not work</h3>
          <ul className="pod-rows">
            {view.failed.map((b) => (
              <BookRow key={b.id} book={b}>
                <button type="button" className="secondary" onClick={() => act(`/api/books/${b.id}/read`, 'She will get to it tonight, while she sleeps.')}>
                  Try again tonight
                </button>
                <button type="button" className="secondary" onClick={() => act(`/api/books/${b.id}/read?now=1`, 'She starts on it now.')}>
                  Try now
                </button>
              </BookRow>
            ))}
          </ul>
        </section>
      )}
      <section className="pod-section">
        <div className="section-head">
          <h3 className="label">Read</h3>
          <span className="sub">newest first</span>
        </div>
        {read.length === 0 ? (
          <Empty>{q ? `Nothing she read matches “${query.trim()}”.` : view.books.length > 0 ? 'Nothing finished yet.' : 'Nothing yet. Add a book and she will read it while she sleeps.'}</Empty>
        ) : (
          <div className="pod-heard-grid">
            {read.map((b) => (
              <Finished key={b.id} book={b} onOpen={() => open(b.id)} />
            ))}
          </div>
        )}
      </section>
    </>
  )
}

const pill: Record<Book['status'], string> = {
  waiting: 'not read',
  queued: 'tonight',
  opening: 'reading',
  reading: 'reading',
  digesting: 'reading',
  thinking: 'reading',
  done: 'read',
  failed: 'did not work',
}

type Match = { chapter: number; title: string; text: string }

// The exact text of the book, searched on the core, with the match marked.
function FullText({ id }: { id: number }) {
  const [query, setQuery] = useState('')
  const [found, setFound] = useState<{ total: number; matches: Match[] } | null>(null)
  const [problem, setProblem] = useState('')
  const q = query.trim()
  useEffect(() => {
    if (!q) {
      setFound(null)
      return
    }
    let live = true
    const t = setTimeout(() => {
      api<{ total: number; matches: Match[] }>(`/api/books/${id}/search?q=${encodeURIComponent(q)}`)
        .then((r) => {
          if (!live) return
          setFound(r)
          setProblem('')
        })
        .catch((err: Error) => live && setProblem(err.message))
    }, 300)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [id, q])
  return (
    <>
      <div className="section-head">
        <SearchBox value={query} onChange={setQuery} label="Search the book" />
        <span className="sub">{found ? `${found.total} ${found.total === 1 ? 'match' : 'matches'}` : 'search to find a passage'}</span>
      </div>
      <p className="note">The exact text is kept on this Mac, so she can quote a passage when you ask instead of going from memory.</p>
      <Problem message={problem} />
      {found && found.matches.length === 0 && <Empty>Nothing in the book matches “{q}”.</Empty>}
      {found && found.matches.length > 0 && (
        <ul className="book-matches">
          {found.matches.map((m, i) => (
            <li key={i}>
              <span className="sub">
                Chapter {m.chapter} · {m.title}
              </span>
              <span>{marked(m.text, q)}</span>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

// Her digest of each chapter, folded until opened.
function Chapters({ book }: { book: Book }) {
  const [open, setOpen] = useState<Set<number>>(() => new Set([book.chapters[0]?.n]))
  const toggle = (n: number) =>
    setOpen((was) => {
      const next = new Set(was)
      if (!next.delete(n)) next.add(n)
      return next
    })
  return (
    <ol className="book-chapters">
      {book.chapters.map((c) => (
        <li key={c.n} className={open.has(c.n) ? 'open' : undefined}>
          <button type="button" aria-expanded={open.has(c.n)} onClick={() => toggle(c.n)}>
            <span className="num">{c.n}</span>
            <b>{c.title}</b>
            <span className="sub">{c.learned.length} learned</span>
          </button>
          {open.has(c.n) && <p>{c.digest}</p>}
        </li>
      ))}
    </ol>
  )
}

const sections = [
  ['short', 'In short'],
  ['hers', 'Her thoughts'],
  ['book', 'What the book says'],
  ['chapters', 'Chapters'],
  ['text', 'Full text search'],
] as const
type Section = (typeof sections)[number][0]

// One book opened. The header and the section tabs stay put; below them
// one scroll holds what she kept. The tabs jump to each section and follow
// the one being read.
function BookPage({ id, back, onRemoved }: { id: number; back: () => void; onRemoved: (title: string) => void }) {
  const { data, error, reload } = useLoad<{ book: Book; kept: Memory[] }>(`/api/books/${id}`)
  const [problem, setProblem] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [current, setCurrent] = useState<Section>('short')
  const [scrolled, setScrolled] = useState(false)
  const body = useRef<HTMLDivElement>(null)
  useSignals((s) => {
    if (s.kind === 'books') reload()
  })
  const forget = (m: Memory) =>
    void api(`/api/memories/${m.id}/forget`, { method: 'POST' })
      .then(reload)
      .catch((err: Error) => setProblem(err.message))
  const kept = (kind: string) => (data?.kept ?? []).filter((m) => (m.kind as string) === kind)
  const again = () =>
    void api(`/api/books/${id}/read`, { method: 'POST' })
      .then(back)
      .catch((err: Error) => setProblem(err.message))
  const b = data?.book
  const remove = (forget: boolean) =>
    void api(`/api/books/${id}${forget ? '?forget=1' : ''}`, { method: 'DELETE' })
      .then(() => onRemoved(b?.title ?? 'the book'))
      .catch((err: Error) => setProblem(err.message))
  const count: Partial<Record<Section, number>> = { hers: kept('view').length, book: kept('read').length }

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
    for (const [k] of sections) {
      const s = section(k)
      if (s && s.offsetTop - 40 <= el.scrollTop) at = k
    }
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 4) at = sections[sections.length - 1][0]
    setCurrent(at)
  }

  const forgetButton = (m: Memory) => (
    <button type="button" className="mem-act" title="Forget" aria-label="Forget" onClick={() => forget(m)}>
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" aria-hidden="true">
        <path d="M4 4l8 8M12 4l-8 8" />
      </svg>
    </button>
  )

  return (
    <div className="pod-ep">
      <div className={scrolled ? 'pod-ep-top scrolled' : 'pod-ep-top'}>
        <button type="button" className="linkish pod-back" onClick={back}>
          ← All books
        </button>
        {b && (
          <header className="pod-show-head">
            <Cover book={b} size="big" />
            <div>
              <span className="eyebrow">Book · {kindWord(b)}</span>
              <h2>{b.title}</h2>
              <span className="sub">{[b.author, b.chapters.length ? `${b.chapters.length} chapters` : '', b.words ? words(b.words) : ''].filter(Boolean).join(' · ')}</span>
              {b.status === 'done' && !isZero(b.finished_at) && <span className="sub">{`Read ${when(b.finished_at)}${b.asleep ? ', while she was asleep' : ''}`}</span>}
            </div>
            <div className="book-acts">
              <ReadAs book={b} onDone={reload} onProblem={setProblem} />
              <button type="button" className="secondary" title="She reads it again and replaces what she kept from it." onClick={again}>
                Read again
              </button>
              <button type="button" className="secondary danger" onClick={() => setConfirm(true)}>
                Remove book
              </button>
            </div>
          </header>
        )}
        {b && (
          <nav className="pod-jump" aria-label="Sections">
            {sections.map(([k, label]) => (
              <button key={k} type="button" aria-current={current === k} onClick={() => jump(k)}>
                {label}
                {count[k] !== undefined && <span className="n">{count[k]}</span>}
              </button>
            ))}
          </nav>
        )}
      </div>
      {confirm && b && (
        <div className="pod-confirm">
          <span>
            Remove {b.title}?{reading(b) ? ' She stops reading it.' : ''} The book file goes. Keep what she learned from it, or forget it too.
          </span>
          <button type="button" className="secondary" onClick={() => setConfirm(false)}>
            Keep it
          </button>
          <button type="button" className="secondary danger" onClick={() => remove(false)}>
            Remove book
          </button>
          <button type="button" className="secondary danger" onClick={() => remove(true)}>
            Remove and forget
          </button>
        </div>
      )}
      <Problem message={error || problem} />
      {b && (
        <div className="pod-ep-body" ref={body} onScroll={onScroll}>
          <div className="pod-episode book-episode">
            <div className="pod-col">
              <section className="pod-section" data-section="short">
                <h3 className="label">In short</h3>
                {b.summary ? <p className="pod-summary">{b.summary}</p> : <Empty>She has not finished this one yet.</Empty>}
              </section>
              {b.share && (
                <div className="pod-bring">
                  <span className="label">She wants to bring up</span>
                  <p>{b.share}</p>
                </div>
              )}
              <section className="pod-section" data-section="hers">
                <div className="section-head">
                  <h3 className="label">Her thoughts</h3>
                  <span className="sub">her own view, kept as “view”</span>
                </div>
                <p className="note">Her own views on it. They are hers, not the author's.</p>
                {kept('view').length === 0 ? (
                  <Empty>She did not form a view of her own on this one.</Empty>
                ) : (
                  <ol className="pod-kept hers">
                    {kept('view').map((m) => (
                      <li key={m.id}>
                        <span>{m.content}</span>
                        {forgetButton(m)}
                      </li>
                    ))}
                  </ol>
                )}
              </section>
              <section className="pod-section" data-section="book">
                <div className="section-head">
                  <h3 className="label">What the book says</h3>
                  <span className="sub">trusted, credited, kept as “read”</span>
                </div>
                <p className="note">These sit in her memory credited to {b.author || 'the author'}, apart from what you have told her.</p>
                {kept('read').length === 0 ? (
                  <Empty>She is not holding anything from this book.</Empty>
                ) : (
                  <ol className="pod-kept book-kept">
                    {kept('read').map((m) => {
                      const ch = /chapter (\d+)/.exec(m.source)
                      return (
                        <li key={m.id}>
                          <span className="pod-chip">{ch ? `ch ${ch[1]}` : 'book'}</span>
                          <span>{m.content}</span>
                          {forgetButton(m)}
                        </li>
                      )
                    })}
                  </ol>
                )}
              </section>
              <section className="pod-section" data-section="chapters">
                <div className="section-head">
                  <h3 className="label">Chapters</h3>
                  <span className="sub">her digest of each</span>
                </div>
                {b.chapters.length === 0 ? <Empty>No chapters yet.</Empty> : <Chapters book={b} />}
              </section>
              <section className="pod-section" data-section="text">
                <h3 className="label">Full text search</h3>
                <FullText id={id} />
              </section>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

type View = { kind: 'all' } | { kind: 'book'; id: number }

export function Books() {
  const { data, error, reload } = useLoad<BooksView>('/api/books')
  const [view, setView] = useState<View>({ kind: 'all' })
  const [busy, setBusy] = useState(false)
  const [over, setOver] = useState(false)
  const [problem, setProblem] = useState('')
  const [toast, setToast] = useState('')
  useSignals((s) => {
    if (s.kind === 'books') reload()
  })
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(''), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const act = (path: string, done: string) =>
    void api(path, { method: 'POST' })
      .then(() => {
        setProblem('')
        setToast(done)
        reload()
      })
      .catch((e: Error) => setProblem(e.message))

  async function add(file: File | undefined) {
    if (!file) return
    if (!/\.epub$/i.test(file.name)) {
      setProblem('Only .epub books can be added.')
      return
    }
    setBusy(true)
    setProblem('')
    try {
      const form = new FormData()
      form.append('file', file)
      const added = await upload<Book>('/api/books', form)
      reload()
      setToast(`Added ${added.title || 'the book'}.`)
    } catch (e) {
      setProblem((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const counts = (() => {
    const done = data?.done.length ?? 0
    const now = data?.current ? 1 : 0
    const waiting = (data?.books ?? []).filter((b) => b.status === 'waiting' || b.status === 'queued').length
    return [done ? `${done} read` : '', now ? '1 reading' : '', waiting ? `${waiting} waiting` : ''].filter(Boolean).join(' · ') || 'none yet'
  })()
  const selected = view.kind === 'book' ? view.id : 0

  return (
    <Screen eyebrow="What she reads while you are busy" title="Books" className="pod-screen">
      <div className="pod-layout">
        <nav className="pod-rail" aria-label="Books">
          <ul>
            <li>
              <button type="button" aria-pressed={selected === 0} onClick={() => setView({ kind: 'all' })}>
                <span className="pod-art all" aria-hidden="true">∗</span>
                <span className="pod-row-text">
                  <b>All books</b>
                  <span className="sub">{counts}</span>
                </span>
                <span />
              </button>
            </li>
            {(data?.books ?? []).map((b) => (
              <li key={b.id}>
                <button type="button" aria-pressed={selected === b.id} onClick={() => setView({ kind: 'book', id: b.id })}>
                  <Cover book={b} />
                  <span className="pod-row-text">
                    <b>{b.title}</b>
                    <span className="sub">{b.author}</span>
                  </span>
                  <span className={`pod-status ${reading(b) ? 'live' : b.status === 'done' ? 'done' : b.status === 'failed' ? 'failed' : ''}`}>{pill[b.status]}</span>
                </button>
              </li>
            ))}
          </ul>
          <div className="pod-add">
            <span className="label">Add a book</span>
            <label
              className={over ? 'book-drop over' : 'book-drop'}
              onDragOver={(e) => {
                e.preventDefault()
                setOver(true)
              }}
              onDragLeave={() => setOver(false)}
              onDrop={(e) => {
                e.preventDefault()
                setOver(false)
                void add(e.dataTransfer.files[0])
              }}
            >
              <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
                <path d="M12 16V4M7 9l5-5 5 5M4 16v4h16v-4" />
              </svg>
              <span>{busy ? 'Opening…' : 'Drop an EPUB here, or choose one'}</span>
              <span className="sub">DRM-free .epub only · stays on this Mac</span>
              <input
                type="file"
                accept=".epub"
                disabled={busy}
                onChange={(e) => {
                  void add(e.target.files?.[0])
                  e.target.value = ''
                }}
              />
            </label>
            <p className="sub">She reads it straight through while she sleeps, or right away if you press Read now. Books you add are ones you have read and trust, so she keeps what they say as knowledge, credited to the author.</p>
          </div>
        </nav>

        <div className={view.kind === 'book' ? 'pod-main episode' : 'pod-main'}>
          <Problem message={error || problem} />
          {data && !data.enabled && <Problem message="Reading on her own is switched off in Settings." />}
          {data && view.kind === 'all' && <AllBooks view={data} act={act} reload={reload} onProblem={setProblem} open={(id) => setView({ kind: 'book', id })} />}
          {view.kind === 'book' && (
            <BookPage
              key={view.id}
              id={view.id}
              back={() => setView({ kind: 'all' })}
              onRemoved={(title) => {
                setToast(`Removed ${title}.`)
                setView({ kind: 'all' })
                reload()
              }}
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
