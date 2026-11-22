package launcher

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cosmos/core/internal/config"
)

// The test binary stands in for llama-server: run with FAKE_LLAMA set, it
// answers /health on the port it is given until it is killed.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_LLAMA") != "" {
		port := ""
		for i, a := range os.Args {
			if a == "--port" && i+1 < len(os.Args) {
				port = os.Args[i+1]
			}
		}
		http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		http.ListenAndServe("127.0.0.1:"+port, nil)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func restLauncher(t *testing.T) *Launcher {
	t.Setenv("FAKE_LLAMA", "1")
	dir := t.TempDir()
	model := filepath.Join(dir, "m.gguf")
	os.WriteFile(model, []byte("x"), 0o600)
	ep := func(name string) config.Endpoint {
		return config.Endpoint{BaseURL: "http://127.0.0.1:" + freePort(t) + "/v1", Model: name,
			Launch: &config.Launch{Binary: os.Args[0], ModelPath: model}}
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	c.Main, c.Worker, c.Sleeper = ep("main"), ep("worker"), ep("sleeper")
	c.Embed = config.Endpoint{}
	if err := cfg.Set(c); err != nil {
		t.Fatal(err)
	}
	l := New(cfg, log.New(io.Discard, "", 0))
	t.Cleanup(l.StopAll)
	return l
}

func up(l *Launcher, tier string) bool {
	return Healthy(context.Background(), l.Cfg.Get().Tier(tier).BaseURL)
}

func acquire(t *testing.T, l *Launcher, tier, want string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, release, err := l.Acquire(ctx, tier)
	if err != nil {
		t.Fatalf("acquire %s: %v", tier, err)
	}
	if got != want {
		t.Fatalf("acquire %s went to %s, want %s", tier, got, want)
	}
	return release
}

func TestSleepAndWakeNeverLoadBothSides(t *testing.T) {
	l := restLauncher(t)
	ctx := context.Background()

	// Awake: main and worker load, and sleeper work goes to main.
	acquire(t, l, "main", "main")()
	acquire(t, l, "worker", "worker")()
	acquire(t, l, "sleeper", "main")()
	if up(l, "sleeper") {
		t.Fatal("the sleeper loaded while she was awake")
	}

	// A request still running holds off falling asleep until it is done.
	held := acquire(t, l, "worker", "worker")
	done := make(chan error, 1)
	go func() { done <- l.Rest(ctx, true) }()
	select {
	case <-done:
		t.Fatal("the worker was stopped in the middle of a request")
	case <-time.After(700 * time.Millisecond):
	}
	held()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if up(l, "main") || up(l, "worker") {
		t.Fatal("the waking models are still loaded after falling asleep")
	}
	if !l.Resting("main") || !l.Resting("worker") || l.Resting("sleeper") {
		t.Fatal("resting is wrong while asleep")
	}

	// Asleep: everything goes to the sleeper, and nothing else loads.
	acquire(t, l, "worker", "sleeper")()
	acquire(t, l, "main", "sleeper")()
	if up(l, "main") || up(l, "worker") {
		t.Fatal("a waking model loaded while she slept")
	}
	if err := l.Ensure(ctx, "main"); err == nil {
		t.Fatal("main could be started by hand while she slept")
	}
	if got := l.Status(ctx, "main").State; got != "resting" {
		t.Fatalf("main is %q while she sleeps, want resting", got)
	}

	// Waking stops the sleeper and loads main and worker again.
	if err := l.Rest(ctx, false); err != nil {
		t.Fatal(err)
	}
	if up(l, "sleeper") {
		t.Fatal("the sleeper is still loaded after waking")
	}
	deadline := time.Now().Add(10 * time.Second)
	for !(up(l, "main") && up(l, "worker")) {
		if time.Now().After(deadline) {
			t.Fatal("main and worker did not load again on waking")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestOutsideServersAreLeftAlone(t *testing.T) {
	c := config.Config{
		Main:    config.Endpoint{BaseURL: "http://a/v1", Launch: &config.Launch{ModelPath: "m"}},
		Worker:  config.Endpoint{BaseURL: "http://b/v1", Launch: &config.Launch{ModelPath: "w"}},
		Sleeper: config.Endpoint{BaseURL: "http://other-mac/v1"}, // not ours
	}
	if route(c, false, "sleeper") != "sleeper" {
		t.Fatal("an outside sleeper was routed away while awake")
	}
	if route(c, true, "worker") != "sleeper" {
		t.Fatal("worker work did not go to the sleeper while asleep")
	}
	// No sleeper set: it falls back to main, which then stays up asleep.
	c.Sleeper = config.Endpoint{}
	if route(c, true, "main") != "main" || route(c, true, "worker") != "sleeper" {
		t.Fatal("without a sleeper, main should carry the night")
	}
}
