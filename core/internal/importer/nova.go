// Package importer brings data across from the archived Nova app.
package importer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cosmos/core/internal/store"
)

type Counts struct {
	Memories int `json:"memories"`
	Journal  int `json:"journal"`
	People   int `json:"people"`
	Skipped  int `json:"skipped"`
}

func readJSON(path string, into any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func key(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func closeness(relationship string) float64 {
	r := strings.ToLower(relationship)
	switch {
	case strings.Contains(r, "family"), strings.Contains(r, "partner"), strings.Contains(r, "wife"), strings.Contains(r, "husband"):
		return 0.9
	case strings.Contains(r, "friend"):
		return 0.7
	case strings.Contains(r, "colleague"), strings.Contains(r, "manager"):
		return 0.4
	}
	return 0.3
}

// Nova reads diary.json and humans.json from Nova's data folder and adds
// what is not already in the store. It only reads from dir. Running it
// twice adds nothing the second time. Calendar entries are not imported:
// Nova's recurring events have no equivalent here yet.
func Nova(s *store.Store, dir string) (Counts, error) {
	var c Counts
	var diary struct {
		Memories []struct {
			Category  string `json:"category"`
			Content   string `json:"content"`
			CreatedAt int64  `json:"createdAt"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"memories"`
		Journal []struct {
			Content   string `json:"content"`
			Source    string `json:"source"`
			CreatedAt int64  `json:"createdAt"`
		} `json:"journal"`
	}
	if err := readJSON(filepath.Join(dir, "diary.json"), &diary); err != nil {
		return c, err
	}
	var humans struct {
		Humans []struct {
			Name          string   `json:"name"`
			Aliases       []string `json:"aliases"`
			Relationship  string   `json:"relationship"`
			Notes         string   `json:"notes"`
			LastMentioned string   `json:"lastMentioned"`
		} `json:"humans"`
	}
	if err := readJSON(filepath.Join(dir, "humans.json"), &humans); err != nil {
		return c, err
	}

	existing, err := s.Memories()
	if err != nil {
		return c, err
	}
	have := map[string]bool{}
	for _, m := range existing {
		have[key(m.Content)] = true
	}
	for _, m := range diary.Memories {
		k := key(m.Content)
		if k == "" || have[k] {
			c.Skipped++
			continue
		}
		have[k] = true
		created := time.UnixMilli(m.CreatedAt)
		updated := created
		if m.UpdatedAt > 0 {
			updated = time.UnixMilli(m.UpdatedAt)
		}
		if _, err := s.AddMemory(store.Memory{Kind: "fact", Category: m.Category, Content: strings.TrimSpace(m.Content), CreatedAt: created, UpdatedAt: updated}); err != nil {
			return c, err
		}
		c.Memories++
	}

	old, err := s.EventsBetween(time.UnixMilli(1), time.Now().Add(time.Hour), "journal")
	if err != nil {
		return c, err
	}
	haveJournal := map[string]bool{}
	for _, e := range old {
		haveJournal[key(e.Text)] = true
	}
	for _, j := range diary.Journal {
		k := key(j.Content)
		if k == "" || haveJournal[k] {
			c.Skipped++
			continue
		}
		haveJournal[k] = true
		if _, err := s.AddEvent(store.Event{At: time.UnixMilli(j.CreatedAt), Kind: "journal", Actor: "Nova", Text: strings.TrimSpace(j.Content), Importance: 2}); err != nil {
			return c, err
		}
		c.Journal++
	}

	for _, h := range humans.Humans {
		if strings.TrimSpace(h.Name) == "" {
			continue
		}
		if _, ok := s.FindPerson(h.Name); ok {
			c.Skipped++
			continue
		}
		p := store.Person{Name: strings.TrimSpace(h.Name), Aliases: h.Aliases, Relationship: h.Relationship, Closeness: closeness(h.Relationship), Notes: h.Notes}
		if t, err := time.ParseInLocation("2006-01-02", h.LastMentioned, time.Local); err == nil {
			p.LastMentioned = t
		}
		if _, err := s.SavePerson(p, time.Now()); err != nil {
			return c, err
		}
		c.People++
	}
	return c, nil
}
