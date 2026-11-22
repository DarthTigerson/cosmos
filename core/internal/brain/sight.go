package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmos/core/internal/store"
	"cosmos/core/internal/voice"
)

// Her sight and whose voice she hears. Each face the camera follows for a
// moment is turned into a print and matched against the people she knows.
// A face she knows is a sighting of that person. One she does not know is
// kept, with others like it, and after she sleeps she asks who it is,
// with the picture. Everything stays on this computer.

// Printer turns a face or a voice into a print. voice.Senses is the real
// one; tests use their own.
type Printer interface {
	Faceprint(ctx context.Context, rgb []byte) ([]float32, error)
	Voiceprint(ctx context.Context, wav []byte) ([]float32, error)
	CanSeeFaces() bool
	CanHearVoices() bool
}

// UserID stands for the user among people: faces of them are filed here,
// since they are not in her People list.
const UserID int64 = -1

// Sighting is what she made of a face.
type Sighting struct {
	Face store.Face `json:"face"`
	// Sure: known, maybe or unknown.
	Sure string `json:"sure"`
	// Who: the person's name when known or maybe; the user's name for them.
	Who string `json:"who,omitempty"`
	// Kept: false when the face was not stored, as a repeat of one just seen.
	Kept bool `json:"kept"`
}

type inView struct {
	mu   sync.Mutex
	seen map[int64]time.Time // person -> last seen
}

func (b *Brain) personName(id int64) string {
	if id == UserID {
		return b.Cfg.Get().UserName
	}
	if p, _, err := b.person(id); err == nil {
		return p.Name
	}
	return ""
}

// bestPerson finds who a print is most like, and how alike.
func bestPerson(vec []float32, known []store.Face) (int64, float64) {
	best := map[int64]float64{}
	for _, f := range known {
		if s := voice.Similarity(vec, f.Vec); s > best[f.PersonID] {
			best[f.PersonID] = s
		}
	}
	var who int64
	score := 0.0
	for id, s := range best {
		if s > score {
			who, score = id, s
		}
	}
	return who, score
}

// See takes a face the camera followed: a picture of it (JPEG) to keep and
// show, and the same face lined up at 112 by 112 for its print.
func (b *Brain) See(ctx context.Context, jpeg, rgb []byte, seconds float64) (Sighting, error) {
	if b.Senses == nil || !b.Senses.CanSeeFaces() {
		return Sighting{}, errors.New("faces are not set up")
	}
	if !b.Cfg.Get().RememberFaces {
		return Sighting{}, errors.New("she is not remembering faces")
	}
	vec, err := b.Senses.Faceprint(ctx, rgb)
	if err != nil {
		return Sighting{}, err
	}
	cfg := b.Cfg.Get()
	now := b.Now()
	known, _ := b.S.KnownFaces()
	who, score := bestPerson(vec, known)
	out := Sighting{Face: store.Face{At: now, Vec: vec, Score: score, Seconds: seconds}}
	switch {
	case who != 0 && score >= cfg.FaceSure:
		out.Sure, out.Face.Status, out.Face.PersonID = "known", "known", who
	case who != 0 && score >= cfg.FaceMaybe:
		out.Sure, out.Face.Status, out.Face.PersonID = "maybe", "maybe", who
	default:
		out.Sure, out.Face.Status = "unknown", "unknown"
	}
	if out.Face.PersonID != 0 {
		out.Who = b.personName(out.Face.PersonID)
	}

	if out.Sure == "known" {
		b.view.mu.Lock()
		if b.view.seen == nil {
			b.view.seen = map[int64]time.Time{}
		}
		last := b.view.seen[who]
		b.view.seen[who] = now
		b.view.mu.Unlock()
		// One sighting of someone every ten minutes is plenty; a face in
		// view all afternoon is not a hundred pictures.
		if !last.IsZero() && now.Sub(last) < 10*time.Minute {
			return out, nil
		}
		if who != UserID {
			meta, _ := json.Marshal(map[string]any{"person_id": who})
			b.S.AddEvent(store.Event{At: now, Kind: "sighting", Actor: cfg.CompanionName, Text: "Saw " + out.Who + ".", Importance: 1, Meta: meta})
		}
	}
	if out.Sure == "unknown" {
		// The same stranger seen again joins their group, up to a few
		// pictures of them.
		unknown, _ := b.S.UnknownFaces()
		group, alike := int64(0), 0.0
		count := map[int64]int{}
		for _, f := range unknown {
			count[f.GroupID]++
			if s := voice.Similarity(vec, f.Vec); s > alike {
				group, alike = f.GroupID, s
			}
		}
		if alike >= cfg.FaceSame {
			out.Face.GroupID = group
			if count[group] >= 8 {
				return out, nil
			}
		}
	}
	img, err := b.keepPicture("face", jpeg, now)
	if err != nil {
		return out, err
	}
	out.Face.ImageID = img.ID
	out.Face, err = b.S.AddFace(out.Face)
	out.Kept = err == nil
	return out, err
}

