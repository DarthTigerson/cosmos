package brain

import (
	"strings"
	"sync"
	"time"
)

// The room: what she has overheard while listening for her name. It is
// held in memory only, for a few minutes, and never written to her
// stream, her memories or her sleep. A reply she is asked for may look
// at what was said just before; nothing else ever sees it.

type overheard struct {
	at   time.Time
	text string
}

type room struct {
	mu    sync.Mutex
	lines []overheard
}

// Overhear keeps a line said nearby that was not meant for her.
func (b *Brain) Overhear(text string) {
	text = strings.TrimSpace(text)
	minutes := b.Cfg.Get().RoomMinutes
	if text == "" || minutes <= 0 {
		return
	}
	b.room.mu.Lock()
	defer b.room.mu.Unlock()
	b.room.lines = append(b.room.lines, overheard{at: time.Now(), text: text})
	b.room.trim(time.Duration(minutes) * time.Minute)
}

// Room returns what was overheard since she was last spoken to, oldest
// first, and lets go of it: each line is shown to one reply at most.
func (b *Brain) Room() string {
	minutes := b.Cfg.Get().RoomMinutes
	b.room.mu.Lock()
	defer b.room.mu.Unlock()
	if minutes <= 0 {
		b.room.lines = nil
		return ""
	}
	b.room.trim(time.Duration(minutes) * time.Minute)
	var sb strings.Builder
	for _, l := range b.room.lines {
		sb.WriteString("- " + l.text + "\n")
	}
	b.room.lines = nil
	return strings.TrimSpace(sb.String())
}

// ForgetRoom drops everything overheard: listening has stopped.
func (b *Brain) ForgetRoom() {
	b.room.mu.Lock()
	defer b.room.mu.Unlock()
	b.room.lines = nil
}

func (r *room) trim(keep time.Duration) {
	cut := 0
	for cut < len(r.lines) && time.Since(r.lines[cut].at) > keep {
		cut++
	}
	r.lines = r.lines[cut:]
	// A long stretch of talk is kept to its last part.
	if len(r.lines) > 40 {
		r.lines = r.lines[len(r.lines)-40:]
	}
}
