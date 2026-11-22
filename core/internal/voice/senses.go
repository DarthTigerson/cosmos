package voice

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

//go:embed senses_server.py
var sensesServer []byte

// Senses turns a face or a voice into a print: numbers that are close for
// the same person and far apart for different people. It runs a helper on
// this computer, in the same Python environment as her voice, and keeps
// nothing itself.
type Senses struct {
	// Dir holds the Python environment (.venv) shared with her voice.
	Dir string
	// FaceModel and VoiceModel are the model files; either may be missing.
	FaceModel  string
	VoiceModel string

	mu   sync.Mutex
	cmd  *exec.Cmd
	port int
}

// DefaultSenses finds the model files in ~/models.
func DefaultSenses(dir string) *Senses {
	home, _ := os.UserHomeDir()
	s := &Senses{Dir: dir, FaceModel: filepath.Join(home, "models", "sight", "w600k_mbf.onnx")}
	found, _ := filepath.Glob(filepath.Join(home, "models", "speaker", "*.onnx"))
	if len(found) > 0 {
		s.VoiceModel = found[0]
	}
	return s
}

func (s *Senses) python() string { return filepath.Join(s.Dir, ".venv", "bin", "python") }

// CanSeeFaces and CanHearVoices say whether each sense is set up.
func (s *Senses) CanSeeFaces() bool {
	return exists(s.python()) && s.FaceModel != "" && exists(s.FaceModel)
}
func (s *Senses) CanHearVoices() bool {
	return exists(s.python()) && s.VoiceModel != "" && exists(s.VoiceModel)
}

// Missing says what is not set up, for the settings screen.
func (s *Senses) Missing() []string {
	out := []string{}
	if !exists(s.python()) {
		out = append(out, "the helper's Python environment is not installed")
	}
	if s.FaceModel == "" || !exists(s.FaceModel) {
		out = append(out, "no face model in ~/models/sight (w600k_mbf.onnx)")
	}
	if s.VoiceModel == "" || !exists(s.VoiceModel) {
		out = append(out, "no speaker model in ~/models/speaker")
	}
	return out
}

func (s *Senses) url() string { return "http://127.0.0.1:" + strconv.Itoa(s.port) }

func (s *Senses) ensure(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.port != 0 {
		return s.url(), nil
	}
	if !s.CanSeeFaces() && !s.CanHearVoices() {
		return "", errors.New("neither faces nor voices are set up")
	}
	script := filepath.Join(s.Dir, "senses_server.py")
	if err := os.WriteFile(script, sensesServer, 0o600); err != nil {
		return "", err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	face, voice := "", ""
	if s.CanSeeFaces() {
		face = s.FaceModel
	}
	if s.CanHearVoices() {
		voice = s.VoiceModel
	}
	cmd := exec.Command(s.python(), "-I", script, strconv.Itoa(port), face, voice)
	var problems bytes.Buffer
	cmd.Stderr = &problems
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("the senses helper would not start: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd, s.port = nil, 0
		}
		s.mu.Unlock()
	}()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if alive(ctx, "http://127.0.0.1:"+strconv.Itoa(port)) {
			s.cmd, s.port = cmd, port
			return s.url(), nil
		}
		select {
		case <-exited:
			return "", fmt.Errorf("the senses helper stopped: %s", lastLine(problems.String()))
		case <-ctx.Done():
			cmd.Process.Kill()
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	cmd.Process.Kill()
	return "", errors.New("the senses helper did not become ready")
}

func alive(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Faceprint is the print of a face: 112 x 112 RGB bytes, already aligned.
func (s *Senses) Faceprint(ctx context.Context, rgb []byte) ([]float32, error) {
	return s.print(ctx, "/face", rgb)
}

// Voiceprint is the print of a voice, from a WAV recording.
func (s *Senses) Voiceprint(ctx context.Context, wav []byte) ([]float32, error) {
	return s.print(ctx, "/voice", wav)
}

func (s *Senses) print(ctx context.Context, path string, data []byte) ([]float32, error) {
	url, err := s.ensure(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(data)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the senses helper is not answering: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		Print []float32 `json:"print"`
		Error string    `json:"error"`
	}
	json.Unmarshal(raw, &out)
	if res.StatusCode != http.StatusOK || len(out.Print) == 0 {
		return nil, fmt.Errorf("the senses helper failed: %s", out.Error)
	}
	return out.Print, nil
}

// Stop shuts the helper down.
func (s *Senses) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.cmd, s.port = nil, 0
}

// Similarity is the cosine of two prints, 1 for the same direction.
func Similarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
