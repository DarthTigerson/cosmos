package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hearing turns what the user says into words. It keeps whisper.cpp's
// server running with the model loaded, so each thing said is written
// out in well under a second instead of loading the model every time.
type Hearing struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	port  int
	model string
	lang  string
	used  time.Time
}

// IdleFor is how long the server is kept with nothing heard before it
// is stopped, so the model does not sit in memory all day.
const IdleFor = 15 * time.Minute

// HearingBinary finds whisper-server, or "".
func HearingBinary() string {
	if p, err := exec.LookPath("whisper-server"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/whisper-server", "/usr/local/bin/whisper-server"} {
		if exists(p) {
			return p
		}
	}
	return ""
}

// HearingModel is the model to listen with: the one chosen, or else the
// first in ~/models/whisper.
func HearingModel(chosen string) string {
	if chosen != "" {
		return chosen
	}
	home, _ := os.UserHomeDir()
	found, _ := filepath.Glob(filepath.Join(home, "models", "whisper", "*.bin"))
	if len(found) > 0 {
		return found[0]
	}
	return ""
}

// Missing says what listening still needs, or "".
func (h *Hearing) Missing(model string) string {
	if HearingBinary() == "" {
		return "whisper-server is not installed (brew install whisper-cpp)"
	}
	if m := HearingModel(model); m == "" || !exists(m) {
		return "no Whisper model found in ~/models/whisper"
	}
	return ""
}

func (h *Hearing) url() string { return "http://127.0.0.1:" + strconv.Itoa(h.port) }

func (h *Hearing) alive(ctx context.Context) bool {
	if h.port == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.url()+"/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return true
}

// Ready starts the server if it is not running with this model and
// language. Calling it when a call begins means the first thing said is
// heard as quickly as the rest.
func (h *Hearing) Ready(ctx context.Context, model, lang string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready(ctx, model, lang)
}

func (h *Hearing) ready(ctx context.Context, model, lang string) error {
	model = HearingModel(model)
	if lang == "" {
		lang = "en"
	}
	h.used = time.Now()
	if h.cmd != nil && h.model == model && h.lang == lang {
		return nil
	}
	if missing := h.Missing(model); missing != "" {
		return errors.New(missing)
	}
	h.stop()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(HearingBinary(), "-m", model, "-l", lang, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-t", "8", "-bs", "1", "-bo", "1", "-nt", "-sns")
	var problems bytes.Buffer
	cmd.Stderr = &problems
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("whisper-server would not start: %w", err)
	}
	h.cmd, h.port, h.model, h.lang = cmd, port, model, lang
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
		// Stopped by itself: the next thing heard starts it again.
		h.mu.Lock()
		if h.cmd == cmd {
			h.cmd, h.port = nil, 0
		}
		h.mu.Unlock()
	}()
	go h.stopWhenIdle(cmd, exited)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if h.alive(ctx) {
			return nil
		}
		select {
		case <-exited:
			h.cmd, h.port = nil, 0
			return fmt.Errorf("whisper-server stopped: %s", lastLine(problems.String()))
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	h.stop()
	return errors.New("whisper-server did not become ready")
}

// Hear returns the words in a WAV recording. prompt is a line of the
// words likely to come up, her name above all, so that they are spelled
// right.
func (h *Hearing) Hear(ctx context.Context, wav []byte, model, lang, prompt string) (string, error) {
	h.mu.Lock()
	if err := h.ready(ctx, model, lang); err != nil {
		h.mu.Unlock()
		return "", err
	}
	url := h.url()
	h.mu.Unlock()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "heard.wav")
	part.Write(wav)
	form.WriteField("response_format", "json")
	form.WriteField("temperature", "0")
	if prompt != "" {
		form.WriteField("prompt", prompt)
	}
	form.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper-server is not answering: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	json.Unmarshal(raw, &out)
	if res.StatusCode != http.StatusOK || out.Error != "" {
		return "", fmt.Errorf("whisper failed: %s", strings.TrimSpace(out.Error+" "+string(raw)))
	}
	return Cleaned(out.Text), nil
}

// HearFor is Hear for something that may have been said to her: the
// words, and whether her name (or something like it) is in them.
// Primed with her name, Whisper sometimes leaves it out when it opens
// what was said, taking it for the prompt. Heard again unprimed, her
// name is caught; only talk not for her pays for the second listen,
// and only when her name matters (needName), not while she is already
// talking with someone.
func (h *Hearing) HearFor(ctx context.Context, wav []byte, model, lang, prompt, name string, also []string, needName bool) (string, bool, error) {
	text, err := h.Hear(ctx, wav, model, lang, prompt)
	if err != nil {
		return "", false, err
	}
	addressed := Addressed(text, name, also)
	if needName && !addressed && text != "" && prompt != "" {
		if plain, err := h.Hear(ctx, wav, model, lang, ""); err == nil && Addressed(plain, name, also) {
			addressed = true
		}
	}
	return text, addressed, nil
}

// HearingPrompt is the line Whisper is primed with: who is talking to
// whom, and where. Written as a sentence, since a bare list of names is
// left out of what follows it.
func HearingPrompt(user, companion, location string) string {
	p := user + " is talking with " + companion + " at home"
	if location != "" {
		p += " in " + location
	}
	return p + "."
}

// Cleaned tidies what Whisper wrote. A note in brackets is what it
// writes when it hears no speech.
func Cleaned(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "(") {
		return ""
	}
	return text
}

func (h *Hearing) stopWhenIdle(cmd *exec.Cmd, exited chan struct{}) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-exited:
			return
		case <-tick.C:
			h.mu.Lock()
			if h.cmd == cmd && time.Since(h.used) > IdleFor {
				h.stop()
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hearing) stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill()
	}
	h.cmd, h.port = nil, 0
}

// Stop shuts the server down.
func (h *Hearing) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stop()
}
