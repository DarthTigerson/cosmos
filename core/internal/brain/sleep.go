package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// Sleep is where a day becomes part of her. Awake, she only writes things
// down. Asleep, the largest model reads the day back, reflects on it,
// tidies her memory, and proposes changes to herself. Tidying is applied
// straight away with an undo. Changes to her values, her writing or her
// planning wait for the user's approval.

type SleepReport struct {
	At          time.Time `json:"at"`
	Journal     string    `json:"journal"`
	Reflections []string  `json:"reflections"`
	Tidied      int       `json:"tidied"`
	Proposals   int       `json:"proposals"`
	Skipped     string    `json:"skipped,omitempty"`
}

func (b *Brain) shouldSleep(cfg config.Config, now time.Time) bool {
	h := now.Hour()
	if h < cfg.SleepHour || h >= cfg.SleepHour+4 || b.S.Get("last_sleep_day") == dayKey(now) {
		return false
	}
	last, _ := b.S.RecentEvents(1, "user_msg")
	return len(last) == 0 || now.Sub(last[0].At) > 30*time.Minute
}

// Sleep runs a sleep now, whatever the hour.
func (b *Brain) Sleep(ctx context.Context) (SleepReport, error) {
	// The day's last reply is filed before she sleeps on it.
	b.after.Lock()
	defer b.after.Unlock()
	b.turn.Lock()
	defer b.turn.Unlock()
	return b.sleepLocked(ctx)
}

func (b *Brain) wake(now time.Time) {
	if b.Rest != nil {
		if err := b.Rest(context.Background(), false); err != nil {
			b.Log.Printf("sleep: resting the sleeper model: %v", err)
		}
	}
	b.S.Set("state", "awake")
	b.S.Set("awake_since", now.Format(time.RFC3339))
	b.Bus.Publish("state", "awake")
}

type reflectOut struct {
	Journal     string `json:"journal"`
	Reflections []struct {
		Evidence   []int  `json:"evidence"`
		Text       string `json:"text"`
		Importance int    `json:"importance"`
	} `json:"reflections"`
	Share  string `json:"share"`
	Wonder []struct {
		Question string `json:"question"`
		Why      string `json:"why"`
	} `json:"wonder"`
	Proposals []struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		After  string `json:"after"`
		Reason string `json:"reason"`
	} `json:"proposals"`
}

// runStats summarises how her plans went, per intent and planning mode.
func runStats(runs []store.Run) string {
	type agg struct{ n, rewrites, unverified int }
	by := map[string]*agg{}
	keys := []string{}
	for _, r := range runs {
		k := r.Intent + " / " + r.Mode
		if by[k] == nil {
			by[k] = &agg{}
			keys = append(keys, k)
		}
		by[k].n++
		by[k].rewrites += r.Rewrites
		if !r.Verified {
			by[k].unverified++
		}
	}
	if len(keys) == 0 {
		return "(no replies today)\n"
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		a := by[k]
		fmt.Fprintf(&sb, "- %s: %d replies, %d needed a rewrite, %d went out unverified\n", k, a.n, a.rewrites, a.unverified)
	}
	return sb.String()
}

