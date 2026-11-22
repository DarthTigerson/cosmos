package brain

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"cosmos/core/internal/store"
)

// PersonNames are the names someone goes by: their name, their other
// names, and the first word of a full name ("Bethany" for "Bethany
// Bonnici").
func PersonNames(p store.Person) []string {
	names := []string{p.Name}
	for _, a := range p.Aliases {
		if a = strings.TrimSpace(a); a != "" {
			names = append(names, a)
		}
	}
	if first, _, ok := strings.Cut(strings.TrimSpace(p.Name), " "); ok && len(first) >= 3 {
		names = append(names, first)
	}
	return names
}

// Mentions reports whether a text names someone, as whole words: "Tom"
// is in "Tom's bike" but not in "tomorrow". Where the text uses capitals
// at all, a name has to start with one, so "Link" is not in "here is the
// link". Text typed all in lower case matches either way.
func Mentions(text string, names []string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	cased := strings.ToLower(text) != text
	for _, n := range names {
		want := strings.Fields(norm(n))
		if len(want) == 0 {
			continue
		}
		for i := 0; i+len(want) <= len(words); i++ {
			match := true
			for j, w := range want {
				if strings.ToLower(words[i+j]) != w {
					match = false
					break
				}
			}
			if match && (!cased || unicode.IsUpper([]rune(words[i])[0])) {
				return true
			}
		}
	}
	return false
}

// Connection is someone named in the same memories as another person.
type Connection struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	Shared       int    `json:"shared"` // memories naming both
}

// PersonView is everything the People screen shows about one person.
type PersonView struct {
	Person      store.Person   `json:"person"`
	Memories    []store.Memory `json:"memories"`
	Moments     []store.Event  `json:"moments"`
	Mentions    int            `json:"mentions"` // in all conversations
	Weeks       []int          `json:"weeks"`    // mentions per week, oldest first, the last is this week
	Connections []Connection   `json:"connections"`
	User        string         `json:"user"`
	Companion   string         `json:"companion"`
	// Faces are the pictures she has of them, newest first; Voice says
	// whether she knows their voice.
	Faces []store.Face `json:"faces"`
	Voice bool         `json:"voice"`
}

var errNoPerson = errors.New("no such person")

func (b *Brain) person(id int64) (store.Person, []store.Person, error) {
	people, err := b.S.People()
	if err != nil {
		return store.Person{}, nil, err
	}
	for _, p := range people {
		if p.ID == id {
			return p, people, nil
		}
	}
	return store.Person{}, nil, errNoPerson
}

// mentionsOf returns the conversation moments that name someone, newest
// first, from all of her history.
func (b *Brain) mentionsOf(p store.Person) []store.Event {
	names := PersonNames(p)
	found, _ := b.S.EventsMentioning(names, "user_msg", "nova_msg", "initiative")
	out := []store.Event{}
	for _, e := range found {
		if Mentions(e.Text, names) {
			out = append(out, e)
		}
	}
	return out
}

// Person gathers what she knows about someone: the memories and moments
// that name them, how often they came up in the last twelve weeks, and
// who else is named alongside them.
func (b *Brain) Person(id int64) (PersonView, error) {
	p, people, err := b.person(id)
	if err != nil {
		return PersonView{}, err
	}
	names := PersonNames(p)
	view := PersonView{Person: p, Memories: []store.Memory{}, Weeks: make([]int, 12), Connections: []Connection{}, User: b.Cfg.Get().UserName, Companion: b.Cfg.Get().CompanionName}
	view.Faces, _ = b.S.FacesOf(id)
	slices.Reverse(view.Faces)
	if len(view.Faces) > 24 {
		view.Faces = view.Faces[:24]
	}
	prints, _ := b.S.Voiceprints()
	for _, v := range prints {
		view.Voice = view.Voice || (v.Owner == "person" && v.PersonID == id)
	}
	all, _ := b.S.Memories()
	for _, m := range all {
		if (m.Kind == "fact" || m.Kind == "reflection") && Mentions(m.Content, names) {
			view.Memories = append(view.Memories, m)
		}
	}
	moments := b.mentionsOf(p)
	view.Mentions = len(moments)
	view.Moments = moments[:min(len(moments), 20)]
	now := b.Now()
	for _, e := range moments {
		if week := int(now.Sub(e.At) / (7 * 24 * time.Hour)); week >= 0 && week < 12 {
			view.Weeks[11-week]++
		}
	}
	for _, other := range people {
		if other.ID == p.ID {
			continue
		}
		shared := 0
		for _, m := range view.Memories {
			if Mentions(m.Content, PersonNames(other)) {
				shared++
			}
		}
		if shared > 0 {
			view.Connections = append(view.Connections, Connection{ID: other.ID, Name: other.Name, Relationship: other.Relationship, Shared: shared})
		}
	}
	sort.SliceStable(view.Connections, func(i, j int) bool { return view.Connections[i].Shared > view.Connections[j].Shared })
	return view, nil
}