// keepPicture writes a picture into her data folder.
func (b *Brain) keepPicture(kind string, data []byte, now time.Time) (store.Image, error) {
	if b.SightDir == "" {
		return store.Image{}, errors.New("there is nowhere to keep pictures")
	}
	dir := filepath.Join(b.SightDir, kind+"s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return store.Image{}, err
	}
	path := filepath.Join(dir, strconv.FormatInt(now.UnixNano(), 36)+".jpg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return store.Image{}, err
	}
	return b.S.AddImage(store.Image{Kind: kind, Path: path, CreatedAt: now})
}

func removeFiles(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}

// InView lists the people seen in the last stretch of time, the user left
// out.
func (b *Brain) InView(within time.Duration) []int64 {
	b.view.mu.Lock()
	defer b.view.mu.Unlock()
	out := []int64{}
	for id, at := range b.view.seen {
		if id != UserID && b.Now().Sub(at) <= within {
			out = append(out, id)
		}
	}
	return out
}

// ---------- asking who it was ----------

// UnknownGroup is someone seen but not known: every sighting of them, and
// the picture that shows them best.
type UnknownGroup struct {
	ID      int64        `json:"id"`
	Faces   []store.Face `json:"faces"`
	Cover   int64        `json:"cover"`
	Seen    int          `json:"seen"`
	Seconds float64      `json:"seconds"`
	Last    time.Time    `json:"last"`
}

// UnknownGroups lists the people she has seen and does not know.
func (b *Brain) UnknownGroups() []UnknownGroup {
	faces, _ := b.S.UnknownFaces()
	byGroup := map[int64]*UnknownGroup{}
	order := []int64{}
	for _, f := range faces {
		g := byGroup[f.GroupID]
		if g == nil {
			g = &UnknownGroup{ID: f.GroupID}
			byGroup[f.GroupID] = g
			order = append(order, f.GroupID)
		}
		g.Faces = append(g.Faces, f)
		g.Seen++
		g.Seconds += f.Seconds
		if f.At.After(g.Last) {
			g.Last = f.At
		}
	}
	out := []UnknownGroup{}
	for _, id := range order {
		g := byGroup[id]
		best := g.Faces[0]
		for _, f := range g.Faces {
			if f.Seconds > best.Seconds {
				best = f
			}
		}
		g.Cover = best.ImageID
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// faceQuestions runs while she sleeps: pictures of strangers kept too long
// are let go, and those seen enough become a question, with the picture.
func (b *Brain) faceQuestions(now time.Time) int {
	cfg := b.Cfg.Get()
	if cfg.UnknownFaceDays > 0 {
		old := []store.Face{}
		faces, _ := b.S.UnknownFaces()
		for _, f := range faces {
			if now.Sub(f.At) > time.Duration(cfg.UnknownFaceDays)*24*time.Hour {
				old = append(old, f)
			}
		}
		removeFiles(b.S.DeleteFaces(old))
	}
	asking := map[string]bool{}
	open, _ := b.S.Intentions("open", "sent")
	for _, i := range open {
		if i.Subject != "" {
			asking[i.Subject] = true
		}
	}
	added := 0
	for _, g := range b.UnknownGroups() {
		subject := "faces:" + strconv.FormatInt(g.ID, 10)
		// Someone who walked past once is not worth asking about.
		if asking[subject] || (g.Seen < 2 && g.Seconds < 6) {
			continue
		}
		text := fmt.Sprintf("Who is this? I saw them %s, last on %s at %s.", times(g.Seen), g.Last.Format("Monday"), g.Last.Format("15:04"))
		b.S.AddIntention(store.Intention{Kind: "face", Text: text, Reason: "seen but not known", TriggerAt: now,
			ExpiresAt: now.Add(time.Duration(max(cfg.UnknownFaceDays, 1)) * 24 * time.Hour), Importance: 2, CreatedAt: now,
			ImageID: g.Cover, Subject: subject})
		added++
	}
	// Faces she half knew: was that them?
	recent, _ := b.S.FacesSince(now.Add(-36 * time.Hour))
	for _, f := range recent {
		subject := "maybe:" + strconv.FormatInt(f.ID, 10)
		if f.Status != "maybe" || asking[subject] {
			continue
		}
		name := b.personName(f.PersonID)
		if name == "" {
			continue
		}
		b.S.AddIntention(store.Intention{Kind: "face", Text: "Was this " + name + "? I was not sure.", Reason: "a face that looked like " + name,
			TriggerAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), Importance: 1, CreatedAt: now, ImageID: f.ImageID, Subject: subject})
		added++
	}
	return added
}

func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return strconv.Itoa(n) + " times"
}

