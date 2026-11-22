// Command voice-check runs recorded clips through her ears, the way a
// call does: speech detection, writing out, her name, whose voice, and
// the gate. It reports what she would have answered that she should not
// have, what she missed, and how long each part took. It runs on this
// computer only, with the same models as the core.
//
//	cd core && go run ./cmd/voice-check          # run the clips
//	cd core && go run ./cmd/voice-check -make    # make missing clips first
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/voice"
)

type clip struct {
	File  string `json:"file"`
	From  string `json:"from"` // you, her, other, tv, noise
	Voice string `json:"voice"`
	Text  string `json:"text"`
	Noise string `json:"noise"`
	// ForHer: she should answer it.
	ForHer bool `json:"for_her"`
}

type manifest struct {
	Enrol map[string]clip `json:"enrol"`
	Clips []clip          `json:"clips"`
}

func main() {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Library", "Application Support", "Cosmos")
	dir := flag.String("clips", "testdata/voice", "folder with clips.json and the clips")
	data := flag.String("data", dataDir, "her data folder, for the config and her voice's environment")
	makeMissing := flag.Bool("make", false, "make the clips that are missing, with her voice model")
	verbose := flag.Bool("v", false, "say what was heard in each part of each clip")
	out := flag.String("out", "", "also write the report here (default <clips>/results.txt)")
	flag.Parse()

	cfg := config.Default()
	if raw, err := os.ReadFile(filepath.Join(*data, "config.json")); err == nil {
		json.Unmarshal(raw, &cfg)
	}
	var m manifest
	raw, err := os.ReadFile(filepath.Join(*dir, "clips.json"))
	if err != nil || json.Unmarshal(raw, &m) != nil {
		fatal("read %s/clips.json: %v", *dir, err)
	}
	for k, c := range m.Enrol {
		c.Text = strings.ReplaceAll(c.Text, "{name}", cfg.CompanionName)
		m.Enrol[k] = c
	}
	for i := range m.Clips {
		m.Clips[i].Text = strings.ReplaceAll(m.Clips[i].Text, "{name}", cfg.CompanionName)
	}
	voiceDir := filepath.Join(*data, "voice")
	ctx := context.Background()
	if *makeMissing {
		makeClips(ctx, voiceDir, cfg, *dir, m)
	}

	hearing := &voice.Hearing{}
	defer hearing.Stop()
	if miss := hearing.Missing(cfg.HearModel); miss != "" {
		fatal("cannot write out speech: %s", miss)
	}
	if err := hearing.Ready(ctx, cfg.HearModel, cfg.HearLanguage); err != nil {
		fatal("whisper: %v", err)
	}
	senses := voice.DefaultSenses(voiceDir)
	defer senses.Stop()
	prints := map[string][]float32{}
	if senses.CanHearVoices() {
		for who, c := range m.Enrol {
			pcm, err := load(filepath.Join(*dir, c.File))
			if err != nil {
				fmt.Fprintf(os.Stderr, "no %s to learn %s's voice from: %v\n", c.File, who, err)
				continue
			}
			if v, err := senses.Voiceprint(ctx, voice.WAV(pcm)); err == nil {
				prints[who] = v
			}
		}
	}
	det := voice.OpenDetector(voiceDir)
	defer det.Close()
	pause := time.Duration(cfg.HearPause * float64(time.Second))
	gate := voice.Gate{ByName: true, AnswerOthers: cfg.AnswerOthers}
	prompt := voice.HearingPrompt(cfg.UserName, cfg.CompanionName, cfg.Location)

	var report strings.Builder
	fmt.Fprintf(&report, "Voice check, %s · detector %s · pause %.1f s · name %q · voices known: %d\n\n", time.Now().Format("2006-01-02 15:04"), det.Kind(), pause.Seconds(), cfg.CompanionName, len(prints))
	tw := tabwriter.NewWriter(&report, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "clip\tfrom\twant\tgot\theard\tname\twho\tvad ms\twrite ms\tprint ms\t")
	var misses, falses, ok int
	var vadTotal, writeTotal, printTotal time.Duration
	var audio float64
	for _, c := range m.Clips {
		pcm, err := load(filepath.Join(*dir, c.File))
		if err != nil {
			fmt.Fprintf(tw, "%s\t%s\t-\tmissing\t%v\t\t\t\t\t\t\n", c.File, c.From, err)
			continue
		}
		audio += float64(len(pcm)) / voice.Rate
		// A moment of quiet either side, as in a room.
		padded := append(append(make([]int16, voice.Rate*8/10), pcm...), make([]int16, voice.Rate*2)...)
		seg := &voice.Segmenter{Pause: pause}
		var said [][]int16
		start := time.Now()
		for i := 0; i+voice.Frame <= len(padded); i += voice.Frame {
			f := padded[i : i+voice.Frame]
			p, err := det.Speech(f)
			if err != nil {
				fatal("detector: %v", err)
			}
			if h, s := seg.Push(f, p, false); h == voice.Finished {
				said = append(said, s)
			}
		}
		vad := time.Since(start)
		vadTotal += vad
		var words []string
		addressed, who := false, ""
		var writeMs, printMs time.Duration
		answered := false
		for _, s := range said {
			wav := voice.WAV(s)
			t := time.Now()
			text, named, err := hearing.HearFor(ctx, wav, cfg.HearModel, cfg.HearLanguage, prompt, cfg.CompanionName, cfg.NameSoundsLike, true)
			writeMs += time.Since(t)
			if err != nil {
				fatal("whisper: %v", err)
			}
			t = time.Now()
			w := whose(ctx, senses, prints, wav, cfg.VoiceSure)
			printMs += time.Since(t)
			words = append(words, text)
			if *verbose {
				fmt.Fprintf(os.Stderr, "%s: %.2f s → %q name=%v who=%s\n", c.File, float64(len(s))/voice.Rate, text, named, dash(w))
			}
			addressed = addressed || named
			if w != "" {
				who = w
			}
			if gate.Decide(voice.Said{Words: text, Addressed: named, Who: w}) == voice.Answer {
				answered = true
			}
		}
		writeTotal += writeMs
		printTotal += printMs
		verdict := "ok"
		switch {
		case c.ForHer && !answered:
			verdict, misses = "MISSED", misses+1
		case !c.ForHer && answered:
			verdict, falses = "FALSE", falses+1
		default:
			ok++
		}
		want := "leave"
		if c.ForHer {
			want = "answer"
		}
		heard := strings.Join(words, " / ")
		if len(said) == 0 {
			heard = "(no speech)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%v\t%s\t%d\t%d\t%d\t\n", c.File, c.From, want, verdict, clipText(heard, 48), addressed, dash(who),
			vad.Milliseconds(), writeMs.Milliseconds(), printMs.Milliseconds())
	}
	tw.Flush()
	fmt.Fprintf(&report, "\n%d right, %d missed, %d false triggers, of %d clips.\n", ok, misses, falses, ok+misses+falses)
	if audio > 0 {
		fmt.Fprintf(&report, "Detector: %.1f ms per second of sound. Writing out: %d ms in all. Voiceprints: %d ms in all.\n",
			float64(vadTotal.Milliseconds())/audio, writeTotal.Milliseconds(), printTotal.Milliseconds())
	}
	fmt.Print(report.String())
	if *out == "" {
		*out = filepath.Join(*dir, "results.txt")
	}
	os.WriteFile(*out, []byte(report.String()), 0o600)
	if misses+falses > 0 {
		os.Exit(1)
	}
}