// Heard is the last thing the user said that names someone, for asking
// who they are.
func (b *Brain) Heard(p store.Person) string {
	for _, e := range b.mentionsOf(p) {
		if e.Kind == "user_msg" {
			return clip(e.Text, 160)
		}
	}
	return ""
}

// PersonEdit is a change the user makes by hand. Nil fields are left as
// they are.
type PersonEdit struct {
	Name         *string   `json:"name"`
	Relationship *string   `json:"relationship"`
	Aliases      *[]string `json:"aliases"`
	Closeness    *float64  `json:"closeness"`
	Notes        *[]string `json:"notes"`
}

// EditPerson applies the user's change. Every field they change is
// locked, so what she later picks up in conversation cannot undo it.
// Notes are never locked: she keeps adding to them.
func (b *Brain) EditPerson(id int64, in PersonEdit) (store.Person, error) {
	p, people, err := b.person(id)
	if err != nil {
		return p, err
	}
	lock := func(field string) {
		if !p.Has(field) {
			p.Locked = append(p.Locked, field)
		}
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return p, errors.New("a person needs a name")
		}
		for _, other := range people {
			if other.ID != p.ID && strings.EqualFold(other.Name, name) {
				return p, errors.New("someone else is already called " + name + "; merge them instead")
			}
		}
		if name != p.Name {
			p.Name = name
			lock("name")
		}
	}
	if in.Aliases != nil {
		p.Aliases = tidyNames(*in.Aliases, p.Name)
		lock("aliases")
	}
	if in.Relationship != nil {
		if rel := strings.TrimSpace(*in.Relationship); rel != p.Relationship {
			p.Relationship = rel
			lock("relationship")
			if in.Closeness == nil && !p.Has("closeness") {
				p.Closeness = closenessFor(rel)
			}
		}
	}
	if in.Closeness != nil {
		p.Closeness = clamp(*in.Closeness, 0, 1)
		lock("closeness")
	}
	if in.Notes != nil {
		kept := []string{}
		for _, n := range *in.Notes {
			if n = strings.TrimSpace(n); n != "" {
				kept = append(kept, n)
			}
		}
		p.Notes = strings.Join(kept, "\n")
	}
	_, err = b.S.SavePerson(p, b.Now())
	return p, err
}

// tidyNames drops blanks, repeats and the person's own name.
func tidyNames(names []string, own string) []string {
	out := []string{}
	seen := map[string]bool{strings.ToLower(own): true}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if k := strings.ToLower(n); n != "" && !seen[k] {
			seen[k] = true
			out = append(out, n)
		}
	}
	return out
}

// MergePerson folds a duplicate into the person it really is. The
// duplicate's names become other names, its notes carry over, and its
// relationship is taken only when the other has none.
func (b *Brain) MergePerson(fromID, intoID int64) (store.Person, error) {
	if fromID == intoID {
		return store.Person{}, errors.New("pick someone else to merge into")
	}
	from, _, err := b.person(fromID)
	if err != nil {
		return from, err
	}
	into, _, err := b.person(intoID)
	if err != nil {
		return into, err
	}
	into.Aliases = tidyNames(append(append(into.Aliases, from.Name), from.Aliases...), into.Name)
	for _, n := range strings.Split(from.Notes, "\n") {
		if n = strings.TrimSpace(n); n != "" && !strings.Contains(norm(into.Notes), norm(n)) {
			into.Notes = strings.TrimSpace(into.Notes + "\n" + n)
		}
	}
	if placeholder(into.Relationship) && !placeholder(from.Relationship) {
		into.Relationship = from.Relationship
	}
	if !into.Has("closeness") && from.Closeness > into.Closeness {
		into.Closeness = from.Closeness
	}
	if from.LastMentioned.After(into.LastMentioned) {
		into.LastMentioned = from.LastMentioned
	}
	if _, err := b.S.SavePerson(into, b.Now()); err != nil {
		return into, err
	}
	// Their faces and voice are the same person's too.
	if err := b.S.MovePrints(from.ID, into.ID); err != nil {
		return into, err
	}
	return into, b.S.DeletePerson(from.ID)
}

// ForgetPerson stops her keeping anything on someone. Memories that name
// them stay; the user can forget those on their own.
func (b *Brain) ForgetPerson(id int64) error {
	if _, _, err := b.person(id); err != nil {
		return err
	}
	// Forgetting someone takes their pictures and voice with them.
	b.forgetPrints(id)
	return b.S.ForgetPerson(id)
}

// IsNoPerson reports an error about a person who does not exist.
func IsNoPerson(err error) bool { return errors.Is(err, errNoPerson) }