// FaceAnswer is the user's answer to "who is this?".
type FaceAnswer struct {
	// PersonID names someone she knows; UserID (-1) means the user.
	PersonID int64 `json:"person_id"`
	// Name (and Relationship) make a new person.
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	// Forget: do not remember them at all.
	Forget bool `json:"forget"`
	// Yes or no, for "was this them?".
	Yes *bool `json:"yes"`
}

// AnswerFaces settles who a group of unknown faces is.
func (b *Brain) AnswerFaces(group int64, a FaceAnswer) (string, error) {
	faces, err := b.S.FacesInGroup(group)
	if err != nil || len(faces) == 0 {
		return "", errors.New("no such face")
	}
	if a.Forget {
		removeFiles(b.S.DeleteFaces(faces))
		b.settleFaceQuestion("faces:" + strconv.FormatInt(group, 10))
		return "", nil
	}
	who := a.PersonID
	if who == 0 {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return "", errors.New("say who it is")
		}
		if p, ok := b.S.FindPerson(name); ok {
			who = p.ID
		} else {
			p, err := b.S.SavePerson(store.Person{Name: name, Relationship: strings.TrimSpace(a.Relationship), Closeness: closenessFor(a.Relationship)}, b.Now())
			if err != nil {
				return "", err
			}
			who = p.ID
			b.Bus.Publish("people", nil)
		}
	} else if who != UserID {
		if _, _, err := b.person(who); err != nil {
			return "", err
		}
	}
	ids := []int64{}
	for _, f := range faces {
		ids = append(ids, f.ID)
	}
	if err := b.S.NameFaces(ids, who); err != nil {
		return "", err
	}
	b.settleFaceQuestion("faces:" + strconv.FormatInt(group, 10))
	return b.personName(who), nil
}

// AnswerMaybe settles "was this them?".
func (b *Brain) AnswerMaybe(faceID int64, yes bool) error {
	f, ok := b.S.GetFace(faceID)
	if !ok {
		return errors.New("no such face")
	}
	if yes {
		err := b.S.NameFaces([]int64{f.ID}, f.PersonID)
		b.settleFaceQuestion("maybe:" + strconv.FormatInt(faceID, 10))
		return err
	}
	// Not them: a stranger, until said otherwise.
	err := b.S.MakeUnknown(f.ID)
	b.settleFaceQuestion("maybe:" + strconv.FormatInt(faceID, 10))
	return err
}

// ForgetFace removes one picture of someone.
func (b *Brain) ForgetFace(id int64) error {
	f, ok := b.S.GetFace(id)
	if !ok {
		return errors.New("no such face")
	}
	removeFiles(b.S.DeleteFaces([]store.Face{f}))
	return nil
}

func (b *Brain) settleFaceQuestion(subject string) {
	open, _ := b.S.Intentions("open", "sent")
	for _, i := range open {
		if i.Subject == subject {
			b.S.SetIntentionStatus(i.ID, "done", b.Now())
			b.S.MarkIntentionAnswered(i.ID)
		}
	}
	b.Bus.Publish("people", nil)
}

// forgetPrints removes everything she has seen and heard of a person.
func (b *Brain) forgetPrints(personID int64) {
	faces, _ := b.S.FacesOf(personID)
	removeFiles(b.S.DeleteFaces(faces))
	b.S.ForgetVoice("person", personID)
}

