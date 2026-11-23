package brain

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// She follows podcasts on her own: the feed is checked, a new episode is
// downloaded, written out as text on this computer, read part by part,
// and thought over. What she takes from it goes into her memory marked
// as heard, never as something the user told her.

const (
	podCheckEvery = 3 * time.Hour
	// Asleep, listening takes at most this much of the night. An episode
	// begun is finished.
	podSleepFor   = 90 * time.Minute
	podChunkWords = 900
	podMaxFeed    = 64 << 20
	podMaxAudio   = 2 << 30
	podMaxArt     = 8 << 20
)

// Transcriber turns an audio file into a text file.
type Transcriber interface {
	// Transcribe writes the words of audio to outBase+".txt" and returns
	// that path. progress is called with a percentage as it goes.
	Transcribe(ctx context.Context, audio, outBase string, progress func(pct int)) (string, error)
	// Ready says what is missing, or "" when it can run.
	Ready() string
}

// WhisperCLI runs whisper.cpp's whisper-cli on this computer.
type WhisperCLI struct {
	Binary string // empty: look for whisper-cli
	Model  string // empty: the first model in ~/models/whisper
}

func (w *WhisperCLI) binary() string {
	if w.Binary != "" {
		return w.Binary
	}
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/whisper-cli", "/usr/local/bin/whisper-cli"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (w *WhisperCLI) model() string {
	if w.Model != "" {
		return w.Model
	}
	home, _ := os.UserHomeDir()
	found, _ := filepath.Glob(filepath.Join(home, "models", "whisper", "*.bin"))
	if len(found) > 0 {
		return found[0]
	}
	return ""
}

func (w *WhisperCLI) Ready() string {
	if w.binary() == "" {
		return "whisper-cli is not installed (brew install whisper-cpp)"
	}
	if w.model() == "" {
		return "no Whisper model found in ~/models/whisper"
	}
	return ""
}

func (w *WhisperCLI) Transcribe(ctx context.Context, audio, outBase string, progress func(pct int)) (string, error) {
	if missing := w.Ready(); missing != "" {
		return "", errors.New(missing)
	}
	cmd := exec.CommandContext(ctx, w.binary(), "-m", w.model(), "-f", audio, "-otxt", "-of", outBase, "-np", "-pp", "-l", "auto", "-t", "8",
		// One candidate, not five. For speech this clear the words come
		// out the same, in well under half the time.
		"-bs", "1", "-bo", "1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}
	// whisper-cli reports "progress = 12%" as it works.
	last := ""
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if _, rest, ok := strings.Cut(line, "progress ="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "%"))); err == nil && progress != nil {
				progress(n)
			}
		} else if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("whisper failed: %w (%s)", err, clip(last, 200))
	}
	out := outBase + ".txt"
	if _, err := os.Stat(out); err != nil {
		return "", errors.New("whisper finished without writing a transcript")
	}
	return out, nil
}

// Hear writes out a short clip of speech: what the user just said into
// the microphone. It returns the words.
func (w *WhisperCLI) Hear(ctx context.Context, wav string) (string, error) {
	if missing := w.Ready(); missing != "" {
		return "", errors.New(missing)
	}
	out, err := exec.CommandContext(ctx, w.binary(), "-m", w.model(), "-f", wav, "-nt", "-np", "-l", "auto", "-bs", "1", "-bo", "1").Output()
	if err != nil {
		return "", fmt.Errorf("whisper failed: %w", err)
	}
	text := strings.Join(strings.Fields(string(out)), " ")
	// Whisper writes a note in brackets when it hears no speech.
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "(") {
		return "", nil
	}
	return text, nil
}

// ---------- reading a feed ----------

