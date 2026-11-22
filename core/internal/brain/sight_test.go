package brain

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSenses gives a print by the first byte: the same byte is the same
// person, and the second byte nudges it a little, like two photos of one
// face.
type fakeSenses struct{}

func (fakeSenses) print(data []byte) []float32 {
	v := make([]float32, 16)
	v[int(data[0])%16] = 1
	v[(int(data[0])+1)%16] = float32(data[1]) / 255 * 0.3
	return v
}
func (f fakeSenses) Faceprint(_ context.Context, rgb []byte) ([]float32, error) {
	return f.print(rgb), nil
}
func (f fakeSenses) Voiceprint(_ context.Context, wav []byte) ([]float32, error) {
	return f.print(wav), nil
}
func (fakeSenses) CanSeeFaces() bool   { return true }
func (fakeSenses) CanHearVoices() bool { return true }

func sightRig(t *testing.T) *rig {
	r := newRig(t)
	r.b.Senses = fakeSenses{}
	r.b.SightDir = t.TempDir()
	return r
}

func face(who, shot byte) []byte { return []byte{who, shot, 0} }

func TestAStrangerSeenTwiceIsAskedAboutAfterSleep(t *testing.T) {
	r := sightRig(t)
	ctx := context.Background()
	a, err := r.b.See(ctx, []byte("jpeg1"), face(3, 10), 4)
	if err != nil || a.Sure != "unknown" || !a.Kept {
		t.Fatalf("first sighting: %+v %v", a, err)
	}
	r.now = r.now.Add(time.Hour)
	b, _ := r.b.See(ctx, []byte("jpeg2"), face(3, 40), 3)
	if b.Face.GroupID != a.Face.GroupID {
		t.Fatalf("the same stranger should be one group: %d and %d", a.Face.GroupID, b.Face.GroupID)
	}
	other, _ := r.b.See(ctx, []byte("jpeg3"), face(9, 0), 1)
	if other.Face.GroupID == a.Face.GroupID {
		t.Fatal("a different face joined the stranger's group")
	}

	if n := r.b.faceQuestions(r.now); n != 1 {
		t.Fatalf("want one question (the face seen once for a second is not asked about), got %d", n)
	}
	open, _ := r.b.S.Intentions("open")
	var q *struct{ id, image int64 }
	for _, i := range open {
		if i.Kind == "face" {
			q = &struct{ id, image int64 }{i.ID, i.ImageID}
			if !strings.HasPrefix(i.Text, "Who is this?") || !strings.Contains(i.Text, "twice") {
				t.Errorf("question = %q", i.Text)
			}
		}
	}
	if q == nil || q.image == 0 {
		t.Fatal("the question should carry a picture")
	}
	if n := r.b.faceQuestions(r.now); n != 0 {
		t.Error("she should not ask twice about the same face")
	}

	who, err := r.b.AnswerFaces(a.Face.GroupID, FaceAnswer{Name: "Link", Relationship: "son"})
	if err != nil || who != "Link" {
		t.Fatalf("answer: %q %v", who, err)
	}
	p, ok := r.b.S.FindPerson("Link")
	if !ok || p.Relationship != "son" {
		t.Fatalf("a new person should be made: %+v", p)
	}
	still, _ := r.b.S.Intentions("open", "sent")
	for _, i := range still {
		if i.Kind == "face" && i.Subject == "faces:"+strconv.FormatInt(a.Face.GroupID, 10) {
			t.Error("the answered question is still open")
		}
	}

	r.now = r.now.Add(time.Hour)
	c, _ := r.b.See(ctx, []byte("jpeg4"), face(3, 25), 5)
	if c.Sure != "known" || c.Who != "Link" {
		t.Fatalf("after being told, she should know him: %+v", c)
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-time.Minute), r.now.Add(time.Minute), "sighting")
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "Link") {
		t.Errorf("a sighting of Link should be in her day: %+v", evs)
	}
	r.now = r.now.Add(time.Minute)
	if again, _ := r.b.See(ctx, []byte("jpeg5"), face(3, 30), 5); again.Kept {
		t.Error("someone seen a minute ago is not a new picture")
	}
}

