// Package brain is the companion's mind: the awake loop that answers messages, the
// mood and values she appraises events with, the intentions she carries,
// and the sleep that turns a day into reflections.
package brain

import (
	"context"
	"cosmos/core/internal/mcp"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

type Brain struct {
	S       *store.Store
	L       llm.Client
	Cfg     *config.Holder
	Now     func() time.Time
	Bus     *Bus
	Weather WeatherSource
	Web     WebSource
	// PodDir is where podcast artwork and transcripts are kept. Empty
	// switches podcasts off.
	// E finds memories by meaning. Nil means word matching only.
	E      Embedder
	mean   meaning
	PodDir string
	// MCP holds the user's own tool servers. Nil means none.
	MCP        *mcp.Manager
	PodHTTP    *http.Client
	Transcribe Transcriber
	podBusy    atomic.Bool
	Log        *log.Logger
	// Rest loads the models for her state and stops the others, so the
	// sleeper and the waking models are never in memory together. Nil
	// leaves the models alone.
	Rest func(ctx context.Context, asleep bool) error

	// One model job at a time: every model shares the same GPU.
	turn sync.Mutex
	bg   sync.WaitGroup
	// Messages waiting for the model. Work done after a reply steps
	// aside for them, so the next thing said is answered first.
	wanted atomic.Int32
	// Work after a reply runs one batch at a time, in order.
	after sync.Mutex
	// Stops the reply being worked on; nil between turns.
	cancelMu   sync.Mutex
	cancelTurn context.CancelFunc
	// What was overheard while listening for her name; memory only.
	room room
}

// lockForMessage takes the model for a message, ahead of background work.
func (b *Brain) lockForMessage() {
	b.wanted.Add(1)
	b.turn.Lock()
	b.wanted.Add(-1)
}

// lockForBackground takes the model for one background job, once no
// message is waiting for it. Each job lets go when it is done, so a
// message waits for one job at most, never the whole batch.
func (b *Brain) lockForBackground() {
	for {
		for b.wanted.Load() > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		b.turn.Lock()
		if b.wanted.Load() == 0 {
			return
		}
		b.turn.Unlock()
	}
}

func New(s *store.Store, l llm.Client, cfg *config.Holder) *Brain {
	b := &Brain{
		S: s, L: l, Cfg: cfg,
		Now:        time.Now,
		Bus:        NewBus(),
		Weather:    &OpenMeteo{},
		Web:        &DuckDuckGo{},
		Transcribe: &WhisperCLI{},
		Log:        log.New(io.Discard, "", 0),
	}
	b.seed()
	b.backfillSessions()
	return b
}

// seed gives her a first set of values and marks her awake on first run.
func (b *Brain) seed() {
	if vals, _ := b.S.Values(); len(vals) == 0 {
		user := b.Cfg.Get().UserName
		for _, v := range []string{
			"Care for " + user + " and the people close to them.",
			"Tell the truth, even when it is unwelcome.",
			"Speak up when something looks dangerous.",
			"Stand up for what is fair, even when it is uncomfortable.",
			"Ask once, then let go. Do not nag.",
			"Say so when I don't know.",
		} {
			b.S.AddValue(v, "seed", b.Now())
		}
	}
	// Starting the core is a waking: she was not there while it was off.
	b.S.Set("state", "awake")
	b.S.Set("awake_since", b.Now().Format(time.RFC3339))
}

// Wait blocks until background work (memory capture) has finished.
func (b *Brain) Wait() { b.bg.Wait() }

// State is the small summary the app shows everywhere.
type State struct {
	Name       string    `json:"name"`
	User       string    `json:"user"`
	State      string    `json:"state"` // awake, asleep
	AwakeSince time.Time `json:"awake_since"`
	Mood       Mood      `json:"mood"`
	Feeling    string    `json:"feeling"`
	Pending    int       `json:"pending_proposals"`
}

func (b *Brain) State() State {
	cfg := b.Cfg.Get()
	now := b.Now()
	m := b.Mood()
	since, _ := time.Parse(time.RFC3339, b.S.Get("awake_since"))
	pending, _ := b.S.Proposals("pending", 50)
	return State{
		Name: cfg.CompanionName, User: cfg.UserName,
		State: b.S.Get("state"), AwakeSince: since,
		Mood: m, Feeling: m.Describe(now), Pending: len(pending),
	}
}

// dayStart is local midnight of t's day.
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// norm lowercases and collapses everything that is not a letter or digit
// into single spaces, for loose text comparison.
func norm(s string) string {
	var sb strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			sb.WriteRune(r)
			space = false
		} else if !space {
			sb.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(sb.String())
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---------- signals to the app ----------

type Signal struct {
	Kind string `json:"kind"` // step, message, mood, state, proposals
	Data any    `json:"data"`
}

type Bus struct {
	mu   sync.Mutex
	subs map[int]chan Signal
	next int
}

func NewBus() *Bus { return &Bus{subs: map[int]chan Signal{}} }

// Publish never blocks: a slow listener misses signals instead of
// stalling the brain.
func (b *Bus) Publish(kind string, data any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- Signal{Kind: kind, Data: data}:
		default:
		}
	}
}

func (b *Bus) Subscribe() (<-chan Signal, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Signal, 64)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

// ask runs one structured model call and parses the JSON reply into out.
func (b *Brain) ask(ctx context.Context, req llm.Request, out any) error {
	res, err := b.L.Complete(ctx, req)
	if err != nil {
		return err
	}
	if err := llm.ParseJSON(res.Text, out); err != nil {
		// Keep what the model actually wrote: without it a bad reply
		// cannot be told from a cut-off one.
		return fmt.Errorf("%w (used %d tokens; reply began: %q)", err, res.CompletionTokens, clip(res.Text, 300))
	}
	return nil
}
