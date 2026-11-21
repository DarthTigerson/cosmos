// Package voice gives her a voice. It runs Kokoro, a small speech model,
// in a helper process on this computer and asks it for sound.
package voice

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

//go:embed voice_server.py
var server []byte

// Voice is one voice she can speak with.
type Voice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Voices are the English voices worth offering, by how they sound.
var Voices = []Voice{
	{"af_heart", "Heart · warm, American"},
	{"af_bella", "Bella · bright, American"},
	{"af_nicole", "Nicole · soft and close, American"},
	{"af_sarah", "Sarah · even, American"},
	{"af_aoede", "Aoede · clear, American"},
	{"af_kore", "Kore · steady, American"},
	{"af_nova", "Nova · light, American"},
	{"af_sky", "Sky · young, American"},
	{"bf_emma", "Emma · warm, British"},
	{"bf_isabella", "Isabella · clear, British"},
	{"bf_lily", "Lily · gentle, British"},
	{"bf_alice", "Alice · bright, British"},
}

const Default = "af_heart"

// Manager starts the helper when she first speaks and keeps it running.
type Manager struct {
	// Dir holds the helper's Python environment (.venv) and its script.
	Dir string
	// ModelDir holds the Kokoro model files.
	ModelDir string

	mu   sync.Mutex
	cmd  *exec.Cmd
	port int
}

func (m *Manager) python() string { return filepath.Join(m.Dir, ".venv", "bin", "python") }

func (m *Manager) files() (model, voices string) {
	voices = filepath.Join(m.ModelDir, "voices-v1.0.bin")
	// Whichever size of the model is there; the smaller ones first.
	for _, name := range []string{"kokoro-v1.0.fp16.onnx", "kokoro-v1.0.int8.onnx", "kokoro-v1.0.onnx"} {
		if _, err := os.Stat(filepath.Join(m.ModelDir, name)); err == nil {
			return filepath.Join(m.ModelDir, name), voices
		}
	}
	return "", voices
}

// Missing says what her voice still needs, or "" when it can speak.
func (m *Manager) Missing() string {
	if _, err := os.Stat(m.python()); err != nil {
		return "the voice helper is not installed"
	}
	model, voices := m.files()
	if model == "" {
		return "the voice model is not in " + m.ModelDir
	}
	if _, err := os.Stat(voices); err != nil {
		return "the voices file is not in " + m.ModelDir
	}
	return ""
}

func (m *Manager) url() string { return "http://127.0.0.1:" + strconv.Itoa(m.port) }

func (m *Manager) alive(ctx context.Context) bool {
	if m.port == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, m.url(), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// ensure makes sure the helper is running, starting it if need be.
func (m *Manager) ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alive(ctx) {
		return nil
	}
	if missing := m.Missing(); missing != "" {
		return errors.New(missing)
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	script := filepath.Join(m.Dir, "voice_server.py")
	if err := os.WriteFile(script, server, 0o600); err != nil {
		return err
	}
	// Any free port: nothing else needs to know it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	m.port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	model, voices := m.files()
	args := []string{script, strconv.Itoa(m.port), model, voices}
	// The timing-aware model, when it is there, says when each sound is
	// spoken. Her mouth follows that.
	if timed := filepath.Join(m.ModelDir, "kokoro-timestamped.fp16.onnx"); exists(timed) {
		args = append(args, timed)
	}
	cmd := exec.Command(m.python(), args...)
	var problems bytes.Buffer
	cmd.Stderr = &problems
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("the voice helper would not start: %w", err)
	}
	m.cmd = cmd
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if m.alive(ctx) {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("the voice helper stopped: %s", lastLine(problems.String()))
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return errors.New("the voice helper did not become ready")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func lastLine(s string) string {
	lines := bytes.Split(bytes.TrimSpace([]byte(s)), []byte("\n"))
	if len(lines) == 0 {
		return "no reason given"
	}
	return string(lines[len(lines)-1])
}

// Spoken is one piece of speech: the sound, and when each speech sound
// in it starts and how long it lasts.
type Spoken struct {
	// Audio is a WAV file, base64-encoded.
	Audio string `json:"audio"`
	// Marks are [start seconds, length seconds, sound] for each sound,
	// in the phonetic alphabet. Empty when the voice gives no timing.
	Marks []json.RawMessage `json:"marks"`
}

// Speak returns the text spoken in the given voice.
func (m *Manager) Speak(ctx context.Context, text, voice string, speed float64) (Spoken, error) {
	var out Spoken
	data, err := m.speak(ctx, text, voice, speed)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("the voice helper sent something unreadable: %w", err)
	}
	if out.Marks == nil {
		out.Marks = []json.RawMessage{}
	}
	return out, nil
}

func (m *Manager) speak(ctx context.Context, text, voice string, speed float64) ([]byte, error) {
	if err := m.ensure(ctx); err != nil {
		return nil, err
	}
	if voice == "" {
		voice = Default
	}
	if speed <= 0 {
		speed = 1
	}
	body, _ := json.Marshal(map[string]any{"text": text, "voice": voice, "speed": speed})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the voice helper is not answering: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		return nil, errors.New("the voice failed: " + e.Error)
	}
	return data, nil
}

// Stop shuts the helper down.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	m.cmd = nil
	m.port = 0
}
