package store

import (
	"encoding/binary"
	"math"
	"strings"
	"time"
)

// ---------- what she sees and whose voice she hears ----------

// Pictures she keeps are files in her data folder; the rows say what each
// one is. Faces are the crops of people she has seen, each with its print
// (numbers that are close for the same person). Voiceprints are the same
// for voices. None of it ever leaves this computer.

const sightSchema = `
CREATE TABLE IF NOT EXISTS images(
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, path TEXT NOT NULL,
  created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS faces(
  id INTEGER PRIMARY KEY, at INTEGER NOT NULL, image_id INTEGER NOT NULL,
  vec BLOB NOT NULL, person_id INTEGER NOT NULL DEFAULT 0,
  group_id INTEGER NOT NULL DEFAULT 0, score REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL, seconds REAL NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS faces_person ON faces(person_id);
CREATE INDEX IF NOT EXISTS faces_group ON faces(group_id);
CREATE TABLE IF NOT EXISTS voiceprints(
  id INTEGER PRIMARY KEY, owner TEXT NOT NULL, person_id INTEGER NOT NULL DEFAULT 0,
  vec BLOB NOT NULL, created_at INTEGER NOT NULL);
`

type Image struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"` // face, moment
	Path      string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// Face is one sighting of a face.
type Face struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	ImageID  int64     `json:"image_id"`
	Vec      []float32 `json:"-"`
	PersonID int64     `json:"person_id"`
	// GroupID gathers sightings of the same unknown person.
	GroupID int64   `json:"group_id"`
	Score   float64 `json:"score"`
	// Status: confirmed (the user said who it is), known (a sure match),
	// maybe (a weak match), unknown.
	Status  string  `json:"status"`
	Seconds float64 `json:"seconds"`
}

