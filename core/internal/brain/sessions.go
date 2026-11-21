package brain

import (
	"context"
	"strconv"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// A session is one sitting with her. She is the same person in all of
// them: memories, people, mood and intentions carry across. Only the
// thread starts clean. A session stays open, and visible, until she
// sleeps on it.

const (
	// After this much silence the next message starts a new session.
	sessionGap = 2 * time.Hour
	// When she speaks first, it joins the current session only if that
	// one was active this recently.
	sessionWarm = 30 * time.Minute
)

func (b *Brain) currentID() int64 {
	id, _ := strconv.ParseInt(b.S.Get("session_id"), 10, 64)
	return id
}

func (b *Brain) setCurrent(id int64, pinned bool) {
	b.S.Set("session_id", strconv.FormatInt(id, 10))
	if pinned {
		b.S.Set("session_pinned", "1")
	} else {
		b.S.Set("session_pinned", "")
	}
}

// CurrentSession returns the open session, if there is one.
func (b *Brain) CurrentSession() (store.Session, bool) {
	s, ok := b.S.GetSession(b.currentID())
	if !ok || !s.SleptAt.IsZero() {
		return store.Session{}, false
	}
	return s, true
}

// sessionFor returns the session a message arriving now belongs to,
// starting a new one after sleep or a long silence. A session the user
// opened by hand is continued however long it has been.
func (b *Brain) sessionFor(now time.Time, startedBy string) store.Session {
	if s, ok := b.CurrentSession(); ok {
		pinned := b.S.Get("session_pinned") != ""
		if pinned || s.Messages == 0 || now.Sub(s.EndedAt) < sessionGap {
			return s
		}
	}
	return b.startSession(now, startedBy, "")
}

func (b *Brain) startSession(now time.Time, startedBy, title string) store.Session {
	s, _ := b.S.AddSession(store.Session{StartedAt: now, EndedAt: now, StartedBy: startedBy, Title: title})
	b.setCurrent(s.ID, false)
	b.Bus.Publish("sessions", nil)
	return s
}

// NewSession starts a clean session at the user's request. An unused one
// is reused, so pressing it twice does not leave empty sessions behind.
func (b *Brain) NewSession() store.Session {
	if s, ok := b.CurrentSession(); ok && s.Messages == 0 {
		return s
	}
	return b.startSession(b.Now(), "user", "")
}

// OpenSession makes an earlier session current again, if she has not
// slept on it.
func (b *Brain) OpenSession(id int64) (store.Session, bool) {
	s, ok := b.S.GetSession(id)
	if !ok || !s.SleptAt.IsZero() {
		return s, false
	}
	b.setCurrent(id, true)
	b.Bus.Publish("sessions", nil)
	return s, true
}

// touch records activity in a session, and names it from the first thing
// the user said in it.
func (b *Brain) touch(s *store.Session, now time.Time, userText string) {
	s.EndedAt = now
	if s.Title == "" && strings.TrimSpace(userText) != "" {
		s.Title = clip(strings.Join(strings.Fields(userText), " "), 42)
	}
	b.S.SaveSession(*s)
}

// nameSession gives a conversation a short title once there is enough
// of it to name. Until then it carries the user's first words.
func (b *Brain) nameSession(ctx context.Context, cfg config.Config, id int64) {
	s, ok := b.S.GetSession(id)
	key := "session_named_" + strconv.FormatInt(id, 10)
	if !ok || s.StartedBy != "user" || s.Messages < 4 || b.S.Get(key) != "" {
		return
	}
	b.S.Set(key, "1") // one try: a title that changes under the user is worse than a plain one
	events, _ := b.S.SessionEvents(id, 8, "user_msg", "nova_msg")
	var out struct {
		Title string `json:"title"`
	}
	err := b.ask(ctx, llm.Request{
		Tier: "worker", Role: "title",
		System:   "Give this conversation a title of two to five words that says what it is about, like a note in a diary: \"Weekend with the boys\", \"Database outage at work\". No quotation marks, no full stop, and not the first message repeated.",
		Messages: []llm.Message{{Role: "user", Content: conversation(cfg, events)}},
		Schema:   titleSchema, MaxTokens: 40,
	}, &out)
	title := strings.Trim(strings.TrimSpace(out.Title), "\".")
	if err != nil || title == "" || len(strings.Fields(title)) > 7 {
		return
	}
	s.Title = clip(title, 42)
	b.S.SaveSession(s)
	b.Bus.Publish("sessions", nil)
}

// carryOver tells the writer what else was talked about since she last
// slept, so a clean thread does not make her forget this morning.
func (b *Brain) carryOver(cfg config.Config, current int64) string {
	open, _ := b.S.OpenSessions()
	lines := []string{}
	for _, s := range open {
		if s.ID == current || s.Messages == 0 {
			continue
		}
		line := "- " + s.StartedAt.Format("15:04") + " " + s.Title
		if s.Summary != "" {
			line += ": " + s.Summary
		}
		lines = append(lines, line)
		if len(lines) == 6 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Earlier, in other conversations since you last slept, you and " + cfg.UserName + " talked about these (newest first). You remember them. If asked what you talked about earlier, answer from this list. Otherwise do not bring them up:\n" + strings.Join(lines, "\n")
}

// summarize writes one line about a session, for carryOver and for the
// morning review.
func (b *Brain) summarize(ctx context.Context, tier string, s store.Session) store.Session {
	if s.Summary != "" || s.Messages < 2 {
		return s
	}
	cfg := b.Cfg.Get()
	events, _ := b.S.SessionEvents(s.ID, 40, "user_msg", "nova_msg", "initiative")
	res, err := b.L.Complete(ctx, llm.Request{
		Tier: tier, Role: "summary",
		System: "Say in one plain sentence of at most 25 words what this conversation was about and where it ended up. " +
			"Write it as " + cfg.CompanionName + " speaking to " + cfg.UserName + ": call " + cfg.UserName + " \"you\" and yourself \"I\", never by name. Past tense. " +
			"Example: \"You told me about your trip and we picked a day to go.\" Only the sentence.",
		Messages:  []llm.Message{{Role: "user", Content: conversation(cfg, events)}},
		MaxTokens: 80,
	})
	if err != nil {
		b.Log.Printf("summary: %v", err)
		return s
	}
	line := cutRepeats(cleanDraft(cfg, res.Text))
	if first, _, ok := strings.Cut(line, "\n"); ok {
		line = first
	}
	s.Summary = clip(line, 220)
	// Only the summary: the session may have moved on while this ran.
	if fresh, ok := b.S.GetSession(s.ID); ok {
		fresh.Summary = s.Summary
		b.S.SaveSession(fresh)
		return fresh
	}
	return s
}

// closeSessions marks every open session as slept on. Sessions without a
// summary get one first, so the morning review can say what each was.
func (b *Brain) closeSessions(ctx context.Context, at time.Time) {
	open, _ := b.S.OpenSessions()
	for i, s := range open {
		if s.Messages == 0 {
			s.SleptAt = at
			b.S.SaveSession(s)
			continue
		}
		if i < 10 {
			s = b.summarize(ctx, "sleeper", s)
		}
		s.SleptAt = at
		b.S.SaveSession(s)
	}
	b.S.Set("session_id", "")
	b.S.Set("session_pinned", "")
	b.Bus.Publish("sessions", nil)
}

// backfillSessions gives sessions to messages from before sessions
// existed: a new one after every sleep or long silence. Those from before
// the last sleep count as slept on.
func (b *Brain) backfillSessions() {
	msgs, err := b.S.EventsBetween(time.UnixMilli(1), b.Now().Add(time.Hour))
	if err != nil {
		return
	}
	lastSleep, _ := time.Parse(time.RFC3339, b.S.Get("last_sleep_at"))
	var cur *store.Session
	slept := false
	flush := func() {
		if cur == nil {
			return
		}
		if !lastSleep.IsZero() && !cur.StartedAt.After(lastSleep) {
			cur.SleptAt = lastSleep
		}
		b.S.SaveSession(*cur)
		cur = nil
	}
	for _, e := range msgs {
		if e.Kind == "sleep" {
			slept = true
			continue
		}
		if e.Kind != "user_msg" && e.Kind != "nova_msg" && e.Kind != "initiative" && e.Kind != "appraisal" {
			continue
		}
		if e.SessionID != 0 {
			return // sessions already exist
		}
		if cur == nil || slept || e.At.Sub(cur.EndedAt) >= sessionGap {
			flush()
			by := "user"
			if e.Kind == "initiative" {
				by = "nova"
			}
			s, err := b.S.AddSession(store.Session{StartedAt: e.At, EndedAt: e.At, StartedBy: by})
			if err != nil {
				return
			}
			cur, slept = &s, false
		}
		cur.EndedAt = e.At
		if cur.Title == "" && (e.Kind == "user_msg" || e.Kind == "initiative") {
			cur.Title = clip(strings.Join(strings.Fields(e.Text), " "), 42)
		}
		b.S.SetEventSession(e.ID, cur.ID)
	}
	last := cur
	flush()
	if last != nil && last.SleptAt.IsZero() {
		b.setCurrent(last.ID, false)
	}
}