// ---------- whose voice it is ----------

// Speaker is whose voice something was said in.
type Speaker struct {
	// Who: you (the user), her (her own voice from the speakers), a
	// person, or someone (not known). Empty when voices are not set up
	// or no voice is known yet.
	Who      string  `json:"who"`
	Name     string  `json:"name,omitempty"`
	PersonID int64   `json:"person_id,omitempty"`
	Score    float64 `json:"score"`
}

// WhoSpoke says whose voice a recording is in. A voice she does not know,
// heard while one person she knows is in view, becomes theirs.
func (b *Brain) WhoSpoke(ctx context.Context, wav []byte) Speaker {
	if b.Senses == nil || !b.Senses.CanHearVoices() {
		return Speaker{}
	}
	prints, _ := b.S.Voiceprints()
	if len(prints) == 0 {
		return Speaker{}
	}
	vec, err := b.Senses.Voiceprint(ctx, wav)
	if err != nil {
		return Speaker{}
	}
	cfg := b.Cfg.Get()
	best, score := store.Voiceprint{}, 0.0
	perPerson := map[int64]int{}
	for _, p := range prints {
		if p.Owner == "person" {
			perPerson[p.PersonID]++
		}
		if s := voice.Similarity(vec, p.Vec); s > score {
			best, score = p, s
		}
	}
	if score >= cfg.VoiceSure {
		switch best.Owner {
		case "user":
			return Speaker{Who: "you", Name: cfg.UserName, Score: score}
		case "her":
			return Speaker{Who: "her", Name: cfg.CompanionName, Score: score}
		}
		return Speaker{Who: "person", Name: b.personName(best.PersonID), PersonID: best.PersonID, Score: score}
	}
	// Seen and heard at once: the one person in view is who is talking.
	if view := b.InView(20 * time.Second); len(view) == 1 && perPerson[view[0]] < 5 {
		b.S.AddVoiceprint(store.Voiceprint{Owner: "person", PersonID: view[0], Vec: vec, CreatedAt: b.Now()})
		return Speaker{Who: "person", Name: b.personName(view[0]), PersonID: view[0], Score: score}
	}
	return Speaker{Who: "someone", Score: score}
}

// IsHer says whether a stretch of sound is her own voice, back from the
// speakers. It only compares; nothing is learned from it, since a scrap
// heard while she talks is no one's voice to keep. known is false when
// she has no print of her own voice to compare with.
func (b *Brain) IsHer(ctx context.Context, wav []byte) (her, known bool) {
	if b.Senses == nil || !b.Senses.CanHearVoices() {
		return false, false
	}
	prints, _ := b.S.Voiceprints()
	if !slices.ContainsFunc(prints, func(p store.Voiceprint) bool { return p.Owner == "her" }) {
		return false, false
	}
	vec, err := b.Senses.Voiceprint(ctx, wav)
	if err != nil {
		return false, true
	}
	mine, theirs := 0.0, 0.0
	for _, p := range prints {
		s := voice.Similarity(vec, p.Vec)
		if p.Owner == "her" {
			mine = math.Max(mine, s)
		} else {
			theirs = math.Max(theirs, s)
		}
	}
	// A short scrap matches less well than a sentence, so the bar is
	// lower than for knowing who spoke; it must still be her more than
	// anyone.
	return mine > theirs && mine >= b.Cfg.Get().VoiceSure*0.8, true
}

// LearnVoice keeps a voiceprint for the user ("user") or for her own voice
// ("her"), replacing the old one.
func (b *Brain) LearnVoice(ctx context.Context, owner string, wav []byte) error {
	if b.Senses == nil || !b.Senses.CanHearVoices() {
		return errors.New("voices are not set up")
	}
	vec, err := b.Senses.Voiceprint(ctx, wav)
	if err != nil {
		return err
	}
	b.S.ForgetVoice(owner, 0)
	_, err = b.S.AddVoiceprint(store.Voiceprint{Owner: owner, Vec: vec, CreatedAt: b.Now()})
	return err
}

// KnowsVoice says whether she has a voiceprint for an owner.
func (b *Brain) KnowsVoice(owner string) bool {
	prints, _ := b.S.Voiceprints()
	for _, p := range prints {
		if p.Owner == owner {
			return true
		}
	}
	return false
}
