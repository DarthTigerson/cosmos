package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

const booksSchema = `
CREATE TABLE IF NOT EXISTS books(
  id INTEGER PRIMARY KEY, title TEXT NOT NULL, author TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'nonfiction', dir TEXT NOT NULL DEFAULT '', cover_path TEXT NOT NULL DEFAULT '',
  added_at INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'queued', stage_note TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '', words INTEGER NOT NULL DEFAULT 0, parts INTEGER NOT NULL DEFAULT 0,
  part INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '', thoughts TEXT NOT NULL DEFAULT '[]',
  share TEXT NOT NULL DEFAULT '', chapters TEXT NOT NULL DEFAULT '[]', asleep INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0);
`

// BookChapter is one chapter of a book: what she noted reading it, and
// what she made of it afterwards.
type BookChapter struct {
	N       int      `json:"n"`
	Title   string   `json:"title"`
	Words   int      `json:"words"`
	Parts   int      `json:"parts"`
	Notes   []string `json:"notes"`
	Digest  string   `json:"digest"`
	Learned []string `json:"learned"`
	// Digested is set once she has thought the chapter over, even when
	// nothing came of it, so a chapter is never taken twice.
	Digested bool `json:"digested"`
}

// Book is one book the user gave her and how far she has got with it.
// Status: waiting (not asked for), queued, opening, reading, digesting,
// thinking, done, failed.
type Book struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Kind      string    `json:"kind"` // fiction or nonfiction
	Dir       string    `json:"-"`    // the epub and the text of each chapter
	CoverPath string    `json:"-"`
	HasCover  bool      `json:"has_cover"`
	AddedAt   time.Time `json:"added_at"`
	Status    string    `json:"status"`
	StageNote string    `json:"stage_note"`
	Error     string    `json:"error"`
	Words     int       `json:"words"`
	Parts     int       `json:"parts"`
	// Part is how many parts she has read; reading picks up from here.
	Part       int           `json:"part"`
	Summary    string        `json:"summary"`
	Thoughts   []string      `json:"thoughts"`
	Share      string        `json:"share"`
	Chapters   []BookChapter `json:"chapters"`
	Asleep     bool          `json:"asleep"` // some of it was read in her sleep
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}

func (s *Store) AddBook(b Book) (Book, error) {
	res, err := s.db.Exec(`INSERT INTO books(title,author,kind,added_at,status) VALUES(?,?,?,?,?)`, b.Title, b.Author, b.Kind, ms(b.AddedAt), b.Status)
	if err != nil {
		return Book{}, err
	}
	b.ID, _ = res.LastInsertId()
	if b.Thoughts == nil {
		b.Thoughts = []string{}
	}
	if b.Chapters == nil {
		b.Chapters = []BookChapter{}
	}
	return b, nil
}

const bookCols = `id,title,author,kind,dir,cover_path,added_at,status,stage_note,error,words,parts,part,summary,thoughts,share,chapters,asleep,started_at,finished_at`

func scanBook(row interface{ Scan(...any) error }) (Book, error) {
	var b Book
	var added, started, finished int64
	var thoughts, chapters string
	err := row.Scan(&b.ID, &b.Title, &b.Author, &b.Kind, &b.Dir, &b.CoverPath, &added, &b.Status, &b.StageNote, &b.Error,
		&b.Words, &b.Parts, &b.Part, &b.Summary, &thoughts, &b.Share, &chapters, &b.Asleep, &started, &finished)
	if err != nil {
		return b, err
	}
	b.AddedAt, b.StartedAt, b.FinishedAt = stamp(added), stamp(started), stamp(finished)
	b.Thoughts, b.Chapters = []string{}, []BookChapter{}
	json.Unmarshal([]byte(thoughts), &b.Thoughts)
	json.Unmarshal([]byte(chapters), &b.Chapters)
	for i := range b.Chapters {
		if b.Chapters[i].Notes == nil {
			b.Chapters[i].Notes = []string{}
		}
		if b.Chapters[i].Learned == nil {
			b.Chapters[i].Learned = []string{}
		}
	}
	b.HasCover = b.CoverPath != ""
	return b, nil
}

// Books lists books, the most recently added first. An empty status list
// means every status.
func (s *Store) Books(statuses ...string) ([]Book, error) {
	q := `SELECT ` + bookCols + ` FROM books`
	args := []any{}
	if len(statuses) > 0 {
		q += ` WHERE status IN (`
		for i, st := range statuses {
			if i > 0 {
				q += `,`
			}
			q += `?`
			args = append(args, st)
		}
		q += `)`
	}
	q += ` ORDER BY added_at DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) GetBook(id int64) (Book, bool) {
	b, err := scanBook(s.db.QueryRow(`SELECT `+bookCols+` FROM books WHERE id=?`, id))
	if err == sql.ErrNoRows || err != nil {
		return Book{}, false
	}
	return b, true
}

// SaveBook writes back everything about a book that changes as she reads.
func (s *Store) SaveBook(b Book) error {
	if b.Thoughts == nil {
		b.Thoughts = []string{}
	}
	if b.Chapters == nil {
		b.Chapters = []BookChapter{}
	}
	thoughts, _ := json.Marshal(b.Thoughts)
	chapters, _ := json.Marshal(b.Chapters)
	_, err := s.db.Exec(`UPDATE books SET title=?,author=?,kind=?,dir=?,cover_path=?,status=?,stage_note=?,error=?,words=?,parts=?,part=?,summary=?,thoughts=?,share=?,chapters=?,asleep=?,started_at=?,finished_at=? WHERE id=?`,
		b.Title, b.Author, b.Kind, b.Dir, b.CoverPath, b.Status, b.StageNote, b.Error, b.Words, b.Parts, b.Part, b.Summary,
		string(thoughts), b.Share, string(chapters), b.Asleep, ms(b.StartedAt), ms(b.FinishedAt), b.ID)
	return err
}

// DeleteBook removes a book. What she learned from it stays in her memory.
func (s *Store) DeleteBook(id int64) error {
	_, err := s.db.Exec(`DELETE FROM books WHERE id=?`, id)
	return err
}
