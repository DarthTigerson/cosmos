package brain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// writerImages is what the last writer request carried as pictures.
func writerImages(r *rig) []string {
	var got []string
	for _, c := range r.fake.Calls {
		if c.Role == "writer" && len(c.Messages) > 0 {
			got = c.Messages[len(c.Messages)-1].Images
		}
	}
	return got
}

func pasted(t *testing.T, r *rig, sees bool) Reply {
	t.Helper()
	r.b.SightDir = t.TempDir()
	cfg := r.b.Cfg.Get()
	cfg.MainSees = sees
	r.b.Cfg.Set(cfg)
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("not really a jpeg"))
	ctx := WithSaid(context.Background(), Said{Pictures: []string{uri}})
	reply, err := r.b.HandleMessage(ctx, "what is this?")
	r.b.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestPastedPicturesReachTheWriterWhenTheModelSees(t *testing.T) {
	r := newRig(t)
	pasted(t, r, true)
	if got := writerImages(r); len(got) != 1 || !strings.HasPrefix(got[0], "data:image/") {
		t.Fatalf("the writer got %v", got)
	}
}

func TestPastedPicturesAreNotSentWhenTheModelCannotSee(t *testing.T) {
	r := newRig(t)
	pasted(t, r, false)
	if got := writerImages(r); len(got) != 0 {
		t.Fatalf("the writer got %d pictures", len(got))
	}
}

func TestPastedPicturesAreKeptWithTheMessage(t *testing.T) {
	r := newRig(t)
	pasted(t, r, true)
	evs, _ := r.b.S.RecentEvents(5, "user_msg")
	if len(evs) != 1 {
		t.Fatalf("got %d messages", len(evs))
	}
	var meta struct {
		ImageIDs []int64 `json:"image_ids"`
	}
	json.Unmarshal(evs[0].Meta, &meta)
	if len(meta.ImageIDs) != 1 {
		t.Fatalf("meta %s", evs[0].Meta)
	}
	if img, ok := r.b.S.GetImage(meta.ImageIDs[0]); !ok || img.Kind != "pasted" {
		t.Fatalf("image %v %v", img, ok)
	}
}