func (b *Brain) sleepLocked(ctx context.Context) (SleepReport, error) {
	cfg := b.Cfg.Get()
	now := b.Now()
	report := SleepReport{At: now, Reflections: []string{}}

	since := now.Add(-24 * time.Hour)
	if raw := b.S.Get("last_sleep_at"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}
	finish := func() {
		// Faces she saw and does not know become questions for the
		// morning; pictures of strangers kept too long are let go.
		b.faceQuestions(now)
		// What she has slept on leaves the list of open sessions.
		b.closeSessions(ctx, now)
		b.S.Set("last_sleep_at", now.Format(time.RFC3339))
		b.S.Set("last_sleep_day", dayKey(now))
		b.wake(b.Now())
		b.Bus.Publish("proposals", nil)
	}

	events, err := b.S.EventsBetween(since, now.Add(time.Second))
	if err != nil {
		return report, err
	}
	lived := 0
	for _, e := range events {
		if e.Kind == "user_msg" || e.Kind == "nova_msg" || e.Kind == "initiative" {
			lived++
		}
	}
	if lived == 0 {
		report.Skipped = "nothing happened since the last sleep"
		// A quiet day still leaves books and podcasts waiting for the
		// night.
		if b.nightWork() {
			b.S.Set("state", "asleep")
			b.Bus.Publish("state", "asleep")
			if b.Rest != nil {
				if err := b.Rest(ctx, true); err != nil {
					b.Log.Printf("sleep: resting the waking models: %v", err)
				}
			}
			b.readAsleep(ctx)
			b.listenAsleep(ctx)
		}
		finish()
		return report, nil
	}

	b.S.Set("state", "asleep")
	b.Bus.Publish("state", "asleep")
	// The waking models make room for the sleeper before it loads.
	if b.Rest != nil {
		if err := b.Rest(ctx, true); err != nil {
			b.Log.Printf("sleep: resting the waking models: %v", err)
		}
	}

	// ----- a second look -----
	// Memory capture runs once, in passing, after each reply, and a small
	// model misses things. Asleep there is time to go back over what
	// mattered today. The same checks apply, so nothing is saved twice.
	second := 0
	for _, e := range events {
		if e.Kind == "user_msg" && e.Importance >= 3 && second < 30 {
			b.capture(ctx, e.Text, "", e.ID)
			second++
		}
	}

	// Pet names are sorted out here too: whose each one is. Awake she
	// only hears them; asleep she works out who they belong to.
	for _, n := range b.learnNicknames(ctx, cfg, events) {
		b.Log.Printf("sleep: learned that %q is a name for %s", n.Name, n.For)
	}

	// ----- reflect -----
	var stream strings.Builder
	if len(events) > 160 {
		events = events[len(events)-160:]
	}
	// Lines are numbered so each reflection can point at what it rests on.
	cited := []store.Event{}
	for _, e := range events {
		if e.Kind == "sleep" || e.Kind == "journal" {
			continue
		}
		cited = append(cited, e)
		fmt.Fprintf(&stream, "#%d %s [%s, importance %d] %s\n", len(cited), e.At.Format("15:04"), e.Kind, e.Importance, clip(e.Text, 240))
	}
	runs, _ := b.S.RunsSince(since)
	sent, _ := b.S.Intentions("sent", "done", "expired")
	var reach strings.Builder
	for _, i := range sent {
		if i.SentAt.Before(since) {
			continue
		}
		outcome := "no answer"
		if i.Answered {
			outcome = "answered"
		}
		fmt.Fprintf(&reach, "- (%s) %s: %s\n", i.Kind, clip(i.Text, 100), outcome)
	}
	if reach.Len() == 0 {
		reach.WriteString("(she did not reach out)\n")
	}
	vals, _ := b.S.Values()
	decided, _ := b.S.Proposals("", 10)
	var past strings.Builder
	for _, p := range decided {
		if p.Status != "pending" {
			fmt.Fprintf(&past, "- %s (%s): %s\n", p.Status, p.Kind, clip(p.After, 120))
		}
	}
	if past.Len() == 0 {
		past.WriteString("(none yet)\n")
	}
	style := strings.TrimSpace(b.S.Prompt("writer_style"))
	if style == "" {
		style = "(nothing learned yet)"
	}

	system := strings.Join([]string{
		"You are " + cfg.CompanionName + ", " + cfg.UserName + "'s companion, asleep. You are looking back over your day to make sense of it. Write in the first person, as yourself.",
		"",
		"Your values:",
		b.valuesList(),
		"How you have learned to write:",
		style,
		"",
		"What you know about " + cfg.UserName + ":",
		b.knownFacts(25),
		"Changes you proposed before and what " + cfg.UserName + " decided:",
		past.String(),
		"Give:",
		"Lines marked [mistake] are things you got wrong today, or that " + cfg.UserName + " corrected. Take them seriously: when the same kind of mistake shows up more than once, that is what a proposal is for.",
		"Lines marked [feedback] are replies " + cfg.UserName + " marked as good or not good. They are the best evidence you have of how to talk to them: when the good ones have something in common that the bad ones lack, that is a writing proposal.",
		"- journal: a short diary entry for the day, three to five sentences.",
		"- reflections: one or two things you now understand that you did not this morning: something concrete about " + cfg.UserName + ", or about how to be better company for them. One plain sentence each, in everyday words. No poetry, no statements about silence, safety or your purpose. For each, evidence lists the numbers of the lines of your day it comes from; a reflection you cannot point to a line for does not belong here. importance 1 to 5.",
		"- share: one thing you want to say or ask " + cfg.UserName + " in the morning, in one plain sentence, or \"\" if there is none worth it. Most nights it is \"\".",
		"- wonder: up to two questions you would like to ask " + cfg.UserName + " some time, about something in their life that came up today and that you do not know: who someone is, what they do, how something turned out, what they like. Ask the way a friend would: one short, plain question about something real. Good: \"What do you do for work?\", \"Who is Rex?\", \"How long have you been diving?\" Bad: \"What does that mean to you?\", or anything about feelings behind a word. Give why you want to know. Never ask what is already in what you know. An empty list is fine.",
		"- proposals: up to three changes to yourself, only where today gives a concrete reason. Do not repeat a declined proposal. A declined proposal means " + cfg.UserName + " said no to it: the opposite stands, so never read it as them wanting what it says.",
		`  kind "value": target is the number of the value to change, or "new". after is the new wording.`,
		`  kind "writing": target is "". after is one instruction for how you write.`,
		`  kind "planning": target is an intent (weather, calendar, memory, people, chat). after is "once" or "steps".`,
		`  kind "bug": something in the platform you run on that keeps failing, from "What failed today" below; only one that failed more than once, or that ` + cfg.UserName + ` noticed. target is "". after is one plain line for ` + cfg.UserName + ` to fix it from: which step failed, how often, and the reason it gave. Never a bug about ` + cfg.UserName + ` or about how you talk.`,
		"  reason: what happened today that makes you want this.",
		"Most nights there is nothing to propose. An empty list is a good answer.",
	}, "\n")
	user := "Your day:\n" + stream.String() + "\nHow your plans went:\n" + runStats(runs) + "\nWhat failed today:\n" + failureNotes(runs) + "\nWhen you reached out first:\n" + reach.String()

	var out reflectOut
	if err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "reflect", System: system,
		Messages: []llm.Message{{Role: "user", Content: user}},
		Schema:   reflectSchema, MaxTokens: 1200, Temperature: 0.6,
	}, &out); err != nil {
		// She did not sleep. Leave last_sleep untouched so tonight's
		// events are still there for the next attempt.
		b.wake(b.Now())
		return report, fmt.Errorf("reflection failed: %w", err)
	}

	if j := strings.TrimSpace(out.Journal); j != "" {
		report.Journal = j
		b.S.AddEvent(store.Event{At: now, Kind: "journal", Actor: cfg.CompanionName, Text: j, Importance: 3})
	}
	thought := map[string]bool{}
	for _, r := range out.Reflections {
		text := strings.TrimSpace(r.Text)
		if text == "" || thought[norm(text)] {
			continue
		}
		thought[norm(text)] = true
		// A reflection has to rest on moments that happened. Without that
		// it is the model musing, and those are the ones that sound grand
		// and mean little.
		var basis []string
		var first int64
		for _, n := range r.Evidence {
			if n >= 1 && n <= len(cited) {
				e := cited[n-1]
				if first == 0 {
					first = e.ID
				}
				basis = append(basis, e.At.Format("15:04")+" "+clip(e.Text, 140))
			}
		}
		if len(basis) == 0 {
			continue
		}
		b.S.AddMemory(store.Memory{Kind: "reflection", Category: "reflection", Content: text,
			Importance: int(clamp(float64(r.Importance), 1, 5)), CreatedAt: now,
			SourceEvent: first, Source: strings.Join(basis, "\n")})
		report.Reflections = append(report.Reflections, text)
	}
	if share := strings.TrimSpace(out.Share); share != "" {
		morning := dayStart(now).Add(time.Duration(cfg.QuietTo) * time.Hour)
		if !morning.After(now) {
			morning = morning.AddDate(0, 0, 1)
		}
		b.S.AddIntention(store.Intention{
			Kind: "reflection", Text: share, Reason: "It came to her while she slept.",
			TriggerAt: morning, ExpiresAt: morning.Add(12 * time.Hour), Importance: 3, CreatedAt: now,
		})
	}
	for _, w := range out.Wonder {
		// Asking what she already knows is not curiosity.
		if !knownAnswer(cfg, w.Question, b) {
			b.wonder(cfg, w.Question, w.Why)
		}
	}
	b.wonderAboutGaps(cfg)
	for _, p := range out.Proposals {
		if prop, ok := b.checkProposal(p.Kind, p.Target, p.After, p.Reason, vals); ok {
			prop.CreatedAt = now
			if _, err := b.S.AddProposal(prop); err == nil {
				report.Proposals++
			}
		}
	}

	// ----- read and listen -----
	// The sleeper is loaded and the night is quiet: the time for books
	// and podcasts.
	b.readAsleep(ctx)
	b.listenAsleep(ctx)

	// ----- tidy -----
	report.Tidied = b.tidy(ctx, cfg, now)

	b.S.AddEvent(store.Event{At: b.Now(), Kind: "sleep", Actor: cfg.CompanionName,
		Text: fmt.Sprintf("Slept. %d reflections, %d memories tidied, %d changes proposed.", len(report.Reflections), report.Tidied, report.Proposals)})
	finish()
	return report, nil
}