// whose is the voice, as the core decides it: the closest known print,
// if close enough, or someone. Empty when no voices are known.
func whose(ctx context.Context, s *voice.Senses, prints map[string][]float32, wav []byte, sure float64) string {
	if len(prints) == 0 {
		return ""
	}
	v, err := s.Voiceprint(ctx, wav)
	if err != nil {
		return ""
	}
	best, score := "", 0.0
	for who, p := range prints {
		if sim := voice.Similarity(v, p); sim > score {
			best, score = who, sim
		}
	}
	if score >= sure {
		return best
	}
	return "someone"
}

func load(path string) ([]int16, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return voice.PCM(raw)
}

// makeClips makes the clips that are not there: voices with Kokoro,
// noises by hand.
func makeClips(ctx context.Context, voiceDir string, cfg config.Config, dir string, m manifest) {
	home, _ := os.UserHomeDir()
	speaker := &voice.Manager{Dir: voiceDir, ModelDir: filepath.Join(home, "models", "kokoro")}
	defer speaker.Stop()
	all := append([]clip{}, m.Clips...)
	for _, c := range m.Enrol {
		all = append(all, c)
	}
	for _, c := range all {
		path := filepath.Join(dir, c.File)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		var pcm []int16
		if c.Noise != "" {
			pcm = noise(c.Noise)
		} else {
			which := c.Voice
			if which == "" {
				which = cfg.Voice // her own voice
			}
			out, err := speaker.Speak(ctx, c.Text, which, 1)
			if err != nil {
				fatal("make %s: %v", c.File, err)
			}
			wav, err := base64.StdEncoding.DecodeString(out.Audio)
			if err != nil {
				fatal("make %s: %v", c.File, err)
			}
			if pcm, err = voice.PCM(wav); err != nil {
				fatal("make %s: %v", c.File, err)
			}
		}
		if err := os.WriteFile(path, voice.WAV(pcm), 0o600); err != nil {
			fatal("write %s: %v", path, err)
		}
		fmt.Fprintf(os.Stderr, "made %s\n", c.File)
	}
}

// noise is a sound that is not speech: a knock, a hiss, or quiet.
func noise(kind string) []int16 {
	pcm := make([]int16, voice.Rate*2)
	r := rand.New(rand.NewPCG(1, 2))
	switch kind {
	case "knock":
		// Two short thuds.
		for _, at := range []int{voice.Rate / 2, voice.Rate * 3 / 4} {
			for i := 0; i < voice.Rate/25; i++ {
				decay := math.Exp(-float64(i) / 120)
				pcm[at+i] = int16(20000 * decay * math.Sin(float64(i)*2*math.Pi*90/voice.Rate) * (0.7 + 0.3*r.Float64()))
			}
		}
	case "hiss":
		for i := range pcm {
			pcm[i] = int16(r.NormFloat64() * 900)
		}
	}
	return pcm
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "voice-check: "+f+"\n", a...)
	os.Exit(2)
}