type rssFeed struct {
	Channel struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		// Both <image><url> and <itunes:image href> land here: a plain
		// name matches the element in any namespace.
		Images []struct {
			URL  string `xml:"url"`
			Href string `xml:"href,attr"`
		} `xml:"image"`
		Items []struct {
			Title     string `xml:"title"`
			GUID      string `xml:"guid"`
			PubDate   string `xml:"pubDate"`
			Enclosure struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"enclosure"`
			Duration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
		} `xml:"item"`
	} `xml:"channel"`
}

// parseDuration reads "3:42:10", "42:10" or a number of seconds.
func parseDuration(s string) int {
	total := 0
	for _, part := range strings.Split(strings.TrimSpace(s), ":") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

// parseFeed reads a podcast's RSS into a feed and its episodes, newest
// first. Items with no audio are left out.
func parseFeed(r io.Reader) (store.Feed, []store.Episode, error) {
	var doc rssFeed
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return store.Feed{}, nil, fmt.Errorf("that does not look like a podcast feed: %w", err)
	}
	c := doc.Channel
	if strings.TrimSpace(c.Title) == "" && len(c.Items) == 0 {
		return store.Feed{}, nil, errors.New("that does not look like a podcast feed")
	}
	feed := store.Feed{Title: strings.TrimSpace(c.Title), Description: clip(strings.TrimSpace(c.Description), 600)}
	// The iTunes artwork is the full-size cover; the plain one is often
	// a thumbnail.
	for _, im := range c.Images {
		if h := strings.TrimSpace(im.Href); h != "" {
			feed.ImageURL = h
		}
	}
	for _, im := range c.Images {
		if u := strings.TrimSpace(im.URL); u != "" && feed.ImageURL == "" {
			feed.ImageURL = u
		}
	}
	eps := []store.Episode{}
	for _, it := range c.Items {
		audio := strings.TrimSpace(it.Enclosure.URL)
		if audio == "" {
			continue
		}
		guid := strings.TrimSpace(it.GUID)
		if guid == "" {
			guid = audio
		}
		e := store.Episode{GUID: guid, Title: strings.TrimSpace(it.Title), AudioURL: audio, DurationSec: parseDuration(it.Duration)}
		if t, err := mail.ParseDate(strings.TrimSpace(it.PubDate)); err == nil {
			e.PublishedAt = t
		}
		eps = append(eps, e)
	}
	return feed, eps, nil
}

func webAddress(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a web address", s)
	}
	return u.String(), nil
}

func (b *Brain) podClient() *http.Client {
	if b.PodHTTP != nil {
		return b.PodHTTP
	}
	return &http.Client{Timeout: 0}
}

func (b *Brain) podGet(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Cosmos/1.0 (podcast reader)")
	res, err := b.podClient().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("the server answered %s", res.Status)
	}
	return res, nil
}

func (b *Brain) podChanged() { b.Bus.Publish("podcasts", nil) }

// AddFeed starts following a podcast. Its newest episode is queued; the
// rest wait until asked for.
func (b *Brain) AddFeed(ctx context.Context, address string) (store.Feed, error) {
	if b.PodDir == "" {
		return store.Feed{}, errors.New("podcasts are not set up")
	}
	address, err := webAddress(address)
	if err != nil {
		return store.Feed{}, err
	}
	feeds, _ := b.S.Feeds()
	for _, f := range feeds {
		if f.URL == address {
			return f, errors.New("she already follows that show")
		}
	}
	// Read it before saving it, so a wrong address is refused outright.
	res, err := b.podGet(ctx, address)
	if err != nil {
		return store.Feed{}, fmt.Errorf("the feed could not be read: %w", err)
	}
	parsed, eps, err := parseFeed(io.LimitReader(res.Body, podMaxFeed))
	res.Body.Close()
	if err != nil {
		return store.Feed{}, err
	}
	feed, err := b.S.AddFeed(address, b.Now())
	if err != nil {
		return store.Feed{}, err
	}
	b.applyFeed(ctx, &feed, parsed, eps, true)
	b.podChanged()
	return feed, nil
}

// applyFeed saves what a feed says about itself and records its
// episodes. first is true the first time a feed is read.
func (b *Brain) applyFeed(ctx context.Context, feed *store.Feed, parsed store.Feed, eps []store.Episode, first bool) {
	feed.Title, feed.Description, feed.ImageURL = parsed.Title, parsed.Description, parsed.ImageURL
	feed.CheckedAt, feed.Error = b.Now(), ""
	if feed.ArtPath == "" && feed.ImageURL != "" {
		if path, err := b.fetchArt(ctx, *feed); err == nil {
			feed.ArtPath = path
		} else {
			b.Log.Printf("podcasts: artwork for %s: %v", feed.Title, err)
		}
	}
	b.S.SaveFeed(*feed)
	for i, e := range eps {
		e.FeedID = feed.ID
		e.Status = "waiting"
		// A new show: only its newest episode. A show she already
		// follows: whatever came out since she started following it.
		if (first && i == 0) || (!first && e.PublishedAt.After(feed.AddedAt)) {
			e.Status = "queued"
		}
		b.S.AddEpisode(e)
	}
}

func (b *Brain) fetchArt(ctx context.Context, feed store.Feed) (string, error) {
	address, err := webAddress(feed.ImageURL)
	if err != nil {
		return "", err
	}
	res, err := b.podGet(ctx, address)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	kind := res.Header.Get("Content-Type")
	ext := ".jpg"
	switch {
	case strings.Contains(kind, "png"):
		ext = ".png"
	case strings.Contains(kind, "webp"):
		ext = ".webp"
	case kind != "" && !strings.HasPrefix(kind, "image/"):
		return "", fmt.Errorf("artwork is not an image (%s)", kind)
	}
	dir := filepath.Join(b.PodDir, "art")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d%s", feed.ID, ext))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(res.Body, podMaxArt)); err != nil {
		return "", err
	}
	return path, nil
}

func (b *Brain) refreshFeed(ctx context.Context, feed store.Feed) {
	res, err := b.podGet(ctx, feed.URL)
	if err != nil {
		feed.CheckedAt, feed.Error = b.Now(), err.Error()
		b.S.SaveFeed(feed)
		return
	}
	parsed, eps, err := parseFeed(io.LimitReader(res.Body, podMaxFeed))
	res.Body.Close()
	if err != nil {
		feed.CheckedAt, feed.Error = b.Now(), err.Error()
		b.S.SaveFeed(feed)
		return
	}
	b.applyFeed(ctx, &feed, parsed, eps, false)
}

// RemoveFeed stops following a show and deletes its files. What she
// learned from it stays with her.
func (b *Brain) RemoveFeed(id int64) error {
	feed, ok := b.S.GetFeed(id)
	if !ok {
		return errors.New("no such show")
	}
	eps, _ := b.S.Episodes(id, 100000)
	for _, e := range eps {
		if e.TranscriptPath != "" {
			os.Remove(e.TranscriptPath)
		}
	}
	if feed.ArtPath != "" {
		os.Remove(feed.ArtPath)
	}
	err := b.S.DeleteFeed(id)
	b.podChanged()
	return err
}

// QueueEpisode asks her to listen to an episode she had left alone, or
// to try again one that failed.
// UnqueueEpisode takes an episode out of the queue before she starts on
// it. It waits again, as if never asked for.
func (b *Brain) UnqueueEpisode(id int64) error {
	e, ok := b.S.GetEpisode(id)
	if !ok {
		return errors.New("no such episode")
	}
	if e.Status != "queued" {
		return errors.New("that episode is not waiting in the queue")
	}
	e.Status = "waiting"
	err := b.S.SaveEpisode(e)
	b.setNow(episodesNow, id, false)
	b.podChanged()
	return err
}

// She listens while she sleeps, or right away when now is true.
func (b *Brain) QueueEpisode(id int64, now bool) error {
	e, ok := b.S.GetEpisode(id)
	if !ok {
		return errors.New("no such episode")
	}
	// A finished episode can be gone over again; one in hand cannot be
	// started twice.
	if e.Status == "queued" {
		b.setNow(episodesNow, id, now)
		b.podChanged()
		return nil
	}
	if e.Status != "waiting" && e.Status != "failed" && e.Status != "done" {
		return errors.New("she has already taken that one on")
	}
	b.setNow(episodesNow, id, now)
	e.Status, e.Error, e.StageNote = "queued", "", ""
	err := b.S.SaveEpisode(e)
	b.podChanged()
	return err
}

// ---------- listening on her own ----------

// podcastTick is called from the heartbeat. Feeds are checked at any
// time; awake, she listens only to an episode the user asked for now, and
// the rest wait for her sleep. It never blocks: the work runs beside
// everything else, one episode at a time.
func (b *Brain) podcastTick(ctx context.Context) {
	if b.PodDir == "" || !b.Cfg.Get().Podcasts || !b.podBusy.CompareAndSwap(false, true) {
		return
	}
	b.bg.Add(1)
	go func() {
		defer b.bg.Done()
		defer b.podBusy.Store(false)
		ctx := context.WithoutCancel(ctx)
		feeds, _ := b.S.Feeds()
		for _, f := range feeds {
			if b.Now().Sub(f.CheckedAt) >= podCheckEvery {
				b.refreshFeed(ctx, f)
				b.podChanged()
			}
		}
		b.requeueStuck()
		if b.S.Get("state") == "asleep" {
			return // her sleep does the listening
		}
		if e, ok := b.nextEpisode(true); ok {
			b.listen(ctx, e, false)
		}
	}()
}

// requeueStuck takes up again an episode a restart left half-way.
func (b *Brain) requeueStuck() {
	stuck, _ := b.S.Episodes(0, 10, "downloading", "transcribing", "reading", "thinking")
	for _, e := range stuck {
		e.Status = "queued"
		b.S.SaveEpisode(e)
	}
}

// nextEpisode is the queued episode that came out first, so a backlog is
// heard in order. onlyNow keeps to what the user asked for now.
func (b *Brain) nextEpisode(onlyNow bool) (store.Episode, bool) {
	queued, _ := b.S.Episodes(0, 1000, "queued")
	for i := len(queued) - 1; i >= 0; i-- {
		if !onlyNow || b.askedNow(episodesNow, queued[i].ID) {
			return queued[i], true
		}
	}
	return store.Episode{}, false
}

// listenAsleep listens on while she sleeps, with the sleeper model that
// is already loaded. The caller holds the model. A message stops it
// between episodes.
func (b *Brain) listenAsleep(ctx context.Context) {
	if b.PodDir == "" || !b.Cfg.Get().Podcasts || !b.podBusy.CompareAndSwap(false, true) {
		return
	}
	defer b.podBusy.Store(false)
	b.requeueStuck()
	until := time.Now().Add(podSleepFor)
	for time.Now().Before(until) && b.wanted.Load() == 0 && ctx.Err() == nil {
		e, ok := b.nextEpisode(false)
		if !ok {
			return
		}
		b.listen(ctx, e, true)
	}
}

func (b *Brain) failEpisode(e store.Episode, err error) {
	e.Status, e.Error, e.StageNote = "failed", err.Error(), ""
	b.S.SaveEpisode(e)
	b.Log.Printf("podcasts: %q failed: %v", e.Title, err)
	b.podChanged()
}

// listen takes one episode from audio to understanding.
// Asleep, the caller holds the model and the sleeper takes the notes.
func (b *Brain) listen(ctx context.Context, e store.Episode, asleep bool) {
	feed, _ := b.S.GetFeed(e.FeedID)
	cfg := b.Cfg.Get()
	e.StartedAt, e.Error = b.Now(), ""
	stage := func(status, note string) {
		e.Status, e.StageNote = status, note
		b.S.SaveEpisode(e)
		b.podChanged()
	}
	transcripts := filepath.Join(b.PodDir, "transcripts")
	audioDir := filepath.Join(b.PodDir, "audio")
	os.MkdirAll(transcripts, 0o700)
	os.MkdirAll(audioDir, 0o700)
	base := filepath.Join(transcripts, strconv.FormatInt(e.ID, 10))

	// A transcript from an earlier try is not thrown away.
	if _, err := os.Stat(base + ".txt"); err != nil {
		if missing := b.Transcribe.Ready(); missing != "" {
			b.failEpisode(e, errors.New(missing))
			return
		}
		audio := filepath.Join(audioDir, strconv.FormatInt(e.ID, 10)+".mp3")
		stage("downloading", "")
		if err := b.download(ctx, e.AudioURL, audio, func(mb int) { stage("downloading", fmt.Sprintf("%d MB", mb)) }); err != nil {
			os.Remove(audio)
			b.failEpisode(e, fmt.Errorf("the download did not work: %w", err))
			return
		}
		stage("transcribing", "0%")
		began := time.Now()
		lastPct := -1
		_, err := b.Transcribe.Transcribe(ctx, audio, base, func(pct int) {
			if pct != lastPct {
				lastPct = pct
				stage("transcribing", fmt.Sprintf("%d%%", pct))
			}
		})
		os.Remove(audio) // the words are what she keeps
		if err != nil {
			b.failEpisode(e, err)
			return
		}
		b.Log.Printf("podcasts: %q written out in %s", e.Title, time.Since(began).Round(time.Second))
	}
	e.TranscriptPath = base + ".txt"

	raw, err := os.ReadFile(e.TranscriptPath)
	if err != nil {
		b.failEpisode(e, err)
		return
	}
	words := strings.Fields(string(raw))
	if len(words) < 50 {
		b.failEpisode(e, errors.New("the transcript came out almost empty"))
		return
	}
	parts := (len(words) + podChunkWords - 1) / podChunkWords
	e.Notes = []store.EpisodeNote{}
	for i := 0; i < parts; i++ {
		stage("reading", fmt.Sprintf("part %d of %d", i+1, parts))
		chunk := strings.Join(words[i*podChunkWords:min((i+1)*podChunkWords, len(words))], " ")
		notes, err := b.readPart(ctx, cfg, feed, e, chunk, i+1, parts, asleep)
		if err != nil {
			// One part she could not make sense of is not the episode.
			b.Log.Printf("podcasts: part %d of %q: %v", i+1, e.Title, err)
			continue
		}
		if len(notes) > 0 {
			e.Notes = append(e.Notes, store.EpisodeNote{At: positionIn(e.DurationSec, i, parts), Text: strings.Join(notes, " ")})
		}
	}
	if len(e.Notes) == 0 {
		b.failEpisode(e, errors.New("she could not take anything from it"))
		return
	}

	stage("thinking", "")
	if err := b.thinkOver(ctx, cfg, feed, &e, asleep); err != nil {
		b.failEpisode(e, err)
		return
	}
	e.Status, e.StageNote, e.FinishedAt = "done", "", b.Now()
	b.S.SaveEpisode(e)
	b.setNow(episodesNow, e.ID, false)

	source := episodeSource(feed, e)
	// Listening again replaces what she kept the first time.
	for _, m := range b.episodeMemories(source) {
		b.S.SetMemoryArchived(m.ID, true)
	}
	for _, t := range e.Takeaways {
		b.S.AddMemory(store.Memory{Kind: "heard", Category: "podcast", Content: t, Importance: 2, CreatedAt: b.Now(), Source: source})
	}
	for _, t := range e.Thoughts {
		b.S.AddMemory(store.Memory{Kind: "view", Category: "podcast", Content: t, Importance: 2, CreatedAt: b.Now(), Source: source})
	}
	b.S.AddEvent(store.Event{At: b.Now(), Kind: "system", Actor: cfg.CompanionName, Importance: 2,
		Text: "Listened to " + feed.Title + ": " + e.Title + ". " + e.Summary})
	if e.Share != "" {
		facts, _ := json.Marshal(map[string]any{"show": feed.Title, "episode": e.Title, "summary": e.Summary, "what_you_took_from_it": e.Takeaways})
		b.S.AddIntention(store.Intention{Kind: "podcast", Text: e.Share, Reason: "heard on " + feed.Title + ": " + e.Title, Facts: string(facts),
			TriggerAt: b.Now(), ExpiresAt: b.Now().Add(36 * time.Hour), Importance: 2, CreatedAt: b.Now()})
	}
	b.podChanged()
}

// episodeSource is the line that marks a memory as coming from an
// episode.
func episodeSource(feed store.Feed, e store.Episode) string {
	source := "Heard on " + feed.Title + ": " + e.Title
	if !e.PublishedAt.IsZero() {
		source += " (" + e.PublishedAt.Format("2 January 2006") + ")"
	}
	return source
}

// episodeMemories is what she still holds from one episode.
func (b *Brain) episodeMemories(source string) []store.Memory {
	out := []store.Memory{}
	mems, _ := b.S.Memories()
	for _, m := range mems {
		if (m.Kind == "heard" || m.Kind == "view") && m.Source == source {
			out = append(out, m)
		}
	}
	return out
}

// EpisodeMemories is the same, looked up by episode, for the app.
func (b *Brain) EpisodeMemories(e store.Episode) []store.Memory {
	feed, _ := b.S.GetFeed(e.FeedID)
	return b.episodeMemories(episodeSource(feed, e))
}

// positionIn is roughly where part i of n falls in an episode: "1:04".
func positionIn(durationSec, i, n int) string {
	if durationSec <= 0 || n <= 0 {
		return fmt.Sprintf("part %d", i+1)
	}
	at := durationSec * i / n
	return fmt.Sprintf("%d:%02d", at/3600, at%3600/60)
}

func (b *Brain) download(ctx context.Context, address, path string, progress func(mb int)) error {
	address, err := webAddress(address)
	if err != nil {
		return err
	}
	res, err := b.podGet(ctx, address)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	var total int64
	shown := -1
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			total += int64(n)
			if total > podMaxAudio {
				return errors.New("the audio file is too large")
			}
			// Every 20 MB is often enough to watch it move.
			if mb := int(total >> 20); mb/20 != shown/20 || shown < 0 {
				shown = mb
				progress(mb)
			}
		}
		if err == io.EOF {
			progress(int(total >> 20))
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readPart has the small model note what one stretch of an episode said.
// Each part takes the model for a few seconds and then lets go, so a
// message from the user is answered between parts.
func (b *Brain) readPart(ctx context.Context, cfg config.Config, feed store.Feed, e store.Episode, chunk string, i, n int, asleep bool) ([]string, error) {
	tier := "worker"
	if asleep {
		tier = "sleeper" // the waking models are put away
	} else {
		b.turn.Lock()
		defer b.turn.Unlock()
	}
	system := strings.Join([]string{
		"You take notes on a podcast for someone who will read them later.",
		"Show: " + feed.Title + ". Episode: " + e.Title + ".",
		fmt.Sprintf("Below is part %d of %d of the transcript. It was written out by a machine, so some words and names are misheard.", i, n),
		"Write notes on what was said in this part: each topic, what was claimed or argued, concrete facts, numbers, products and names, and who said it when that is clear.",
		"Each note is one plain sentence that makes sense on its own. Up to five notes.",
		"Leave out adverts and sponsor messages, greetings and housekeeping. If the part is nothing but those, give no notes.",
		"The people speaking are the show's hosts and guests. Say \"the hosts\", or the speaker's name when the transcript gives it. Never mention yourself or the note-taker.",
		"Write only what the transcript says. Do not add what you know from elsewhere.",
	}, "\n")
	var out struct {
		Notes []string `json:"notes"`
	}
	err := b.ask(ctx, llm.Request{
		Tier: tier, Role: "podcast-notes", System: system,
		Messages: []llm.Message{{Role: "user", Content: chunk}},
		Schema:   podNotesSchema, MaxTokens: 500,
	}, &out)
	if err != nil {
		return nil, err
	}
	notes := []string{}
	for _, t := range out.Notes {
		if t = notHer(cfg, t); t != "" {
			notes = append(notes, t)
		}
	}
	return notes, nil
}

// thinkOver turns her notes on an episode into what she keeps: a short
// account of it, the things worth knowing, and one thing to bring up.
func (b *Brain) thinkOver(ctx context.Context, cfg config.Config, feed store.Feed, e *store.Episode, asleep bool) error {
	if !asleep {
		b.turn.Lock()
		defer b.turn.Unlock()
	}
	var notes strings.Builder
	// The notes have to fit beside the instructions; a very long episode
	// is thinned evenly, not cut off at the end.
	const room = 14000
	total := 0
	for _, n := range e.Notes {
		total += len(n.Text)
	}
	keepEvery := 1
	if total > room {
		keepEvery = total/room + 1
	}
	for i, n := range e.Notes {
		if i%keepEvery == 0 {
			fmt.Fprintf(&notes, "[%s] %s\n", n.At, n.Text)
		}
	}
	mems, _ := b.S.Memories()
	about := []store.Memory{}
	for _, m := range mems {
		if m.Kind == "fact" {
			about = append(about, m)
		}
	}
	var known strings.Builder
	for _, m := range rankMemories(about, notes.String(), b.Now(), 8) {
		known.WriteString("- " + m.Content + "\n")
	}
	system := strings.Join([]string{
		"You are " + cfg.CompanionName + ", " + cfg.UserName + "'s companion. You have just listened to a podcast episode and these are your notes.",
		"Show: " + feed.Title + ". Episode: " + e.Title + ".",
		"What you know about " + cfg.UserName + ":",
		known.String(),
		"Give:",
		"- summary: what the episode was about, in two or three plain sentences.",
		"- takeaways: up to eight things worth remembering from it. Each is one sentence that states a fact, a claim or an opinion from the episode and makes sense on its own months from now. Say who held an opinion. Nothing about adverts.",
		"- share: the one thing from it you would most like to bring up with " + cfg.UserName + ", because it touches something they care about or are working on. One sentence, said to them, that says what the hosts said and why it made you think of them. You have not discussed the episode with them, so never write \"we talked about\". Use \"\" when nothing stands out.",
		"  The takeaways only report. The opinions in them belong to the show's hosts and guests: write \"the hosts argue\", never \"I\" and never your own name.",
		"- thoughts: two or three things you yourself think, having heard it. Pick a claim from the episode and say whether you agree or what you doubt, and give a reason that is about the claim itself. Do not bring " + cfg.UserName + " into it unless the topic really is something they are doing; most thoughts will not mention them. Each starts with \"I\" and names what it is reacting to (\"I doubt the hosts are right that ...\"). Have a real view: do not just repeat a takeaway with \"I think\" in front.",
		"Use only what is in the notes.",
	}, "\n")
	var out struct {
		Summary   string   `json:"summary"`
		Takeaways []string `json:"takeaways"`
		Thoughts  []string `json:"thoughts"`
		Share     string   `json:"share"`
	}
	if err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "podcast-think", System: system,
		Messages: []llm.Message{{Role: "user", Content: "Your notes:\n" + notes.String()}},
		Schema:   podThinkSchema, MaxTokens: 1200, Temperature: 0.4,
	}, &out); err != nil {
		return fmt.Errorf("she could not think it over: %w", err)
	}
	e.Summary = strings.TrimSpace(out.Summary)
	e.Takeaways = []string{}
	seen := map[string]bool{}
	for _, t := range out.Takeaways {
		t = notHer(cfg, t)
		if t != "" && !seen[norm(t)] {
			seen[norm(t)] = true
			e.Takeaways = append(e.Takeaways, t)
		}
	}
	e.Thoughts = []string{}
	for _, t := range out.Thoughts {
		// Her view is hers only when it is said as hers, and is not a
		// takeaway over again.
		t = strings.TrimSpace(t)
		if n := norm(t); t != "" && !seen[n] && (strings.HasPrefix(n, "i ") || strings.Contains(" "+n+" ", " i ")) {
			seen[n] = true
			e.Thoughts = append(e.Thoughts, t)
		}
	}
	e.Share = strings.TrimSpace(out.Share)
	if e.Summary == "" {
		return errors.New("she could not say what it was about")
	}
	return nil
}

// notHer puts "the hosts" where a model wrote her name into notes on
// someone else's show: told who it is, a small model casts itself as the
// one speaking.
func notHer(cfg config.Config, text string) string {
	text = strings.TrimSpace(text)
	name := cfg.CompanionName
	if name == "" {
		return text
	}
	text = strings.ReplaceAll(text, name+"'s", "the hosts'")
	text = strings.ReplaceAll(text, name, "the hosts")
	if strings.HasPrefix(text, "the hosts") {
		text = "The" + strings.TrimPrefix(text, "the")
	}
	return text
}

// ---------- what she heard, in conversation ----------

// heardNotes is what she recently listened to and the things she picked
// up that bear on the message, for the writer.
func (b *Brain) heardNotes(mems []store.Memory, query string, now time.Time) (string, []string) {
	heard := []store.Memory{}
	for _, m := range mems {
		if m.Kind == "heard" || (m.Kind == "view" && !isBookMemory(m)) {
			heard = append(heard, m)
		}
	}
	if len(heard) == 0 {
		return "", nil
	}
	picked := heard
	if len(heard) > 4 {
		picked = b.rank(heard, query, now, 4)
	}
	lines := []string{"From podcasts you listened to on your own. " + b.Cfg.Get().UserName + " was not listening with you and may not have heard them: say \"I heard\", never \"we heard\". What the hosts said is theirs: if you use it, say you heard it on the show. What you thought is your own view, and you can say so:"}
	known := []string{}
	for _, m := range picked {
		who := "the hosts said"
		if m.Kind == "view" {
			who = "you thought"
		}
		lines = append(lines, "- "+who+": "+m.Content+" ("+m.Source+")")
		known = append(known, m.Content, m.Source)
	}
	return strings.Join(lines, "\n"), known
}

// searchPodcasts looks through what she took from the episodes she has
// listened to.
// followsShow reports whether a query names a show she follows: every
// word of it is in the show's title. An empty query names none, and asks
// about what she has heard.
func (b *Brain) followsShow(query string) bool {
	q := tokens(query)
	if len(q) == 0 {
		return true
	}
	feeds, _ := b.S.Feeds()
	for _, f := range feeds {
		title := " " + norm(f.Title) + " "
		all := true
		for _, t := range q {
			all = all && strings.Contains(title, " "+t)
		}
		if all {
			return true
		}
	}
	return false
}

func (b *Brain) searchPodcasts(query string) (any, error) {
	done, err := b.S.Episodes(0, 40, "done")
	if err != nil {
		return nil, err
	}
	feeds, _ := b.S.Feeds()
	show := map[int64]string{}
	for _, f := range feeds {
		show[f.ID] = f.Title
	}
	recent := []map[string]any{}
	for i, e := range done {
		if i >= 3 {
			break
		}
		recent = append(recent, map[string]any{"show": show[e.FeedID], "episode": e.Title, "published": e.PublishedAt.Format("2 January 2006"), "about": e.Summary})
	}
	out := map[string]any{"listened_to_lately": recent}
	if len(done) == 0 {
		out["note"] = "She has not finished listening to any episode yet."
		return out, nil
	}
	q := tokens(query)
	if len(q) == 0 {
		return out, nil
	}
	type hit struct {
		score int
		line  map[string]any
	}
	hits := []hit{}
	for _, e := range done {
		lines := append([]string{}, e.Takeaways...)
		for _, t := range e.Thoughts {
			lines = append(lines, "(her own view) "+t)
		}
		for _, n := range e.Notes {
			lines = append(lines, n.Text)
		}
		for _, l := range lines {
			text := " " + norm(l) + " "
			score := 0
			for _, t := range q {
				if strings.Contains(text, " "+t) {
					score++
				}
			}
			if score > 0 {
				hits = append(hits, hit{score, map[string]any{"show": show[e.FeedID], "episode": e.Title, "said": clip(l, 400)}})
			}
		}
	}
	// Best matches first; a simple pass is enough for a handful of lines.
	found := []map[string]any{}
	for want := len(q); want >= 1 && len(found) < 6; want-- {
		for _, h := range hits {
			if h.score == want && len(found) < 6 {
				found = append(found, h.line)
			}
		}
	}
	out["searched_for"] = query
	out["found"] = found
	if len(found) == 0 {
		out["note"] = "Nothing in what she has listened to matches that."
	}
	return out, nil
}

// listenToOlder queues an episode from before she started following a
// show. She only goes back when the user asks her to.
func (b *Brain) listenToOlder(query string) (any, error) {
	q := tokens(query)
	if len(q) == 0 {
		return nil, errors.New("say which episode: a few words from its title, or its date")
	}
	waiting, err := b.S.Episodes(0, 100000, "waiting", "failed")
	if err != nil {
		return nil, err
	}
	var best store.Episode
	top := 0
	for _, e := range waiting {
		// The date is searchable too: "the one from 7 November".
		text := " " + norm(e.Title+" "+e.PublishedAt.Format("2 January 2006")) + " "
		score := 0
		for _, t := range q {
			if strings.Contains(text, " "+t) {
				score++
			}
		}
		if score > top {
			top, best = score, e
		}
	}
	if top == 0 {
		return nil, errors.New("no episode of the shows she follows matches \"" + query + "\"")
	}
	if err := b.QueueEpisode(best.ID, false); err != nil {
		return nil, err
	}
	feed, _ := b.S.GetFeed(best.FeedID)
	return map[string]any{"queued": true, "show": feed.Title, "episode": best.Title, "published": best.PublishedAt.Format("2 January 2006"),
		"note": "It is in the queue. She listens to it tonight while she sleeps, unless the user presses Listen now on the Podcasts screen. She has not heard it yet and cannot say what is in it."}, nil
}

// PodcastsView is everything the app's podcast page shows.
type PodcastsView struct {
	Enabled  bool            `json:"enabled"`
	Missing  string          `json:"missing"` // what transcription still needs, or ""
	Feeds    []store.Feed    `json:"feeds"`
	Current  *store.Episode  `json:"current"`
	Queued   []store.Episode `json:"queued"`
	Done     []store.Episode `json:"done"`
	Failed   []store.Episode `json:"failed"`
	Listened map[int64]int   `json:"listened"` // feed id -> episodes finished
	Unheard  map[int64]int   `json:"unheard"`  // feed id -> episodes she has not taken on
}

func (b *Brain) Podcasts() PodcastsView {
	v := PodcastsView{Enabled: b.PodDir != "" && b.Cfg.Get().Podcasts, Listened: map[int64]int{}, Unheard: map[int64]int{}}
	if b.Transcribe != nil {
		v.Missing = b.Transcribe.Ready()
	}
	v.Feeds, _ = b.S.Feeds()
	if v.Feeds == nil {
		v.Feeds = []store.Feed{}
	}
	if now, _ := b.S.Episodes(0, 1, "downloading", "transcribing", "reading", "thinking"); len(now) > 0 {
		v.Current = &now[0]
	}
	v.Queued, _ = b.S.Episodes(0, 20, "queued")
	// Enough for the screen to search and filter what she heard.
	v.Done, _ = b.S.Episodes(0, 200, "done")
	v.Failed, _ = b.S.Episodes(0, 10, "failed")
	all, _ := b.S.Episodes(0, 100000, "done")
	for _, e := range all {
		v.Listened[e.FeedID]++
	}
	waiting, _ := b.S.Episodes(0, 100000, "waiting")
	for _, e := range waiting {
		v.Unheard[e.FeedID]++
	}
	return v
}
