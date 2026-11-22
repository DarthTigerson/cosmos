package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// Initiative works from reasons, not from a timer. Conversations, sleep
// and the world put intentions in a queue, each with why she cares and
// when to raise it. The heartbeat only checks whether one is due and
// whether now is an acceptable moment. No model runs while she is idle.

func inQuietHours(cfg config.Config, now time.Time) bool {
	h := now.Hour()
	if cfg.QuietFrom == cfg.QuietTo {
		return false
	}
	if cfg.QuietFrom < cfg.QuietTo {
		return h >= cfg.QuietFrom && h < cfg.QuietTo
	}
	return h >= cfg.QuietFrom || h < cfg.QuietTo
}

// gate decides whether an intention of this kind may be raised now. It
// returns "" to allow, or the reason to hold back. Reminders always go
// out: the user asked for them at that time. The morning brief ignores
// the cooldown and the budget, since it is a daily fixture.
func gate(cfg config.Config, now time.Time, kind string, lastInitiative time.Time, sentToday int) string {
	if kind == "reminder" {
		return ""
	}
	if inQuietHours(cfg, now) {
		return "quiet hours"
	}
	if kind == "brief" {
		return ""
	}
	if !lastInitiative.IsZero() && now.Sub(lastInitiative) < time.Duration(cfg.CooldownMinutes)*time.Minute {
		return "too soon after the last time she reached out"
	}
	if sentToday >= cfg.InitiativeBudget {
		return "daily budget used up"
	}
	return ""
}

// Tick is the heartbeat. It is safe to call often: it does nothing while
// a turn is running.
func (b *Brain) Tick(ctx context.Context) {
	// Listening goes on beside everything else and never waits here.
	b.podcastTick(ctx)
	b.bookTick(ctx)
	go b.embedMissing(context.WithoutCancel(ctx))
	if !b.turn.TryLock() {
		return
	}
	defer b.turn.Unlock()
	now := b.Now()
	cfg := b.Cfg.Get()
	if b.S.Get("state") == "asleep" {
		// She holds the lock for the whole of sleep, so finding her
		// asleep here means a sleep was cut short. Wake her.
		b.wake(now)
	}
	b.expireIntentions(now)
	b.gatherReminders(now)
	b.queueBrief(cfg, now)
	b.watchWeather(ctx, cfg, now)
	// Not while the last reply is still being filed: the next beat sleeps.
	if b.shouldSleep(cfg, now) && b.after.TryLock() {
		defer b.after.Unlock()
		if _, err := b.sleepLocked(ctx); err != nil {
			b.Log.Printf("sleep: %v", err)
		}
		return
	}
	b.deliverOne(ctx, cfg, now)
}

func (b *Brain) expireIntentions(now time.Time) {
	list, _ := b.S.Intentions("open", "sent")
	for _, i := range list {
		switch {
		case i.Status == "open" && now.After(i.ExpiresAt):
			b.S.SetIntentionStatus(i.ID, "expired", now)
		case i.Status == "sent" && now.Sub(i.SentAt) > 48*time.Hour:
			// She asked once and it was never settled. Let it go.
			b.S.SetIntentionStatus(i.ID, "expired", now)
		}
	}
}

func (b *Brain) gatherReminders(now time.Time) {
	due, _ := b.S.DueReminders(now)
	for _, r := range due {
		b.S.AddIntention(store.Intention{
			Kind: "reminder", Text: r.Title, Reason: "They asked to be reminded.",
			TriggerAt: now, ExpiresAt: now.Add(12 * time.Hour), Importance: 5, CreatedAt: now,
		})
		if r.Repeat != "" {
			b.S.MoveCalendarItem(r.ID, store.NextOccurrence(r.At, r.Repeat, now.Add(time.Minute)))
		} else {
			b.S.MarkCalendarNotified(r.ID)
		}
	}
}

func (b *Brain) queueBrief(cfg config.Config, now time.Time) {
	h := now.Hour()
	if h < cfg.BriefHour || h >= cfg.BriefHour+5 || b.S.Get("last_brief_day") == dayKey(now) {
		return
	}
	b.S.Set("last_brief_day", dayKey(now))
	b.S.AddIntention(store.Intention{
		Kind: "brief", Text: "Morning note for " + now.Format("Monday 2 January"),
		Reason:    "A new day is starting.",
		TriggerAt: now, ExpiresAt: dayStart(now).Add(time.Duration(cfg.BriefHour+5) * time.Hour),
		Importance: 4, CreatedAt: now,
	})
}

