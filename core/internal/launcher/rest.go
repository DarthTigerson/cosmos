package launcher

import (
	"context"
	"errors"
	"time"

	"cosmos/core/internal/config"
)

// Resting: what is loaded when. Awake she needs the main and worker
// models; asleep only the sleeper. On one Mac they do not all fit at once,
// so the side she is not using is stopped. Its requests go to the side that
// is loaded: asleep, the sleeper stands in for both; awake, the main model
// stands in for the sleeper. The memory model is small and stays.
//
// Only servers this launcher starts are rested. One started outside Cosmos
// uses memory Cosmos cannot give back, so it is used as it is.

// needs reports whether a tier belongs to the side that is loaded now.
func needs(c config.Config, asleep bool, tier string) bool {
	ep := c.Tier(tier)
	if tier == "embed" || ep.Launch == nil || ep.Launch.ModelPath == "" {
		return true
	}
	side := []string{"main", "worker"}
	if asleep {
		side = []string{"sleeper"}
	}
	for _, t := range side {
		if c.Tier(t).BaseURL == ep.BaseURL {
			return true
		}
	}
	return false
}

// route is the tier that answers for this one in the current state.
func route(c config.Config, asleep bool, tier string) string {
	if needs(c, asleep, tier) {
		return tier
	}
	if asleep {
		return "sleeper"
	}
	return "main"
}

// Resting reports whether a tier is stopped because of her state.
func (l *Launcher) Resting(tier string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !needs(l.Cfg.Get(), l.asleep, tier)
}

// Acquire routes a request, makes sure its server is up, and counts it as
// in flight until release is called, so it is not stopped halfway through.
func (l *Launcher) Acquire(ctx context.Context, tier string) (string, func(), error) {
	asked := tier
	for try := 0; ; try++ {
		l.mu.Lock()
		tier = route(l.Cfg.Get(), l.asleep, asked)
		base := l.Cfg.Get().Tier(tier).BaseURL
		l.busy[base]++
		l.mu.Unlock()
		release := func() {
			l.mu.Lock()
			l.busy[base]--
			l.mu.Unlock()
		}
		err := l.Ensure(ctx, tier)
		if err == nil {
			return tier, release, nil
		}
		release()
		// She fell asleep or woke between routing and loading: route again.
		if !errors.Is(err, errResting) || try > 0 {
			return tier, func() {}, err
		}
	}
}

// Rest switches what is loaded for her state. It returns once the side she
// no longer needs has stopped and its memory is free. Waking, it also starts
// loading the main and worker models so the first reply does not wait.
func (l *Launcher) Rest(ctx context.Context, asleep bool) error {
	l.mu.Lock()
	changed := l.asleep != asleep
	l.asleep = asleep
	l.mu.Unlock()
	if changed {
		if asleep {
			l.Log.Printf("launcher: she is asleep, stopping the main and worker models")
		} else {
			l.Log.Printf("launcher: she is awake, stopping the sleeper model")
		}
	}
	if err := l.clearResting(ctx); err != nil {
		return err
	}
	if !asleep && changed {
		for _, t := range []string{"main", "worker"} {
			go func() {
				if err := l.Ensure(context.Background(), t); err != nil {
					l.Log.Printf("launcher: loading %s on waking: %v", t, err)
				}
			}()
		}
	}
	return nil
}

// clearResting stops every server of ours that her state does not need,
// each once nothing is still using it, and waits for them to exit.
func (l *Launcher) clearResting(ctx context.Context) error {
	for {
		l.mu.Lock()
		c := l.Cfg.Get()
		resting := map[string]bool{}
		for _, t := range []string{"main", "worker", "sleeper"} {
			if !needs(c, l.asleep, t) {
				resting[c.Tier(t).BaseURL] = true
			}
		}
		alive := false
		for base, s := range l.servers {
			if !resting[base] || s.cmd == nil || s.exited {
				continue
			}
			alive = true
			if l.busy[base] == 0 && !s.stopping {
				s.stopping = true
				s.cmd.Process.Kill()
			}
		}
		l.mu.Unlock()
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

var errResting = errors.New("this model rests while she is in her current state, so Cosmos will not load it now")
