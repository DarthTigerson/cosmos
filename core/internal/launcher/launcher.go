// Package launcher starts llama-server processes for endpoints that are
// configured with a model file, keeps their output, and stops them again.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmos/core/internal/config"
)

// tail keeps the last few kilobytes a server printed.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailSize = 12 * 1024

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailSize {
		t.buf = t.buf[len(t.buf)-tailSize:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

type server struct {
	cmd    *exec.Cmd
	out    *tail
	exited bool
	err    string // why the last start failed, if it did
}

type Launcher struct {
	Cfg *config.Holder
	Log *log.Logger

	mu      sync.Mutex
	servers map[string]*server // keyed by base URL
}

func New(cfg *config.Holder, logger *log.Logger) *Launcher {
	return &Launcher{Cfg: cfg, Log: logger, servers: map[string]*server{}}
}

func healthURL(base string) (string, *url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", nil, fmt.Errorf("bad server address %q", base)
	}
	return u.Scheme + "://" + u.Host + "/health", u, nil
}

// Healthy reports whether the endpoint's server is up and has its model
// loaded.
func Healthy(ctx context.Context, base string) bool {
	h, _, err := healthURL(base)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// FindBinary resolves the llama-server program: the configured path, then
// PATH, then the usual Homebrew places (an app started from the Dock does
// not inherit the shell's PATH). It returns "" when none is found.
func FindBinary(configured string) string {
	if configured != "" {
		p := expandHome(configured)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/llama-server", "/usr/local/bin/llama-server"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ModelFile is one GGUF found on disk.
type ModelFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ScanModels lists the .gguf files in the given folders, by name.
func ScanModels(dirs []string) []ModelFile {
	out := []ModelFile{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(expandHome(dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				continue
			}
			full := filepath.Join(expandHome(dir), e.Name())
			if seen[full] {
				continue
			}
			seen[full] = true
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, ModelFile{Name: e.Name(), Path: full, Size: info.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Status describes one tier's server for the Models screen.
type Status struct {
	State string `json:"state"` // running, starting, stopped, failed, external
	Error string `json:"error,omitempty"`
	Log   string `json:"log,omitempty"`
}

// Status reports on the server behind a tier. "external" means something
// is answering at the address that this launcher did not start.
func (l *Launcher) Status(ctx context.Context, tier string) Status {
	ep := l.Cfg.Get().Tier(tier)
	up := ep.BaseURL != "" && Healthy(ctx, ep.BaseURL)
	l.mu.Lock()
	s := l.servers[ep.BaseURL]
	l.mu.Unlock()
	switch {
	case s == nil && up:
		return Status{State: "external"}
	case s == nil:
		return Status{State: "stopped"}
	case s.err != "":
		return Status{State: "failed", Error: s.err, Log: s.out.String()}
	case s.exited:
		return Status{State: "stopped", Log: s.out.String()}
	case up:
		return Status{State: "running", Log: s.out.String()}
	}
	return Status{State: "starting", Log: s.out.String()}
}

// Ensure makes sure the server for a tier is answering, starting it when
// the endpoint has a model file set. Endpoints without one are left to
// whoever runs them.
func (l *Launcher) Ensure(ctx context.Context, tier string) error {
	ep := l.Cfg.Get().Tier(tier)
	if ep.Launch == nil || ep.Launch.ModelPath == "" {
		return nil
	}
	if Healthy(ctx, ep.BaseURL) {
		return nil
	}
	s, err := l.spawn(ep, tier)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		if Healthy(ctx, ep.BaseURL) {
			return nil
		}
		l.mu.Lock()
		exited, why := s.exited, s.err
		l.mu.Unlock()
		if exited {
			return errors.New(why)
		}
	}
	return errors.New("the model server did not become ready in three minutes")
}

// spawn starts the server for an endpoint unless one of ours is already
// loading there.
func (l *Launcher) spawn(ep config.Endpoint, tier string) (*server, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.servers[ep.BaseURL]; s != nil && !s.exited {
		return s, nil
	}
	fail := func(msg string) (*server, error) {
		l.servers[ep.BaseURL] = &server{out: &tail{}, exited: true, err: msg}
		return nil, errors.New(msg)
	}
	_, u, err := healthURL(ep.BaseURL)
	if err != nil {
		return fail(err.Error())
	}
	model := expandHome(ep.Launch.ModelPath)
	if _, err := os.Stat(model); err != nil {
		return fail("model file not found: " + model)
	}
	bin := FindBinary(ep.Launch.Binary)
	if bin == "" {
		return fail("llama-server was not found. Install llama.cpp, or set the program path.")
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	ctxSize := ep.Launch.ContextSize
	if ctxSize == 0 {
		ctxSize = 8192
	}
	args := []string{
		"-m", model,
		"-c", strconv.Itoa(ctxSize),
		"-ngl", strconv.Itoa(ep.Launch.GPULayers),
		"--host", u.Hostname(), "--port", port,
		"--alias", ep.Model,
	}
	if tier == "embed" {
		args = append(args, "--embedding", "--pooling", "mean")
	}
	if ep.Launch.MMProj != "" {
		args = append(args, "--mmproj", expandHome(ep.Launch.MMProj))
	}
	if ep.APIKey != "" {
		args = append(args, "--api-key", ep.APIKey)
	}
	s := &server{out: &tail{}}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = s.out, s.out
	if err := cmd.Start(); err != nil {
		return fail(fmt.Sprintf("could not start %s: %v", bin, err))
	}
	s.cmd = cmd
	l.servers[ep.BaseURL] = s
	l.Log.Printf("launcher: starting llama-server for %s on %s", tier, u.Host)
	go func() {
		err := cmd.Wait()
		l.mu.Lock()
		defer l.mu.Unlock()
		s.exited = true
		// A server we stopped on purpose is not a failure.
		if err != nil && l.servers[ep.BaseURL] == s && !strings.Contains(err.Error(), "signal: killed") {
			s.err = "llama-server exited: " + err.Error()
		}
	}()
	return s, nil
}

// Stop ends the server for a tier if this launcher started it.
func (l *Launcher) Stop(tier string) error {
	ep := l.Cfg.Get().Tier(tier)
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.servers[ep.BaseURL]
	if s == nil || s.cmd == nil || s.exited {
		delete(l.servers, ep.BaseURL)
		return errors.New("Cosmos did not start a server at this address, so it will not stop one")
	}
	s.cmd.Process.Kill()
	return nil
}

// StopAll ends every server this launcher started.
func (l *Launcher) StopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.servers {
		if s.cmd != nil && !s.exited {
			s.cmd.Process.Kill()
		}
	}
}