func (b *Brain) watchWeather(ctx context.Context, cfg config.Config, now time.Time) {
	if !cfg.WeatherWatch || strings.TrimSpace(cfg.Location) == "" {
		return
	}
	var st watchState
	b.S.GetJSON("weather_watch", &st)
	if now.Before(st.NextCheckAt) {
		return
	}
	w, cmp, err := b.lookAtWeather(ctx, cfg.Location)
	if err != nil {
		st.NextCheckAt = now.Add(30 * time.Minute)
		b.S.SetJSON("weather_watch", st)
		return
	}
	severity := classifySeverity(w)
	if reason := decideReport(st, severity, now); reason != "" {
		facts := weatherFacts(w, cmp)
		next := []string{}
		for i, h := range w.Hourly {
			if i >= 6 {
				break
			}
			_, clock, _ := strings.Cut(h.Time, "T")
			next = append(next, fmt.Sprintf("%s %s %d%%", clock, describeCode(h.Code), h.RainChancePct))
		}
		facts["next_hours"] = next
		raw, _ := json.Marshal(facts)
		importance := 3
		if severity == sevActive {
			importance = 4
		}
		b.S.AddIntention(store.Intention{
			Kind: "weather", Text: reason, Reason: "She watches the weather where " + cfg.UserName + " lives.",
			Facts: string(raw), TriggerAt: now, ExpiresAt: now.Add(2 * time.Hour), Importance: importance, CreatedAt: now,
		})
		st.LastReportAt = now
	}
	st.Severity = severity
	st.NextCheckAt = now.Add(watchCadence[severity])
	b.S.SetJSON("weather_watch", st)
}

// initiativesToday returns when she last reached out and how many times
// today, not counting reminders the user asked for.
func (b *Brain) initiativesToday(now time.Time) (time.Time, int) {
	evs, _ := b.S.EventsBetween(dayStart(now), now.Add(time.Second), "initiative")
	var last time.Time
	count := 0
	for _, e := range evs {
		var meta struct {
			Kind string `json:"kind"`
		}
		json.Unmarshal(e.Meta, &meta)
		if meta.Kind == "reminder" {
			continue
		}
		count++
		last = e.At
	}
	return last, count
}

func (b *Brain) deliverOne(ctx context.Context, cfg config.Config, now time.Time) {
	open, _ := b.S.Intentions("open")
	due := []store.Intention{}
	for _, i := range open {
		if !i.TriggerAt.After(now) {
			due = append(due, i)
		}
	}
	if len(due) == 0 {
		return
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].Importance > due[j].Importance })
	last, count := b.initiativesToday(now)
	for _, i := range due {
		// A question waits for a conversation; it is never a reach-out.
		if i.Kind == "curiosity" {
			continue
		}
		if gate(cfg, now, i.Kind, last, count) != "" {
			continue
		}
		if err := b.deliver(ctx, cfg, i); err != nil {
			b.Log.Printf("initiative %d: %v", i.ID, err)
		}
		return // one at a time, never a burst
	}
}

// reachOutTitle names the session a reach-out opens, after what it is
// about.
func reachOutTitle(i store.Intention) string {
	switch i.Kind {
	case "reminder":
		return clip("Reminder: "+i.Text, 42)
	case "podcast":
		show, _, _ := strings.Cut(strings.TrimPrefix(i.Reason, "heard on "), ":")
		return clip("From "+show, 42)
	case "book":
		title, _, _ := strings.Cut(strings.TrimPrefix(i.Reason, "read "), " by ")
		return clip("From "+title, 42)
	case "brief":
		return "Morning note"
	case "weather":
		return "Weather"
	case "reflection":
		return "A thought"
	case "followup":
		return "Checking in"
	case "face":
		return "Who is this?"
	}
	return "From her"
}