// Voiceprint is a voice she knows: the user's, her own, or a person's.
type Voiceprint struct {
	ID        int64     `json:"id"`
	Owner     string    `json:"owner"` // user, her, person
	PersonID  int64     `json:"person_id"`
	Vec       []float32 `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func packVec(v []float32) []byte {
	raw := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(x))
	}
	return raw
}

func unpackVec(raw []byte) []float32 {
	v := make([]float32, len(raw)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return v
}

func (s *Store) AddImage(i Image) (Image, error) {
	res, err := s.db.Exec(`INSERT INTO images(kind,path,created_at) VALUES(?,?,?)`, i.Kind, i.Path, ms(i.CreatedAt))
	if err != nil {
		return i, err
	}
	i.ID, _ = res.LastInsertId()
	return i, nil
}

func (s *Store) GetImage(id int64) (Image, bool) {
	var i Image
	var c int64
	err := s.db.QueryRow(`SELECT id,kind,path,created_at FROM images WHERE id=?`, id).Scan(&i.ID, &i.Kind, &i.Path, &c)
	i.CreatedAt = fromMs(c)
	return i, err == nil
}

// DeleteImage removes the row and returns the file it named, for the
// caller to delete.
func (s *Store) DeleteImage(id int64) string {
	i, ok := s.GetImage(id)
	if !ok {
		return ""
	}
	s.db.Exec(`DELETE FROM images WHERE id=?`, id)
	return i.Path
}

func (s *Store) AddFace(f Face) (Face, error) {
	res, err := s.db.Exec(`INSERT INTO faces(at,image_id,vec,person_id,group_id,score,status,seconds) VALUES(?,?,?,?,?,?,?,?)`,
		ms(f.At), f.ImageID, packVec(f.Vec), f.PersonID, f.GroupID, f.Score, f.Status, f.Seconds)
	if err != nil {
		return f, err
	}
	f.ID, _ = res.LastInsertId()
	if f.GroupID == 0 && f.Status == "unknown" {
		// A new unknown person starts a group of their own.
		f.GroupID = f.ID
		s.db.Exec(`UPDATE faces SET group_id=? WHERE id=?`, f.ID, f.ID)
	}
	return f, nil
}

const faceCols = `id,at,image_id,vec,person_id,group_id,score,status,seconds`

func (s *Store) faces(where string, args ...any) ([]Face, error) {
	rows, err := s.db.Query(`SELECT `+faceCols+` FROM faces WHERE `+where+` ORDER BY at,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Face{}
	for rows.Next() {
		var f Face
		var at int64
		var raw []byte
		if err := rows.Scan(&f.ID, &at, &f.ImageID, &raw, &f.PersonID, &f.GroupID, &f.Score, &f.Status, &f.Seconds); err != nil {
			return nil, err
		}
		f.At, f.Vec = fromMs(at), unpackVec(raw)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFace(id int64) (Face, bool) {
	f, err := s.faces(`id=?`, id)
	if err != nil || len(f) == 0 {
		return Face{}, false
	}
	return f[0], true
}

// KnownFaces are the faces that say what a person looks like: the ones
// the user named and the sure matches.
func (s *Store) KnownFaces() ([]Face, error) {
	return s.faces(`person_id!=0 AND status IN ('confirmed','known')`)
}

// FacesOf lists every sighting of a person, oldest first.
func (s *Store) FacesOf(personID int64) ([]Face, error) {
	return s.faces(`person_id=?`, personID)
}

// UnknownFaces lists sightings of people she does not know yet.
func (s *Store) UnknownFaces() ([]Face, error) { return s.faces(`status='unknown'`) }

func (s *Store) FacesInGroup(group int64) ([]Face, error) {
	return s.faces(`group_id=? AND status='unknown'`, group)
}

// FacesSince lists sightings from a time on.
func (s *Store) FacesSince(t time.Time) ([]Face, error) { return s.faces(`at>=?`, ms(t)) }

// NameFaces says who the faces are: they become that person's, as the
// user said so.
func (s *Store) NameFaces(ids []int64, personID int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `UPDATE faces SET person_id=?, status='confirmed', group_id=0 WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	args := []any{personID}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.Exec(q, args...)
	return err
}

// MakeUnknown turns a sighting back into a stranger of their own.
func (s *Store) MakeUnknown(id int64) error {
	_, err := s.db.Exec(`UPDATE faces SET person_id=0, status='unknown', group_id=id, score=0 WHERE id=?`, id)
	return err
}

// DeleteFaces removes sightings and their pictures' rows, and returns the
// files to delete.
func (s *Store) DeleteFaces(fs []Face) []string {
	files := []string{}
	for _, f := range fs {
		s.db.Exec(`DELETE FROM faces WHERE id=?`, f.ID)
		if p := s.DeleteImage(f.ImageID); p != "" {
			files = append(files, p)
		}
	}
	return files
}

func (s *Store) AddVoiceprint(v Voiceprint) (Voiceprint, error) {
	res, err := s.db.Exec(`INSERT INTO voiceprints(owner,person_id,vec,created_at) VALUES(?,?,?,?)`, v.Owner, v.PersonID, packVec(v.Vec), ms(v.CreatedAt))
	if err != nil {
		return v, err
	}
	v.ID, _ = res.LastInsertId()
	return v, nil
}

func (s *Store) Voiceprints() ([]Voiceprint, error) {
	rows, err := s.db.Query(`SELECT id,owner,person_id,vec,created_at FROM voiceprints ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Voiceprint{}
	for rows.Next() {
		var v Voiceprint
		var raw []byte
		var c int64
		if err := rows.Scan(&v.ID, &v.Owner, &v.PersonID, &raw, &c); err != nil {
			return nil, err
		}
		v.Vec, v.CreatedAt = unpackVec(raw), fromMs(c)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ForgetVoice removes the voiceprints of an owner ("user", "her"), or of a
// person when owner is "person".
func (s *Store) ForgetVoice(owner string, personID int64) error {
	_, err := s.db.Exec(`DELETE FROM voiceprints WHERE owner=? AND person_id=?`, owner, personID)
	return err
}

// MovePrints gives one person's faces and voice to another, when two
// entries turn out to be the same person.
func (s *Store) MovePrints(from, to int64) error {
	if _, err := s.db.Exec(`UPDATE faces SET person_id=? WHERE person_id=?`, to, from); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE voiceprints SET person_id=? WHERE person_id=? AND owner='person'`, to, from)
	return err
}
