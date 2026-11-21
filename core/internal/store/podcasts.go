package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Feed is one podcast she follows.
type Feed struct {
	ID          int64     `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	ImageURL    string    `json:"image_url"`
	ArtPath     string    `json:"-"`
	HasArt      bool      `json:"has_art"`
	AddedAt     time.Time `json:"added_at"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error"`
}

// EpisodeNote is what she noted from one stretch of an episode.
type EpisodeNote struct {
	At   string `json:"at"` // roughly where in the episode, "1:04"
	Text string `json:"text"`
}

// Episode is one episode of a feed and how far she has got with it.
// Status: waiting (not asked for), queued, downloading, transcribing,
// reading, thinking, done, failed.
type Episode struct {
	ID          int64     `json:"id"`
	FeedID      int64     `json:"feed_id"`
	GUID        string    `json:"-"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"published_at"`
	AudioURL    string    `json:"-"`
	DurationSec int       `json:"duration_sec"`
	Status      string    `json:"status"`
	StageNote   string    `json:"stage_note"`
	Error       string    `json:"error"`
	Summary     string    `json:"summary"`
	Takeaways   []string  `json:"takeaways"`
	// Thoughts are her own views on what was said, kept apart from the
	// takeaways, which only report it.
	Thoughts       []string      `json:"thoughts"`
	Share          string        `json:"share"`
	Notes          []EpisodeNote `json:"notes"`
	TranscriptPath string        `json:"-"`
	HasTranscript  bool          `json:"has_transcript"`
	StartedAt      time.Time     `json:"started_at"`
	FinishedAt     time.Time     `json:"finished_at"`
}

func stamp(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func (s *Store) AddFeed(url string, at time.Time) (Feed, error) {
	res, err := s.db.Exec(`INSERT INTO feeds(url,added_at) VALUES(?,?)`, url, ms(at))
	if err != nil {
		return Feed{}, err
	}
	id, _ := res.LastInsertId()
	return Feed{ID: id, URL: url, AddedAt: at}, nil
}

func (s *Store) SaveFeed(f Feed) error {
	_, err := s.db.Exec(`UPDATE feeds SET title=?,description=?,image_url=?,art_path=?,checked_at=?,error=? WHERE id=?`,
		f.Title, f.Description, f.ImageURL, f.ArtPath, ms(f.CheckedAt), f.Error, f.ID)
	return err
}

func (s *Store) Feeds() ([]Feed, error) {
	rows, err := s.db.Query(`SELECT id,url,title,description,image_url,art_path,added_at,checked_at,error FROM feeds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Feed{}
	for rows.Next() {
		var f Feed
		var added, checked int64
		if err := rows.Scan(&f.ID, &f.URL, &f.Title, &f.Description, &f.ImageURL, &f.ArtPath, &added, &checked, &f.Error); err != nil {
			return nil, err
		}
		f.AddedAt, f.CheckedAt, f.HasArt = stamp(added), stamp(checked), f.ArtPath != ""
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFeed(id int64) (Feed, bool) {
	all, _ := s.Feeds()
	for _, f := range all {
		if f.ID == id {
			return f, true
		}
	}
	return Feed{}, false
}

// DeleteFeed removes a feed and its episodes. What she learned from them
// stays in her memory.
func (s *Store) DeleteFeed(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM episodes WHERE feed_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM feeds WHERE id=?`, id)
	return err
}

// AddEpisode records an episode the feed lists. It reports false when
// the episode was already known.
func (s *Store) AddEpisode(e Episode) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO episodes(feed_id,guid,title,published_at,audio_url,duration_sec,status) VALUES(?,?,?,?,?,?,?)`,
		e.FeedID, e.GUID, e.Title, ms(e.PublishedAt), e.AudioURL, e.DurationSec, e.Status)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

const episodeCols = `id,feed_id,guid,title,published_at,audio_url,duration_sec,status,stage_note,error,summary,takeaways,share,notes,transcript_path,started_at,finished_at,thoughts`

func scanEpisode(row interface{ Scan(...any) error }) (Episode, error) {
	var e Episode
	var pub, started, finished int64
	var takeaways, notes, thoughts string
	err := row.Scan(&e.ID, &e.FeedID, &e.GUID, &e.Title, &pub, &e.AudioURL, &e.DurationSec, &e.Status, &e.StageNote, &e.Error,
		&e.Summary, &takeaways, &e.Share, &notes, &e.TranscriptPath, &started, &finished, &thoughts)
	if err != nil {
		return e, err
	}
	e.PublishedAt, e.StartedAt, e.FinishedAt = stamp(pub), stamp(started), stamp(finished)
	e.Takeaways, e.Notes, e.Thoughts = []string{}, []EpisodeNote{}, []string{}
	json.Unmarshal([]byte(thoughts), &e.Thoughts)
	json.Unmarshal([]byte(takeaways), &e.Takeaways)
	json.Unmarshal([]byte(notes), &e.Notes)
	e.HasTranscript = e.TranscriptPath != ""
	return e, nil
}

// Episodes lists episodes newest first. feedID 0 means every feed; an
// empty status list means every status.
func (s *Store) Episodes(feedID int64, limit int, statuses ...string) ([]Episode, error) {
	q := `SELECT ` + episodeCols + ` FROM episodes WHERE (?=0 OR feed_id=?)`
	args := []any{feedID, feedID}
	if len(statuses) > 0 {
		q += ` AND status IN (`
		for i, st := range statuses {
			if i > 0 {
				q += `,`
			}
			q += `?`
			args = append(args, st)
		}
		q += `)`
	}
	q += ` ORDER BY published_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetEpisode(id int64) (Episode, bool) {
	e, err := scanEpisode(s.db.QueryRow(`SELECT `+episodeCols+` FROM episodes WHERE id=?`, id))
	if err == sql.ErrNoRows || err != nil {
		return Episode{}, false
	}
	return e, true
}

// SaveEpisode writes back everything about an episode that changes as
// she works through it.
func (s *Store) SaveEpisode(e Episode) error {
	takeaways, _ := json.Marshal(e.Takeaways)
	notes, _ := json.Marshal(e.Notes)
	if e.Thoughts == nil {
		e.Thoughts = []string{}
	}
	thoughts, _ := json.Marshal(e.Thoughts)
	_, err := s.db.Exec(`UPDATE episodes SET status=?,stage_note=?,error=?,summary=?,takeaways=?,share=?,notes=?,transcript_path=?,started_at=?,finished_at=?,thoughts=? WHERE id=?`,
		e.Status, e.StageNote, e.Error, e.Summary, string(takeaways), e.Share, string(notes), e.TranscriptPath, ms(e.StartedAt), ms(e.FinishedAt), string(thoughts), e.ID)
	return err
}