// checkProposal turns a model's proposal into a stored one, or rejects it
// when it does not point at something real.
func (b *Brain) checkProposal(kind, target, after, reason string, vals []store.Value) (store.Proposal, bool) {
	after = strings.TrimSpace(after)
	target = strings.TrimSpace(target)
	if after == "" {
		return store.Proposal{}, false
	}
	p := store.Proposal{Kind: kind, After: after, Reason: strings.TrimSpace(reason)}
	switch kind {
	case "value":
		if strings.EqualFold(target, "new") || target == "" {
			p.Target = "new"
			return p, true
		}
		n, err := strconv.Atoi(strings.TrimPrefix(target, "#"))
		if err != nil || n < 1 || n > len(vals) {
			return p, false
		}
		v := vals[n-1]
		if norm(v.Text) == norm(after) {
			return p, false
		}
		p.Target, p.Before = strconv.FormatInt(v.ID, 10), v.Text
		return p, true
	case "writing", "bug":
		return p, true
	case "planning":
		target = strings.ToLower(target)
		after = strings.ToLower(after)
		okIntent := false
		for _, i := range intents {
			if i == target {
				okIntent = true
			}
		}
		if !okIntent || (after != "once" && after != "steps") {
			return p, false
		}
		before := b.S.Get("planning." + target)
		if before == "" {
			before = "her own choice each time"
		}
		if before == after {
			return p, false
		}
		p.Target, p.After, p.Before = target, after, before
		return p, true
	}
	return p, false
}

