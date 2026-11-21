// Package store is the single SQLite database behind Cosmos: the memory
// stream, long-term memories, people, values, intentions, proposals and
// the small logs the brain keeps about itself.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS events(
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL, kind TEXT NOT NULL,
  actor TEXT NOT NULL DEFAULT '', text TEXT NOT NULL,
  importance INTEGER NOT NULL DEFAULT 1, meta TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS events_at ON events(at);
CREATE TABLE IF NOT EXISTS memories(
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, category TEXT NOT NULL,
  content TEXT NOT NULL, importance INTEGER NOT NULL DEFAULT 2,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS people(
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, aliases TEXT NOT NULL DEFAULT '[]',
  relationship TEXT NOT NULL DEFAULT '', closeness REAL NOT NULL DEFAULT 0.3,
  notes TEXT NOT NULL DEFAULT '', last_mentioned INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS vals(
  id INTEGER PRIMARY KEY, text TEXT NOT NULL, origin TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS intentions(
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, text TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '', facts TEXT NOT NULL DEFAULT '',
  trigger_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
  importance INTEGER NOT NULL DEFAULT 2, status TEXT NOT NULL DEFAULT 'open',
  created_at INTEGER NOT NULL, sent_at INTEGER NOT NULL DEFAULT 0,
  answered INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS proposals(
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
  before_text TEXT NOT NULL DEFAULT '', after_text TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL, decided_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS tidy(
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL, summary TEXT NOT NULL,
  undo TEXT NOT NULL, undone INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS runs(
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL, request TEXT NOT NULL,
  intent TEXT NOT NULL, mode TEXT NOT NULL, steps TEXT NOT NULL,
  rewrites INTEGER NOT NULL, verified INTEGER NOT NULL, duration_ms INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS readings(
  id INTEGER PRIMARY KEY, place TEXT NOT NULL, at INTEGER NOT NULL,
  temp_c REAL NOT NULL, wind_kph REAL NOT NULL, rain_mm REAL NOT NULL, code INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS calendar(
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, at INTEGER NOT NULL,
  duration_min INTEGER NOT NULL DEFAULT 0, location TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '', notified INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions(
  id INTEGER PRIMARY KEY, started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '',
  slept_at INTEGER NOT NULL DEFAULT 0, started_by TEXT NOT NULL DEFAULT 'user');
CREATE TABLE IF NOT EXISTS prompts(role TEXT PRIMARY KEY, text TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS embeddings(memory_id INTEGER PRIMARY KEY, model TEXT NOT NULL, vec BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS feeds(
  id INTEGER PRIMARY KEY, url TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', image_url TEXT NOT NULL DEFAULT '', art_path TEXT NOT NULL DEFAULT '',
  added_at INTEGER NOT NULL, checked_at INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS episodes(
  id INTEGER PRIMARY KEY, feed_id INTEGER NOT NULL, guid TEXT NOT NULL, title TEXT NOT NULL,
  published_at INTEGER NOT NULL DEFAULT 0, audio_url TEXT NOT NULL DEFAULT '', duration_sec INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'waiting', stage_note TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '', takeaways TEXT NOT NULL DEFAULT '[]', share TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '[]', transcript_path TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(feed_id, guid));
CREATE TABLE IF NOT EXISTS list_items(
  id INTEGER PRIMARY KEY, list TEXT NOT NULL, text TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, done_at INTEGER NOT NULL DEFAULT 0);
`

// Open opens (and creates) the database. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	if path == ":memory:" {
		dsn = "file::memory:"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps writes ordered and makes :memory: behave.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Columns added after the first release. Each memory keeps where it
	// came from, a replaced memory points at what replaced it, and each
	// event belongs to the session it happened in.
	for _, add := range [][2]string{
		{"memories", "source_event INTEGER NOT NULL DEFAULT 0"},
		{"memories", "source TEXT NOT NULL DEFAULT ''"},
		{"memories", "superseded_by INTEGER NOT NULL DEFAULT 0"},
		{"events", "session_id INTEGER NOT NULL DEFAULT 0"},
		{"calendar", "repeat TEXT NOT NULL DEFAULT ''"},
		{"episodes", "thoughts TEXT NOT NULL DEFAULT '[]'"},
		{"people", "locked TEXT NOT NULL DEFAULT '[]'"},
		{"people", "forgotten INTEGER NOT NULL DEFAULT 0"},
	} {
		name, _, _ := strings.Cut(add[1], " ")
		var n int
		db.QueryRow(`SELECT count(*) FROM pragma_table_info('`+add[0]+`') WHERE name=?`, name).Scan(&n)
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + add[0] + ` ADD COLUMN ` + add[1]); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// ---------- events: the memory stream ----------

type Event struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	Kind       string          `json:"kind"` // user_msg, nova_msg, initiative, appraisal, tool, journal, sleep, system
	Actor      string          `json:"actor"`
	Text       string          `json:"text"`
	Importance int             `json:"importance"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	SessionID  int64           `json:"session_id,omitempty"`
}

func (s *Store) AddEvent(e Event) (Event, error) {
	if e.Importance == 0 {
		e.Importance = 1
	}
	if len(e.Meta) == 0 {
		e.Meta = json.RawMessage("{}")
	}
	res, err := s.db.Exec(`INSERT INTO events(at,kind,actor,text,importance,meta,session_id) VALUES(?,?,?,?,?,?,?)`,
		ms(e.At), e.Kind, e.Actor, e.Text, e.Importance, string(e.Meta), e.SessionID)
	if err != nil {
		return e, err
	}
	e.ID, _ = res.LastInsertId()
	return e, nil
}

// GetEvent returns one event.
func (s *Store) GetEvent(id int64) (Event, bool) {
	rows, err := s.db.Query(`SELECT id,at,kind,actor,text,importance,meta,session_id FROM events WHERE id=?`, id)
	if err != nil {
		return Event{}, false
	}
	evs, err := scanEvents(rows)
	if err != nil || len(evs) == 0 {
		return Event{}, false
	}
	return evs[0], true
}

// SetEventMeta replaces the details kept with an event.
func (s *Store) SetEventMeta(id int64, meta []byte) error {
	_, err := s.db.Exec(`UPDATE events SET meta=? WHERE id=?`, string(meta), id)
	return err
}

func (s *Store) SetEventImportance(id int64, importance int) error {
	_, err := s.db.Exec(`UPDATE events SET importance=? WHERE id=?`, importance, id)
	return err
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at int64
		var meta string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Actor, &e.Text, &e.Importance, &meta, &e.SessionID); err != nil {
			return nil, err
		}
		e.At = fromMs(at)
		e.Meta = json.RawMessage(meta)
		out = append(out, e)
	}
	return out, rows.Err()
}

func kindFilter(kinds []string) (string, []any) {
	if len(kinds) == 0 {
		return "", nil
	}
	args := make([]any, len(kinds))
	for i, k := range kinds {
		args[i] = k
	}
	return " AND kind IN (?" + strings.Repeat(",?", len(kinds)-1) + ")", args
}

// EventsBetween returns events with from <= at < to, oldest first.
func (s *Store) EventsBetween(from, to time.Time, kinds ...string) ([]Event, error) {
	clause, extra := kindFilter(kinds)
	args := append([]any{ms(from), ms(to)}, extra...)
	rows, err := s.db.Query(`SELECT id,at,kind,actor,text,importance,meta,session_id FROM events WHERE at>=? AND at<?`+clause+` ORDER BY at,id`, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// RecentEvents returns the last n events of the given kinds, oldest first.
func (s *Store) RecentEvents(n int, kinds ...string) ([]Event, error) {
	clause, extra := kindFilter(kinds)
	args := append(extra, n)
	rows, err := s.db.Query(`SELECT id,at,kind,actor,text,importance,meta,session_id FROM events WHERE 1=1`+clause+` ORDER BY at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	return evs, err
}

// EventsMentioning returns the events of the given kinds whose text
// contains any of the words, newest first. It is a rough filter: callers
// check for whole words themselves.
func (s *Store) EventsMentioning(words []string, kinds ...string) ([]Event, error) {
	if len(words) == 0 {
		return []Event{}, nil
	}
	likes := make([]string, len(words))
	args := []any{}
	for i, w := range words {
		likes[i] = `lower(text) LIKE ?`
		args = append(args, "%"+strings.ToLower(w)+"%")
	}
	clause, extra := kindFilter(kinds)
	args = append(args, extra...)
	rows, err := s.db.Query(`SELECT id,at,kind,actor,text,importance,meta,session_id FROM events WHERE (`+strings.Join(likes, " OR ")+`)`+clause+` ORDER BY at DESC,id DESC`, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// SessionEvents returns the last n events of a session in the given kinds,
// oldest first. n <= 0 means all of them.
func (s *Store) SessionEvents(sessionID int64, n int, kinds ...string) ([]Event, error) {
	clause, extra := kindFilter(kinds)
	args := append([]any{sessionID}, extra...)
	q := `SELECT id,at,kind,actor,text,importance,meta,session_id FROM events WHERE session_id=?` + clause + ` ORDER BY at DESC,id DESC`
	if n > 0 {
		q += ` LIMIT ?`
		args = append(args, n)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	return evs, err
}

func (s *Store) SetEventSession(eventID, sessionID int64) error {
	_, err := s.db.Exec(`UPDATE events SET session_id=? WHERE id=?`, sessionID, eventID)
	return err
}

// ---------- sessions: sittings of one conversation ----------

// A Session is one sitting. It stays open, and listed, until she sleeps
// on it; after that it is part of her past.
type Session struct {
	ID        int64     `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"` // last activity
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	SleptAt   time.Time `json:"slept_at"`
	StartedBy string    `json:"started_by"` // user, nova
	Messages  int       `json:"messages"`
}

const sessionCols = `id,started_at,ended_at,title,summary,slept_at,started_by,
  (SELECT count(*) FROM events e WHERE e.session_id=sessions.id AND e.kind IN ('user_msg','nova_msg','initiative'))`

func (s *Store) querySessions(where string, args ...any) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		var st, en, sl int64
		if err := rows.Scan(&x.ID, &st, &en, &x.Title, &x.Summary, &sl, &x.StartedBy, &x.Messages); err != nil {
			return nil, err
		}
		x.StartedAt, x.EndedAt, x.SleptAt = fromMs(st), fromMs(en), fromMs(sl)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) AddSession(x Session) (Session, error) {
	if x.StartedBy == "" {
		x.StartedBy = "user"
	}
	if x.EndedAt.IsZero() {
		x.EndedAt = x.StartedAt
	}
	res, err := s.db.Exec(`INSERT INTO sessions(started_at,ended_at,title,summary,slept_at,started_by) VALUES(?,?,?,?,?,?)`,
		ms(x.StartedAt), ms(x.EndedAt), x.Title, x.Summary, ms(x.SleptAt), x.StartedBy)
	if err != nil {
		return x, err
	}
	x.ID, _ = res.LastInsertId()
	return x, nil
}

func (s *Store) GetSession(id int64) (Session, bool) {
	found, _ := s.querySessions(`WHERE id=?`, id)
	if len(found) == 0 {
		return Session{}, false
	}
	return found[0], true
}

func (s *Store) SaveSession(x Session) error {
	_, err := s.db.Exec(`UPDATE sessions SET ended_at=?, title=?, summary=?, slept_at=? WHERE id=?`,
		ms(x.EndedAt), x.Title, x.Summary, ms(x.SleptAt), x.ID)
	return err
}

// OpenSessions are the ones she has not slept on yet, newest first.
func (s *Store) OpenSessions() ([]Session, error) {
	return s.querySessions(`WHERE slept_at=0 ORDER BY started_at DESC, id DESC`)
}

// SessionsSleptAt are the sessions one particular sleep closed, newest first.
func (s *Store) SessionsSleptAt(at time.Time) ([]Session, error) {
	// The sleep time is kept to the second elsewhere, so match the second.
	from := ms(at.Truncate(time.Second))
	return s.querySessions(`WHERE slept_at>=? AND slept_at<? ORDER BY started_at DESC, id DESC`, from, from+1000)
}

// SleepNights lists when she slept, newest first: the times at which
// sessions were closed by a sleep.
func (s *Store) SleepNights(limit int) ([]time.Time, error) {
	rows, err := s.db.Query(`SELECT DISTINCT slept_at/1000 FROM sessions WHERE slept_at>0 ORDER BY 1 DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var sec int64
		if err := rows.Scan(&sec); err != nil {
			return nil, err
		}
		out = append(out, time.Unix(sec, 0))
	}
	return out, rows.Err()
}

// MemoriesFromSession are the current memories whose source is an event
// of this session.
func (s *Store) MemoriesFromSession(id int64) ([]Memory, error) {
	return s.queryMemories(`WHERE archived=0 AND source_event IN (SELECT id FROM events WHERE session_id=?) ORDER BY id`, id)
}

// ---------- memories ----------

type Memory struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"` // fact, reflection
	Category   string    `json:"category"`
	Content    string    `json:"content"`
	Importance int       `json:"importance"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Archived   bool      `json:"archived"`
	// Source is what was said (or, for a reflection, the moments) that
	// this memory rests on. SourceEvent is that event's id.
	SourceEvent  int64  `json:"source_event"`
	Source       string `json:"source"`
	SupersededBy int64  `json:"superseded_by,omitempty"`
}

func (s *Store) AddMemory(m Memory) (Memory, error) {
	if m.Kind == "" {
		m.Kind = "fact"
	}
	if m.Category == "" {
		m.Category = "general"
	}
	if m.Importance == 0 {
		m.Importance = 2
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	res, err := s.db.Exec(`INSERT INTO memories(kind,category,content,importance,created_at,updated_at,source_event,source) VALUES(?,?,?,?,?,?,?,?)`,
		m.Kind, m.Category, m.Content, m.Importance, ms(m.CreatedAt), ms(m.UpdatedAt), m.SourceEvent, m.Source)
	if err != nil {
		return m, err
	}
	m.ID, _ = res.LastInsertId()
	return m, nil
}

func (s *Store) UpdateMemory(id int64, category, content string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE memories SET category=?, content=?, updated_at=? WHERE id=?`, category, content, ms(at), id)
	return err
}

// Supersede replaces a memory with a newer one without losing the old: the
// old row is set aside and points at its replacement, so what she used to
// believe, and when it changed, stays on record.
func (s *Store) Supersede(oldID int64, next Memory) (Memory, error) {
	next, err := s.AddMemory(next)
	if err != nil {
		return next, err
	}
	_, err = s.db.Exec(`UPDATE memories SET archived=1, superseded_by=? WHERE id=?`, next.ID, oldID)
	return next, err
}

// Replaced returns, for each memory that replaced another, the wording it
// replaced (the most recent one when there were several).
func (s *Store) Replaced() (map[int64]string, error) {
	rows, err := s.db.Query(`SELECT superseded_by, content FROM memories WHERE superseded_by<>0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			return nil, err
		}
		out[id] = content
	}
	return out, rows.Err()
}

// Past is one wording a memory used to have, and when it was learned.
type Past struct {
	Content string    `json:"content"`
	Since   time.Time `json:"since"`
}

// Histories returns, for each memory that replaced another, every wording
// before it, newest first.
func (s *Store) Histories() (map[int64][]Past, error) {
	old, err := s.queryMemories(`WHERE superseded_by<>0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	before := map[int64]Memory{}
	for _, m := range old {
		before[m.SupersededBy] = m
	}
	out := map[int64][]Past{}
	for id := range before {
		// The bound guards against a loop in a hand-edited database.
		for cur := id; len(out[id]) < 50; {
			m, ok := before[cur]
			if !ok {
				break
			}
			out[id] = append(out[id], Past{Content: m.Content, Since: m.CreatedAt})
			cur = m.ID
		}
	}
	return out, nil
}

// ForgottenMemories are the memories the user put away, newest first.
// Memories set aside because a newer one replaced them are history, not
// forgotten, and are left out.
func (s *Store) ForgottenMemories() ([]Memory, error) {
	return s.queryMemories(`WHERE archived=1 AND superseded_by=0 ORDER BY id DESC`)
}

// Pinned is the importance of a memory the user asked her to keep in
// mind. Pinned memories are always offered to the model.
const Pinned = 5

func (s *Store) SetMemoryImportance(id int64, importance int) error {
	_, err := s.db.Exec(`UPDATE memories SET importance=? WHERE id=?`, importance, id)
	return err
}

func (s *Store) SetMemoryArchived(id int64, archived bool) error {
	_, err := s.db.Exec(`UPDATE memories SET archived=? WHERE id=?`, archived, id)
	return err
}

func (s *Store) TouchMemories(ids []int64, at time.Time) {
	for _, id := range ids {
		s.db.Exec(`UPDATE memories SET last_used_at=? WHERE id=?`, ms(at), id)
	}
}

func (s *Store) GetMemory(id int64) (Memory, error) {
	found, err := s.queryMemories(`WHERE id=?`, id)
	if err != nil {
		return Memory{}, err
	}
	if len(found) == 0 {
		return Memory{}, sql.ErrNoRows
	}
	return found[0], nil
}

// Memories returns the active (not archived) memories, oldest first.
func (s *Store) Memories() ([]Memory, error) {
	return s.queryMemories(`WHERE archived=0 ORDER BY created_at,id`)
}

func (s *Store) queryMemories(where string, args ...any) ([]Memory, error) {
	rows, err := s.db.Query(`SELECT id,kind,category,content,importance,created_at,updated_at,last_used_at,archived,source_event,source,superseded_by FROM memories `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		var c, u, l int64
		if err := rows.Scan(&m.ID, &m.Kind, &m.Category, &m.Content, &m.Importance, &c, &u, &l, &m.Archived, &m.SourceEvent, &m.Source, &m.SupersededBy); err != nil {
			return nil, err
		}
		m.CreatedAt, m.UpdatedAt, m.LastUsedAt = fromMs(c), fromMs(u), fromMs(l)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- people ----------

type Person struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Aliases       []string  `json:"aliases"`
	Relationship  string    `json:"relationship"`
	Closeness     float64   `json:"closeness"` // 0 stranger .. 1 closest
	Notes         string    `json:"notes"`
	LastMentioned time.Time `json:"last_mentioned"`
	// Locked names the fields the user set by hand ("relationship",
	// "closeness", ...). What she learns from conversation leaves them be.
	Locked []string `json:"locked"`
}

// Has reports whether the user set this field by hand.
func (p Person) Has(field string) bool {
	for _, f := range p.Locked {
		if f == field {
			return true
		}
	}
	return false
}

// People returns everyone she knows, closest first. People the user
// asked her to forget are left out.
func (s *Store) People() ([]Person, error) {
	return s.queryPeople(`WHERE forgotten=0 ORDER BY closeness DESC, name`)
}

// Forgotten reports whether the user asked her to forget someone by this
// name, so conversation does not bring them back.
func (s *Store) Forgotten(name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	gone, _ := s.queryPeople(`WHERE forgotten=1`)
	for _, p := range gone {
		for _, n := range append([]string{p.Name}, p.Aliases...) {
			if strings.ToLower(n) == want {
				return true
			}
		}
	}
	return false
}

func (s *Store) ForgetPerson(id int64) error {
	_, err := s.db.Exec(`UPDATE people SET forgotten=1 WHERE id=?`, id)
	return err
}

// DeletePerson removes someone outright, as when they are merged into
// another person.
func (s *Store) DeletePerson(id int64) error {
	_, err := s.db.Exec(`DELETE FROM people WHERE id=?`, id)
	return err
}

func (s *Store) queryPeople(where string, args ...any) ([]Person, error) {
	rows, err := s.db.Query(`SELECT id,name,aliases,relationship,closeness,notes,last_mentioned,locked FROM people `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		var aliases, locked string
		var lm int64
		if err := rows.Scan(&p.ID, &p.Name, &aliases, &p.Relationship, &p.Closeness, &p.Notes, &lm, &locked); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(aliases), &p.Aliases)
		if p.Aliases == nil {
			p.Aliases = []string{}
		}
		json.Unmarshal([]byte(locked), &p.Locked)
		if p.Locked == nil {
			p.Locked = []string{}
		}
		p.LastMentioned = fromMs(lm)
		out = append(out, p)
	}
	return out, rows.Err()
}

// FindPerson matches a name or alias, ignoring case.
func (s *Store) FindPerson(name string) (Person, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return Person{}, false
	}
	people, _ := s.People()
	for _, p := range people {
		if strings.ToLower(p.Name) == want {
			return p, true
		}
		for _, a := range p.Aliases {
			if strings.ToLower(a) == want {
				return p, true
			}
		}
	}
	return Person{}, false
}

// SavePerson inserts when ID is zero, otherwise updates.
func (s *Store) SavePerson(p Person, now time.Time) (Person, error) {
	if p.Aliases == nil {
		p.Aliases = []string{}
	}
	if p.Locked == nil {
		p.Locked = []string{}
	}
	aliases, _ := json.Marshal(p.Aliases)
	locked, _ := json.Marshal(p.Locked)
	if p.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO people(name,aliases,relationship,closeness,notes,last_mentioned,created_at,locked) VALUES(?,?,?,?,?,?,?,?)`,
			p.Name, string(aliases), p.Relationship, p.Closeness, p.Notes, ms(p.LastMentioned), ms(now), string(locked))
		if err != nil {
			return p, err
		}
		p.ID, _ = res.LastInsertId()
		return p, nil
	}
	_, err := s.db.Exec(`UPDATE people SET name=?,aliases=?,relationship=?,closeness=?,notes=?,last_mentioned=?,locked=? WHERE id=?`,
		p.Name, string(aliases), p.Relationship, p.Closeness, p.Notes, ms(p.LastMentioned), string(locked), p.ID)
	return p, err
}

// ---------- values ----------

type Value struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Origin    string    `json:"origin"` // seed (given by the user) or hers
	Active    bool      `json:"active"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) Values() ([]Value, error) {
	rows, err := s.db.Query(`SELECT id,text,origin,active,updated_at FROM vals WHERE active=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Value{}
	for rows.Next() {
		var v Value
		var u int64
		if err := rows.Scan(&v.ID, &v.Text, &v.Origin, &v.Active, &u); err != nil {
			return nil, err
		}
		v.UpdatedAt = fromMs(u)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) AddValue(text, origin string, now time.Time) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO vals(text,origin,created_at,updated_at) VALUES(?,?,?,?)`, text, origin, ms(now), ms(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateValue(id int64, text, origin string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE vals SET text=?, origin=?, updated_at=? WHERE id=?`, text, origin, ms(now), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("value not found")
	}
	return nil
}

// ---------- key/value ----------

func (s *Store) Get(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&v)
	return v
}

func (s *Store) Set(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetJSON(key string, into any) bool {
	raw := s.Get(key)
	if raw == "" {
		return false
	}
	return json.Unmarshal([]byte(raw), into) == nil
}

func (s *Store) SetJSON(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Set(key, string(raw))
}

// ---------- intentions: things she wants to say ----------

type Intention struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"` // followup, reminder, brief, weather, reflection
	Text       string    `json:"text"`
	Reason     string    `json:"reason"`
	Facts      string    `json:"facts,omitempty"`
	TriggerAt  time.Time `json:"trigger_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Importance int       `json:"importance"`
	Status     string    `json:"status"` // open, sent, done, expired
	CreatedAt  time.Time `json:"created_at"`
	SentAt     time.Time `json:"sent_at"`
	Answered   bool      `json:"answered"`
}

func (s *Store) AddIntention(i Intention) (Intention, error) {
	if i.Importance == 0 {
		i.Importance = 2
	}
	i.Status = "open"
	res, err := s.db.Exec(`INSERT INTO intentions(kind,text,reason,facts,trigger_at,expires_at,importance,status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		i.Kind, i.Text, i.Reason, i.Facts, ms(i.TriggerAt), ms(i.ExpiresAt), i.Importance, i.Status, ms(i.CreatedAt))
	if err != nil {
		return i, err
	}
	i.ID, _ = res.LastInsertId()
	return i, nil
}

// Intentions lists intentions in the given statuses, soonest trigger first.
func (s *Store) Intentions(statuses ...string) ([]Intention, error) {
	q := `SELECT id,kind,text,reason,facts,trigger_at,expires_at,importance,status,created_at,sent_at,answered FROM intentions`
	args := []any{}
	if len(statuses) > 0 {
		q += ` WHERE status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	rows, err := s.db.Query(q+` ORDER BY trigger_at,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Intention{}
	for rows.Next() {
		var i Intention
		var t, e, c, sent int64
		if err := rows.Scan(&i.ID, &i.Kind, &i.Text, &i.Reason, &i.Facts, &t, &e, &i.Importance, &i.Status, &c, &sent, &i.Answered); err != nil {
			return nil, err
		}
		i.TriggerAt, i.ExpiresAt, i.CreatedAt, i.SentAt = fromMs(t), fromMs(e), fromMs(c), fromMs(sent)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) SetIntentionStatus(id int64, status string, at time.Time) error {
	if status == "sent" {
		_, err := s.db.Exec(`UPDATE intentions SET status=?, sent_at=? WHERE id=?`, status, ms(at), id)
		return err
	}
	_, err := s.db.Exec(`UPDATE intentions SET status=? WHERE id=?`, status, id)
	return err
}

func (s *Store) MarkIntentionAnswered(id int64) error {
	_, err := s.db.Exec(`UPDATE intentions SET answered=1 WHERE id=?`, id)
	return err
}

// ---------- proposals: changes she asks permission for ----------

type Proposal struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`   // value, writing, planning
	Target    string    `json:"target"` // value id, or the intent a planning change applies to
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"` // pending, approved, declined
	CreatedAt time.Time `json:"created_at"`
	DecidedAt time.Time `json:"decided_at"`
}

func (s *Store) AddProposal(p Proposal) (Proposal, error) {
	p.Status = "pending"
	res, err := s.db.Exec(`INSERT INTO proposals(kind,target,before_text,after_text,reason,status,created_at) VALUES(?,?,?,?,?,?,?)`,
		p.Kind, p.Target, p.Before, p.After, p.Reason, p.Status, ms(p.CreatedAt))
	if err != nil {
		return p, err
	}
	p.ID, _ = res.LastInsertId()
	return p, nil
}

// Proposals returns the newest first; status "" means all.
func (s *Store) Proposals(status string, limit int) ([]Proposal, error) {
	q := `SELECT id,kind,target,before_text,after_text,reason,status,created_at,decided_at FROM proposals`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := s.db.Query(q+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		var p Proposal
		var c, d int64
		if err := rows.Scan(&p.ID, &p.Kind, &p.Target, &p.Before, &p.After, &p.Reason, &p.Status, &c, &d); err != nil {
			return nil, err
		}
		p.CreatedAt, p.DecidedAt = fromMs(c), fromMs(d)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProposal(id int64) (Proposal, error) {
	var p Proposal
	var c, d int64
	err := s.db.QueryRow(`SELECT id,kind,target,before_text,after_text,reason,status,created_at,decided_at FROM proposals WHERE id=?`, id).
		Scan(&p.ID, &p.Kind, &p.Target, &p.Before, &p.After, &p.Reason, &p.Status, &c, &d)
	p.CreatedAt, p.DecidedAt = fromMs(c), fromMs(d)
	return p, err
}

func (s *Store) DecideProposal(id int64, status string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE proposals SET status=?, decided_at=? WHERE id=?`, status, ms(at), id)
	return err
}

// ---------- tidy log: what sleep changed on its own, with an undo ----------

type Tidy struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Summary string          `json:"summary"`
	Undo    json.RawMessage `json:"-"`
	Undone  bool            `json:"undone"`
}

func (s *Store) AddTidy(at time.Time, summary string, undo any) error {
	raw, err := json.Marshal(undo)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO tidy(at,summary,undo) VALUES(?,?,?)`, ms(at), summary, string(raw))
	return err
}

func (s *Store) TidySince(since time.Time) ([]Tidy, error) {
	rows, err := s.db.Query(`SELECT id,at,summary,undo,undone FROM tidy WHERE at>=? ORDER BY id`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tidy{}
	for rows.Next() {
		var t Tidy
		var at int64
		var undo string
		if err := rows.Scan(&t.ID, &at, &t.Summary, &undo, &t.Undone); err != nil {
			return nil, err
		}
		t.At, t.Undo = fromMs(at), json.RawMessage(undo)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTidy(id int64) (Tidy, error) {
	var t Tidy
	var at int64
	var undo string
	err := s.db.QueryRow(`SELECT id,at,summary,undo,undone FROM tidy WHERE id=?`, id).Scan(&t.ID, &at, &t.Summary, &undo, &t.Undone)
	t.At, t.Undo = fromMs(at), json.RawMessage(undo)
	return t, err
}

func (s *Store) MarkTidyUndone(id int64) error {
	_, err := s.db.Exec(`UPDATE tidy SET undone=1 WHERE id=?`, id)
	return err
}

// ---------- runs: how each reply was produced ----------

type Step struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"` // main, worker, tool, code
	OK     bool   `json:"ok"`
	Millis int64  `json:"ms"`
	Note   string `json:"note,omitempty"`
}

type Run struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Request  string    `json:"request"`
	Intent   string    `json:"intent"`
	Mode     string    `json:"mode"`
	Steps    []Step    `json:"steps"`
	Rewrites int       `json:"rewrites"`
	Verified bool      `json:"verified"`
	Millis   int64     `json:"ms"`
}

func (s *Store) AddRun(r Run) (Run, error) {
	steps, _ := json.Marshal(r.Steps)
	res, err := s.db.Exec(`INSERT INTO runs(at,request,intent,mode,steps,rewrites,verified,duration_ms) VALUES(?,?,?,?,?,?,?,?)`,
		ms(r.At), r.Request, r.Intent, r.Mode, string(steps), r.Rewrites, r.Verified, r.Millis)
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// SetRunSteps replaces a run's steps: the feeling step finishes after the
// reply has been sent and recorded.
func (s *Store) SetRunSteps(id int64, steps []Step) error {
	raw, _ := json.Marshal(steps)
	_, err := s.db.Exec(`UPDATE runs SET steps=? WHERE id=?`, string(raw), id)
	return err
}

// RunsSince returns runs at or after since, oldest first.
func (s *Store) RunsSince(since time.Time) ([]Run, error) {
	return s.queryRuns(`WHERE at>=? ORDER BY id`, ms(since))
}

func (s *Store) GetRun(id int64) (Run, bool) {
	runs, _ := s.queryRuns(`WHERE id=?`, id)
	if len(runs) == 0 {
		return Run{}, false
	}
	return runs[0], true
}

func (s *Store) LastRun() (Run, bool) {
	runs, _ := s.queryRuns(`ORDER BY id DESC LIMIT 1`)
	if len(runs) == 0 {
		return Run{}, false
	}
	return runs[0], true
}

func (s *Store) queryRuns(where string, args ...any) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id,at,request,intent,mode,steps,rewrites,verified,duration_ms FROM runs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var at int64
		var steps string
		if err := rows.Scan(&r.ID, &at, &r.Request, &r.Intent, &r.Mode, &steps, &r.Rewrites, &r.Verified, &r.Millis); err != nil {
			return nil, err
		}
		r.At = fromMs(at)
		json.Unmarshal([]byte(steps), &r.Steps)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- weather readings: her own record of what is normal ----------

type Reading struct {
	Place   string    `json:"place"`
	At      time.Time `json:"at"`
	TempC   float64   `json:"temp_c"`
	WindKph float64   `json:"wind_kph"`
	RainMm  float64   `json:"rain_mm"`
	Code    int       `json:"code"`
}

func (s *Store) AddReading(r Reading) error {
	_, err := s.db.Exec(`INSERT INTO readings(place,at,temp_c,wind_kph,rain_mm,code) VALUES(?,?,?,?,?,?)`,
		r.Place, ms(r.At), r.TempC, r.WindKph, r.RainMm, r.Code)
	return err
}

func (s *Store) ReadingsBetween(place string, from, to time.Time) ([]Reading, error) {
	rows, err := s.db.Query(`SELECT place,at,temp_c,wind_kph,rain_mm,code FROM readings WHERE place=? AND at>=? AND at<? ORDER BY at`, place, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reading{}
	for rows.Next() {
		var r Reading
		var at int64
		if err := rows.Scan(&r.Place, &at, &r.TempC, &r.WindKph, &r.RainMm, &r.Code); err != nil {
			return nil, err
		}
		r.At = fromMs(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- calendar ----------

type CalendarItem struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"` // event, reminder
	Title       string    `json:"title"`
	At          time.Time `json:"at"`
	DurationMin int       `json:"duration_min"`
	Location    string    `json:"location"`
	Notes       string    `json:"notes"`
	Notified    bool      `json:"notified"`
	// Repeat is "", daily, weekly, monthly or yearly. At is then the first
	// occurrence, and for a birthday the year it began.
	Repeat string `json:"repeat"`
}

func (s *Store) AddCalendarItem(c CalendarItem) (CalendarItem, error) {
	if c.Kind == "" {
		c.Kind = "event"
	}
	res, err := s.db.Exec(`INSERT INTO calendar(kind,title,at,duration_min,location,notes,repeat) VALUES(?,?,?,?,?,?,?)`,
		c.Kind, c.Title, ms(c.At), c.DurationMin, c.Location, c.Notes, c.Repeat)
	if err != nil {
		return c, err
	}
	c.ID, _ = res.LastInsertId()
	return c, nil
}

// NextOccurrence returns the first occurrence of a repeating item at or
// after notBefore. For an item that does not repeat it returns its time.
func NextOccurrence(first time.Time, repeat string, notBefore time.Time) time.Time {
	if repeat == "" || !first.Before(notBefore) {
		return first
	}
	step := func(t time.Time) time.Time {
		switch repeat {
		case "daily":
			return t.AddDate(0, 0, 1)
		case "weekly":
			return t.AddDate(0, 0, 7)
		case "monthly":
			return t.AddDate(0, 1, 0)
		}
		return t.AddDate(1, 0, 0)
	}
	t := first
	// Jump close before walking, so a birthday from decades ago or a
	// daily item from years ago does not take thousands of steps.
	switch repeat {
	case "yearly":
		if y := notBefore.Year() - first.Year() - 1; y > 0 {
			t = first.AddDate(y, 0, 0)
		}
	case "monthly":
		if m := (notBefore.Year()-first.Year())*12 + int(notBefore.Month()-first.Month()) - 1; m > 0 {
			t = first.AddDate(0, m, 0)
		}
	case "daily", "weekly":
		days := 1
		if repeat == "weekly" {
			days = 7
		}
		if n := int(notBefore.Sub(first).Hours()/24)/days - 1; n > 0 {
			t = first.AddDate(0, 0, n*days)
		}
	}
	for t.Before(notBefore) {
		t = step(t)
	}
	return t
}

// CalendarBetween returns what falls in [from, to), oldest first. A
// repeating item appears once for each occurrence in the range, with At
// set to that occurrence.
func (s *Store) CalendarBetween(from, to time.Time) ([]CalendarItem, error) {
	out, err := s.queryCalendar(`WHERE repeat='' AND at>=? AND at<? ORDER BY at`, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	repeating, err := s.queryCalendar(`WHERE repeat<>'' AND at<?`, ms(to))
	if err != nil {
		return nil, err
	}
	for _, c := range repeating {
		for t, n := NextOccurrence(c.At, c.Repeat, from), 0; t.Before(to) && n < 400; n++ {
			occ := c
			occ.At = t
			out = append(out, occ)
			t = NextOccurrence(c.At, c.Repeat, t.Add(time.Second))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// AllCalendar returns every calendar item as stored, one row each.
func (s *Store) AllCalendar() ([]CalendarItem, error) {
	return s.queryCalendar(`ORDER BY at`)
}

// MoveCalendarItem changes when an item is. A repeating reminder is moved
// on to its next occurrence each time it has been given.
func (s *Store) MoveCalendarItem(id int64, at time.Time) error {
	_, err := s.db.Exec(`UPDATE calendar SET at=? WHERE id=?`, ms(at), id)
	return err
}

// DueReminders returns reminders whose time has come and that were not sent.
func (s *Store) DueReminders(now time.Time) ([]CalendarItem, error) {
	return s.queryCalendar(`WHERE kind='reminder' AND notified=0 AND at<=? ORDER BY at`, ms(now))
}

func (s *Store) MarkCalendarNotified(id int64) error {
	_, err := s.db.Exec(`UPDATE calendar SET notified=1 WHERE id=?`, id)
	return err
}

func (s *Store) DeleteCalendarItem(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM calendar WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) queryCalendar(where string, args ...any) ([]CalendarItem, error) {
	rows, err := s.db.Query(`SELECT id,kind,title,at,duration_min,location,notes,notified,repeat FROM calendar `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CalendarItem{}
	for rows.Next() {
		var c CalendarItem
		var at int64
		if err := rows.Scan(&c.ID, &c.Kind, &c.Title, &at, &c.DurationMin, &c.Location, &c.Notes, &c.Notified, &c.Repeat); err != nil {
			return nil, err
		}
		c.At = fromMs(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- prompts she is allowed to change ----------

func (s *Store) Prompt(role string) string {
	var v string
	s.db.QueryRow(`SELECT text FROM prompts WHERE role=?`, role).Scan(&v)
	return v
}

func (s *Store) SetPrompt(role, text string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO prompts(role,text,updated_at) VALUES(?,?,?) ON CONFLICT(role) DO UPDATE SET text=excluded.text, updated_at=excluded.updated_at`, role, text, ms(now))
	return err
}