func TestDoNotRememberThemDeletesThePictures(t *testing.T) {
	r := sightRig(t)
	s, _ := r.b.See(context.Background(), []byte("jpeg"), face(5, 0), 8)
	img, _ := r.b.S.GetImage(s.Face.ImageID)
	if _, err := os.Stat(img.Path); err != nil {
		t.Fatalf("the picture should be on disk: %v", err)
	}
	if _, err := r.b.AnswerFaces(s.Face.GroupID, FaceAnswer{Forget: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(img.Path); !os.IsNotExist(err) {
		t.Error("the picture should be deleted")
	}
	if len(r.b.UnknownGroups()) != 0 {
		t.Error("nothing of them should be left")
	}
}

func TestThatsMeFilesFacesAsTheUser(t *testing.T) {
	r := sightRig(t)
	s, _ := r.b.See(context.Background(), []byte("jpeg"), face(1, 0), 8)
	if who, _ := r.b.AnswerFaces(s.Face.GroupID, FaceAnswer{PersonID: UserID}); who != "Thomas" {
		t.Errorf("answered as the user, got %q", who)
	}
	r.now = r.now.Add(time.Hour)
	again, _ := r.b.See(context.Background(), []byte("jpeg"), face(1, 20), 8)
	if again.Sure != "known" || again.Who != "Thomas" {
		t.Errorf("she should know the user's face: %+v", again)
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-time.Minute), r.now.Add(time.Minute), "sighting")
	if len(evs) != 0 {
		t.Error("seeing the user is not a sighting to tell them about")
	}
}

func TestForgettingSomeoneForgetsTheirFace(t *testing.T) {
	r := sightRig(t)
	s, _ := r.b.See(context.Background(), []byte("jpeg"), face(7, 0), 8)
	r.b.AnswerFaces(s.Face.GroupID, FaceAnswer{Name: "Mark", Relationship: "brother"})
	p, _ := r.b.S.FindPerson("Mark")
	if err := r.b.ForgetPerson(p.ID); err != nil {
		t.Fatal(err)
	}
	if fs, _ := r.b.S.FacesOf(p.ID); len(fs) != 0 {
		t.Error("their faces should be gone")
	}
}

func TestWhoseVoiceItIs(t *testing.T) {
	r := sightRig(t)
	ctx := context.Background()
	if sp := r.b.WhoSpoke(ctx, []byte{2, 0}); sp.Who != "" {
		t.Errorf("with no voice known she cannot say: %+v", sp)
	}
	r.b.LearnVoice(ctx, "user", []byte{2, 0})
	r.b.LearnVoice(ctx, "her", []byte{11, 0})
	if sp := r.b.WhoSpoke(ctx, []byte{2, 50}); sp.Who != "you" {
		t.Errorf("the user's voice: %+v", sp)
	}
	if sp := r.b.WhoSpoke(ctx, []byte{11, 10}); sp.Who != "her" {
		t.Errorf("her own voice from the speakers: %+v", sp)
	}
	if sp := r.b.WhoSpoke(ctx, []byte{6, 0}); sp.Who != "someone" {
		t.Errorf("a voice she does not know: %+v", sp)
	}

	// Mark is in view and someone she does not know speaks: it is Mark.
	s, _ := r.b.See(ctx, []byte("jpeg"), face(7, 0), 8)
	r.b.AnswerFaces(s.Face.GroupID, FaceAnswer{Name: "Mark", Relationship: "brother"})
	r.now = r.now.Add(time.Minute)
	r.b.See(ctx, []byte("jpeg"), face(7, 5), 8)
	if sp := r.b.WhoSpoke(ctx, []byte{8, 0}); sp.Who != "person" || sp.Name != "Mark" {
		t.Fatalf("seen and heard at once should be Mark: %+v", sp)
	}
	r.now = r.now.Add(time.Hour)
	if sp := r.b.WhoSpoke(ctx, []byte{8, 20}); sp.Name != "Mark" {
		t.Errorf("later, without seeing him, his voice is known: %+v", sp)
	}
}