// bugList is where the bugs she reports go once approved: the list
// the user keeps for work on the platform she runs on.
const bugList = "cosmos platform tasks"

// DecideProposal applies or declines a pending proposal.
func (b *Brain) DecideProposal(id int64, approve bool) (store.Proposal, error) {
	p, err := b.S.GetProposal(id)
	if err != nil {
		return p, errors.New("proposal not found")
	}
	if p.Status != "pending" {
		return p, errors.New("this proposal was already decided")
	}
	now := b.Now()
	cfg := b.Cfg.Get()
	if p.Kind == "bug" {
		// A bug report is not a rule about her: approved, it goes on the
		// list of things to fix in the platform.
		status, text := "declined", cfg.UserName+" did not take up a bug I reported: "+clip(p.After, 160)
		if approve {
			if _, err := b.S.AddListItem(listName(bugList), "Bug: "+clip(p.After, 200), now); err != nil {
				return p, err
			}
			b.Bus.Publish("lists", nil)
			status, text = "approved", cfg.UserName+" put a bug I reported on the "+bugList+" list: "+clip(p.After, 160)
		}
		b.S.DecideProposal(id, status, now)
		b.S.AddEvent(store.Event{At: now, Kind: "system", Actor: cfg.UserName, Text: text, Importance: 3})
		p.Status = status
		b.Bus.Publish("proposals", nil)
		return p, nil
	}
	if !approve {
		b.S.DecideProposal(id, "declined", now)
		b.S.AddEvent(store.Event{At: now, Kind: "system", Actor: cfg.UserName, Text: cfg.UserName + " said no to a rule I proposed, so this is NOT how they want me to behave: " + clip(p.After, 160), Importance: 3})
		p.Status = "declined"
		b.Bus.Publish("proposals", nil)
		return p, nil
	}
	switch p.Kind {
	case "value":
		if p.Target == "new" {
			_, err = b.S.AddValue(p.After, "hers", now)
		} else {
			vid, _ := strconv.ParseInt(p.Target, 10, 64)
			err = b.S.UpdateValue(vid, p.After, "hers", now)
		}
	case "writing":
		lines := []string{}
		for _, l := range strings.Split(b.S.Prompt("writer_style"), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		lines = append(lines, "- "+p.After)
		if len(lines) > 8 {
			lines = lines[len(lines)-8:]
		}
		err = b.S.SetPrompt("writer_style", strings.Join(lines, "\n"), now)
	case "planning":
		err = b.S.Set("planning."+p.Target, p.After)
	default:
		err = errors.New("unknown kind of proposal")
	}
	if err != nil {
		return p, err
	}
	b.S.DecideProposal(id, "approved", now)
	b.S.AddEvent(store.Event{At: now, Kind: "system", Actor: cfg.UserName, Text: cfg.UserName + " said yes to a change I proposed, so this is now how they want me to behave: " + clip(p.After, 160), Importance: 4})
	p.Status = "approved"
	b.Bus.Publish("proposals", nil)
	return p, nil
}

// ---------- tidying memory ----------

type tidyUndo struct {
	Type        string  `json:"type"` // merge, archive
	KeepID      int64   `json:"keep_id,omitempty"`
	OldContent  string  `json:"old_content,omitempty"`
	OldCategory string  `json:"old_category,omitempty"`
	IDs         []int64 `json:"ids"`
}

// tidy merges duplicate memories and archives ones that no longer hold.
// Nothing is deleted: every change is logged with what is needed to undo.
func (b *Brain) tidy(ctx context.Context, cfg config.Config, now time.Time) int {
	all, _ := b.S.Memories()
	facts := []store.Memory{}
	for _, m := range all {
		if m.Kind == "fact" {
			facts = append(facts, m)
		}
	}
	if len(facts) < 8 {
		return 0
	}
	if len(facts) > 80 {
		facts = facts[len(facts)-80:]
	}
	var list strings.Builder
	for i, m := range facts {
		fmt.Fprintf(&list, "#%d [%s] %s\n", i+1, m.Category, m.Content)
	}
	var out struct {
		Merge []struct {
			Keep    int    `json:"keep"`
			Drop    []int  `json:"drop"`
			Content string `json:"content"`
		} `json:"merge"`
		Archive []struct {
			Ref int    `json:"ref"`
			Why string `json:"why"`
		} `json:"archive"`
	}
	err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "tidy",
		System: strings.Join([]string{
			"You tidy " + cfg.CompanionName + "'s memory of facts about " + cfg.UserName + ".",
			"- merge: memories that say the same thing. keep one number, drop the others, and give the combined wording. Never lose a detail.",
			"- archive: a memory that a newer one clearly replaces. Say why.",
			"Change as little as possible. If the list is clean, return empty lists.",
		}, "\n"),
		Messages: []llm.Message{{Role: "user", Content: list.String()}},
		Schema:   tidySchema, MaxTokens: 600,
	}, &out)
	if err != nil {
		b.Log.Printf("tidy: %v", err)
		return 0
	}
	valid := func(n int) bool { return n >= 1 && n <= len(facts) }
	touched := map[int]bool{}
	count := 0
	for _, m := range out.Merge {
		content := strings.TrimSpace(m.Content)
		if !valid(m.Keep) || touched[m.Keep] || content == "" {
			continue
		}
		undo := tidyUndo{Type: "merge", KeepID: facts[m.Keep-1].ID, OldContent: facts[m.Keep-1].Content, OldCategory: facts[m.Keep-1].Category, IDs: []int64{}}
		for _, d := range m.Drop {
			if valid(d) && d != m.Keep && !touched[d] {
				touched[d] = true
				undo.IDs = append(undo.IDs, facts[d-1].ID)
			}
		}
		if len(undo.IDs) == 0 {
			continue
		}
		touched[m.Keep] = true
		b.S.UpdateMemory(undo.KeepID, undo.OldCategory, content, now)
		for _, id := range undo.IDs {
			b.S.SetMemoryArchived(id, true)
		}
		b.S.AddTidy(now, fmt.Sprintf("Merged %d memories into: %s", len(undo.IDs)+1, clip(content, 100)), undo)
		count += len(undo.IDs)
	}
	for _, a := range out.Archive {
		if !valid(a.Ref) || touched[a.Ref] {
			continue
		}
		touched[a.Ref] = true
		m := facts[a.Ref-1]
		b.S.SetMemoryArchived(m.ID, true)
		b.S.AddTidy(now, "Set aside: "+clip(m.Content, 100)+" ("+clip(a.Why, 80)+")", tidyUndo{Type: "archive", IDs: []int64{m.ID}})
		count++
	}
	return count
}

// UndoTidy reverses one thing sleep tidied.
func (b *Brain) UndoTidy(id int64) error {
	t, err := b.S.GetTidy(id)
	if err != nil {
		return errors.New("not found")
	}
	if t.Undone {
		return errors.New("already undone")
	}
	var u tidyUndo
	if err := json.Unmarshal(t.Undo, &u); err != nil {
		return err
	}
	for _, mid := range u.IDs {
		b.S.SetMemoryArchived(mid, false)
	}
	if u.Type == "merge" {
		b.S.UpdateMemory(u.KeepID, u.OldCategory, u.OldContent, b.Now())
	}
	return b.S.MarkTidyUndone(id)
}