// instructionFor tells the writer what she is reaching out about.
func (b *Brain) instructionFor(cfg config.Config, i store.Intention) (string, string) {
	facts := i.Facts
	switch i.Kind {
	case "reminder":
		return cfg.UserName + " asked to be reminded: \"" + i.Text + "\". It is time. Tell them in one sentence.", facts
	case "weather":
		return i.Text + " Write a short weather update for " + cfg.UserName + ": what it is doing now and what is coming in the next hours. One or two sentences.", facts
	case "podcast":
		return "You have just finished listening to a podcast episode (" + i.Reason + "). You wanted to bring this up with " + cfg.UserName + ": \"" + i.Text + "\". Say it in your own words in one or two sentences, and say which show you heard it on. The people talking were the show's hosts. You and " + cfg.UserName + " have not talked about this episode: never say \"what we talked about\" or \"as we discussed\". If it reminds you of something " + cfg.UserName + " is doing, say it made you think of that.", facts
	case "book":
		return "You have just finished reading a book (" + strings.TrimPrefix(i.Reason, "read ") + ") that " + cfg.UserName + " gave you. You wanted to bring this up with them: \"" + i.Text + "\". Say it in your own words in one or two sentences, and say which book it is from. You read it on your own and have not discussed it with them: never say \"what we talked about\" or \"as we discussed\".", facts
	case "face":
		return "You saw someone through your camera and you are not sure who they are. Your message is shown with the picture of them. Ask " + cfg.UserName + " this, in your own words and briefly, without describing the picture: \"" + i.Text + "\"", facts
	case "reflection":
		return "While you slept last night you had this thought: \"" + i.Text + "\". Share it with " + cfg.UserName + " in your own words, briefly.", facts
	case "brief":
		now := b.Now()
		brief := map[string]any{}
		items, _ := b.S.CalendarBetween(dayStart(now), dayStart(now).AddDate(0, 0, 1))
		today := []map[string]any{}
		for _, it := range items {
			today = append(today, calendarView(it))
		}
		brief["calendar_today"] = today
		if cfg.Location != "" {
			if w, cmp, err := b.lookAtWeather(context.Background(), cfg.Location); err == nil {
				brief["weather"] = weatherFacts(w, cmp)
			}
		}
		raw, _ := json.Marshal(brief)
		return "It is the morning of " + now.Format("Monday 2 January") + ". Give " + cfg.UserName + " a short morning note: what today holds, the weather, and anything worth flagging. A few sentences.", string(raw)
	}
	return "A while ago something came up that you wanted to follow up on with " + cfg.UserName + ": \"" + i.Text + "\". Why you care: " + i.Reason + " Check in casually, in one or two short sentences.", facts
}

func (b *Brain) deliver(ctx context.Context, cfg config.Config, i store.Intention) error {
	instruction, facts := b.instructionFor(cfg, i)
	system, known := b.writerSystem(cfg, b.Mood(), i.Text)
	system += "\n- You are reaching out first. " + cfg.UserName + " has not just written to you."
	content := instruction
	if facts != "" {
		content += "\n\nFACTS:\n" + facts
	}
	content += "\n\nWrite only the message itself, as " + cfg.CompanionName + " speaking to " + cfg.UserName + ". No greeting and no offer of further help."
	text := ""
	for attempt := 0; attempt < 2; attempt++ {
		res, err := b.L.Complete(ctx, llm.Request{
			Tier: "main", Role: "writer", System: system,
			Messages:  []llm.Message{{Role: "user", Content: content}},
			MaxTokens: 300, Temperature: 0.7,
		})
		if err != nil {
			return err
		}
		// A pet name that is not hers to use is taken out here too, as
		// it is in a reply.
		text = b.dropBorrowedNames(cutRepeats(cutEchoes(cleanDraft(cfg, res.Text), system, content)))
		bad := unsupportedNumbers(text, instruction, facts, known)
		if len(bad) == 0 {
			break
		}
		content += "\n\nYour draft used numbers that are not in the facts (" + strings.Join(bad, ", ") + "). Write it again using only the facts."
	}
	if text == "" {
		return fmt.Errorf("the writer returned nothing")
	}
	now := b.Now()
	about := map[string]any{"intention_id": i.ID, "kind": i.Kind, "reason": i.Reason}
	// A picture goes with it: the face she is asking about.
	if i.ImageID != 0 {
		about["image_id"] = i.ImageID
		about["subject"] = i.Subject
	}
	meta, _ := json.Marshal(about)
	// It joins the session in progress if there is a warm one, otherwise
	// it opens a session of her own.
	sess, ok := b.CurrentSession()
	// Each thing she raises gets a conversation of its own. It joins the
	// current one only when the user is in the middle of it: they wrote
	// there a moment ago. Two reach-outs never share a session.
	talking := false
	if ok {
		if last, _ := b.S.SessionEvents(sess.ID, 1, "user_msg"); len(last) > 0 {
			talking = now.Sub(last[0].At) <= sessionWarm
		}
	}
	if !talking {
		sess = b.startSession(now, "nova", reachOutTitle(i))
	}
	ev, err := b.S.AddEvent(store.Event{At: now, Kind: "initiative", Actor: cfg.CompanionName, Text: text, Importance: 2, Meta: meta, SessionID: sess.ID})
	if err != nil {
		return err
	}
	// A follow-up, or a face she asked about, stays "sent" until it is
	// answered or it expires. The other kinds are finished once said.
	status := "done"
	if i.Kind == "followup" || i.Kind == "face" {
		status = "sent"
	}
	b.S.SetIntentionStatus(i.ID, "sent", now)
	if status == "done" {
		b.S.SetIntentionStatus(i.ID, "done", now)
	}
	b.touch(&sess, now, "")
	b.Bus.Publish("message", ev)
	b.Bus.Publish("sessions", nil)
	return nil
}
