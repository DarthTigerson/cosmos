package brain

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cosmos/core/internal/llm"
)

const testFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
 <channel>
  <title>The Test Show</title>
  <description>Two people talk about computers.</description>
  <image><url>ART/plain.jpg</url></image>
  <itunes:image href="ART/cover.jpg"/>
  <item><title>Newest episode</title><guid>g3</guid><pubDate>Sat, 14 Nov 2026 04:30:00 -0000</pubDate>
   <itunes:duration>3:42:10</itunes:duration><enclosure url="AUDIO/3.mp3" type="audio/mpeg"/></item>
  <item><title>Older episode</title><guid>g2</guid><pubDate>Sat, 07 Nov 2026 04:30:00 -0000</pubDate>
   <itunes:duration>5400</itunes:duration><enclosure url="AUDIO/2.mp3" type="audio/mpeg"/></item>
  <item><title>A post with no audio</title><guid>g1</guid></item>
 </channel>
</rss>`

func TestParseFeed(t *testing.T) {
	feed, eps, err := parseFeed(strings.NewReader(testFeed))
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "The Test Show" || feed.ImageURL != "ART/cover.jpg" {
		t.Errorf("feed = %+v", feed)
	}
	if len(eps) != 2 || eps[0].Title != "Newest episode" || eps[0].DurationSec != 3*3600+42*60+10 || eps[1].DurationSec != 5400 {
		t.Fatalf("episodes = %+v", eps)
	}
	if eps[0].PublishedAt.Day() != 14 {
		t.Errorf("published = %v", eps[0].PublishedAt)
	}
	if _, _, err := parseFeed(strings.NewReader("<html><body>not a feed</body></html>")); err == nil {
		t.Error("a web page is not a feed")
	}
}

// fakeWhisper writes a fixed transcript.
type fakeWhisper struct{ text string }

func (f fakeWhisper) Ready() string { return "" }
func (f fakeWhisper) Transcribe(_ context.Context, audio, outBase string, progress func(int)) (string, error) {
	if _, err := os.Stat(audio); err != nil {
		return "", err
	}
	progress(100)
	return outBase + ".txt", os.WriteFile(outBase+".txt", []byte(f.text), 0o600)
}

func TestSheListensToANewShow(t *testing.T) {
	r := newRig(t)
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		switch {
		case q.URL.Path == "/feed":
			io.WriteString(w, strings.ReplaceAll(strings.ReplaceAll(testFeed, "AUDIO", base+"/audio"), "ART", base+"/art"))
		case strings.HasPrefix(q.URL.Path, "/art/"):
			w.Header().Set("Content-Type", "image/jpeg")
			io.WriteString(w, "not really a jpeg")
		case strings.HasPrefix(q.URL.Path, "/audio/"):
			io.WriteString(w, "not really audio")
		default:
			http.NotFound(w, q)
		}
	}))
	defer srv.Close()
	base = srv.URL
	r.b.PodDir = t.TempDir()
	r.b.PodHTTP = srv.Client()
	r.b.Transcribe = fakeWhisper{text: strings.Repeat("small models are checked by a larger model ", 300)}
	parts := 0
	r.fake.Handlers["podcast-notes"] = func(q llm.Request) (string, error) {
		parts++
		return `{"notes":["The hosts say small models work well when a larger model checks them."]}`, nil
	}
	r.fake.Handlers["podcast-think"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[0].Content, "small models work well") {
			t.Error("her notes were not handed to the thinking step")
		}
		return `{"summary":"They talked about small models.","takeaways":["The hosts think small models work well when a larger model checks them."],"thoughts":["I think the hosts are right about small models, because that is how I work.","The hosts like small models."],"share":"They talked about the small-models idea you are trying."}`, nil
	}

	feed, err := r.b.AddFeed(context.Background(), srv.URL+"/feed")
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "The Test Show" || feed.ArtPath == "" {
		t.Fatalf("feed = %+v", feed)
	}
	v := r.b.Podcasts()
	if len(v.Queued) != 1 || v.Queued[0].Title != "Newest episode" {
		t.Fatalf("only the newest episode should be queued: %+v", v.Queued)
	}

	// Awake, it waits for her sleep.
	r.b.podcastTick(context.Background())
	r.b.Wait()
	if parts != 0 {
		t.Fatal("awake, she should not listen to an episode nobody asked for now")
	}
	if err := r.b.QueueEpisode(v.Queued[0].ID, true); err != nil {
		t.Fatal(err)
	}
	r.b.podcastTick(context.Background())
	r.b.Wait()

	v = r.b.Podcasts()
	if len(v.Done) != 1 || v.Current != nil {
		t.Fatalf("the episode should be finished: done=%+v failed=%+v", v.Done, v.Failed)
	}
	e := v.Done[0]
	if len(e.Thoughts) != 1 || !strings.HasPrefix(e.Thoughts[0], "I think") {
		t.Fatalf("only a view said as hers counts as her thought: %+v", e.Thoughts)
	}
	if kept := r.b.EpisodeMemories(e); len(kept) != 2 {
		t.Fatalf("she should hold one thing heard and one view: %+v", kept)
	}
	if parts != 3 || e.Summary == "" || len(e.Takeaways) != 1 || len(e.Notes) != 3 || !e.HasTranscript {
		t.Fatalf("episode = %+v (parts read: %d)", e, parts)
	}
	// What she heard is in her memory as heard, and reaches the writer
	// apart from what the user told her.
	_, notes, _ := r.b.writerParts(r.b.Cfg.Get(), r.b.Mood(), "small models")
	if !strings.Contains(notes, "From podcasts you listened to on your own") || !strings.Contains(notes, "Heard on The Test Show: Newest episode") {
		t.Fatalf("the writer was not told what she heard:\n%s", notes)
	}
	if strings.Contains(strings.Split(notes, "From podcasts you listened to on your own")[0], "small models work well") {
		t.Error("something heard on a podcast was listed as known about the user")
	}
	open, _ := r.b.S.Intentions("open")
	if len(open) != 1 || open[0].Kind != "podcast" {
		t.Fatalf("she should want to bring it up: %+v", open)
	}
	res, _ := r.b.searchPodcasts("small models")
	if found := res.(map[string]any)["found"].([]map[string]any); len(found) == 0 {
		t.Fatal("searching what she heard found nothing")
	}
	// The older episode waits until asked for.
	all, _ := r.b.S.Episodes(feed.ID, 10, "waiting")
	if len(all) != 1 {
		t.Fatalf("the older episode should be waiting: %+v", all)
	}
	if err := r.b.QueueEpisode(all[0].ID, false); err != nil {
		t.Fatal(err)
	}
	// Asleep, she listens to what is queued, with the sleeper.
	tiers := map[string]bool{}
	takeNotes := r.fake.Handlers["podcast-notes"]
	r.fake.Handlers["podcast-notes"] = func(q llm.Request) (string, error) {
		tiers[q.Tier] = true
		return takeNotes(q)
	}
	r.b.listenAsleep(context.Background())
	if v := r.b.Podcasts(); len(v.Done) != 2 || len(v.Queued) != 0 {
		t.Fatalf("she should listen to the queued episode in her sleep: done=%d queued=%d", len(v.Done), len(v.Queued))
	}
	if len(tiers) != 1 || !tiers["sleeper"] {
		t.Fatalf("asleep, only the sleeper takes notes: %v", tiers)
	}
}

func TestOlderEpisodeOnlyWhenAskedFor(t *testing.T) {
	r := newRig(t)
	feed, _ := r.b.S.AddFeed("https://example.org/feed", r.now)
	_, eps, _ := parseFeed(strings.NewReader(testFeed))
	r.b.PodDir = t.TempDir()
	r.b.applyFeed(context.Background(), &feed, feed, eps, true)
	if q, _ := r.b.S.Episodes(0, 10, "queued"); len(q) != 1 || q[0].Title != "Newest episode" {
		t.Fatalf("only the newest should be queued when a show is added: %+v", q)
	}
	res, err := r.b.listenToOlder("the older one")
	if err != nil || res.(map[string]any)["episode"] != "Older episode" {
		t.Fatalf("asking for the older episode should queue it: %v %v", res, err)
	}
	if _, err := r.b.listenToOlder("quantum gardening"); err == nil {
		t.Error("an episode that does not exist cannot be queued")
	}
}

func TestUnqueueSendsAnEpisodeBackToWaiting(t *testing.T) {
	r := newRig(t)
	feed, _ := r.b.S.AddFeed("https://example.org/feed", r.now)
	_, eps, _ := parseFeed(strings.NewReader(testFeed))
	r.b.PodDir = t.TempDir()
	r.b.applyFeed(context.Background(), &feed, feed, eps, true)
	if v := r.b.Podcasts(); v.Unheard[feed.ID] != 1 || len(v.Queued) != 1 {
		t.Fatalf("one episode queued, the other unheard: %+v", v)
	}
	queued, _ := r.b.S.Episodes(0, 10, "queued")
	if err := r.b.UnqueueEpisode(queued[0].ID); err != nil {
		t.Fatal(err)
	}
	if v := r.b.Podcasts(); v.Unheard[feed.ID] != 2 || len(v.Queued) != 0 {
		t.Fatalf("an unqueued episode waits again: %+v", v)
	}
	if err := r.b.UnqueueEpisode(queued[0].ID); err == nil {
		t.Error("only a queued episode can be taken out of the queue")
	}
}
