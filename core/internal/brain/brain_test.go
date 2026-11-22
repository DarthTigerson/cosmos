package brain

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/mcp"
	"cosmos/core/internal/store"
)

type fakeWeather struct{ w Weather }

func (f fakeWeather) Fetch(context.Context, string) (Weather, error) { return f.w, nil }

type rig struct {
	b    *Brain
	fake *llm.Fake
	now  time.Time
}

// newRig builds a brain on an in-memory database with a scripted model
// and a clock the test controls. It starts on a Friday at 09:00.
func newRig(t *testing.T) *rig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cfg, _ := config.Load("")
	c := cfg.Get()
	c.Location = "Lisbon"
	// The tests were written with these names; nothing else assumes them.
	c.UserName, c.CompanionName = "Thomas", "Nova"
	cfg.Set(c)
	r := &rig{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)}
	r.fake = &llm.Fake{Handlers: map[string]func(llm.Request) (string, error){
		"route":   func(llm.Request) (string, error) { return `{"intent":"chat","mode":"once","steps":[]}`, nil },
		"claims":  func(llm.Request) (string, error) { return `{"invented":""}`, nil },
		"summary": func(llm.Request) (string, error) { return "You told me about your day.", nil },
		"notice":  func(llm.Request) (string, error) { return calmNotice, nil },
		"writer":  func(llm.Request) (string, error) { return "Sounds good.", nil },
		"verify":  func(llm.Request) (string, error) { return `{"ok":true,"reason":""}`, nil },
		"capture": func(llm.Request) (string, error) { return `{"changes":[],"people":[]}`, nil },
	}}
	r.b = New(s, r.fake, cfg)
	r.b.Now = func() time.Time { return r.now }
	r.b.Weather = fakeWeather{Weather{Place: "Lisbon, Portugal", TempC: 13.6, WindKph: 38.2, Sky: "clear sky", TodayMinC: 11, TodayMaxC: 16}}
	r.b.seedClock()
	return r
}

// seedClock re-stamps the state New wrote with the real clock.
func (b *Brain) seedClock() { b.S.Set("awake_since", b.Now().Format(time.RFC3339)) }

const calmNotice = `{"emotion":"calm","intensity":0.1,"touches":"","warmth":0,"energy":0,"confidence":0,"importance":1,"follow_up":{"text":"","reason":"","when":"none"},"resolved":[]}`

// say sends a message and waits for everything that follows the reply:
// how it landed on her, what she remembers from it.
func (r *rig) say(text string) (Reply, error) {
	reply, err := r.b.HandleMessage(context.Background(), text)
	r.b.Wait()
	return reply, err
}

// finished waits for the work after a reply and reloads its run, which by
// then includes the feeling step.
func (r *rig) finished(reply Reply) store.Run {
	r.b.Wait()
	run, _ := r.b.S.GetRun(reply.Run.ID)
	return run
}

func stepNames(run store.Run) string {
	names := []string{}
	for _, s := range run.Steps {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

func TestMoodMovesAndDriftsBack(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	m := baseline
	m.UpdatedAt = now
	m = applied(m, Appraisal{Emotion: "Worry", Intensity: 1, Warmth: 1, Energy: -1}, now)
	if m.Warmth <= baseline.Warmth || m.Energy >= baseline.Energy {
		t.Fatalf("mood did not move: %+v", m)
	}
	if got := m.Describe(now); got != "worry" {
		t.Fatalf("feeling = %q, want worry", got)
	}
	later := decayed(m, now.Add(moodHalfLife))
	wantWarmth := baseline.Warmth + (m.Warmth-baseline.Warmth)/2
	if diff := later.Warmth - wantWarmth; diff > 0.001 || diff < -0.001 {
		t.Fatalf("after one half-life warmth = %v, want %v", later.Warmth, wantWarmth)
	}
	if got := later.Describe(now.Add(moodHalfLife)); got == "worry" {
		t.Fatal("the feeling should have faded after six hours")
	}
}

func TestMoodDoesNotPinAtTheTopInAFriendlyConversation(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	m := baseline
	m.UpdatedAt = now
	for i := 0; i < 40; i++ {
		now = now.Add(30 * time.Second)
		m = applied(m, Appraisal{Emotion: "glad", Intensity: 0.5, Warmth: 1, Energy: 1, Confidence: 1}, now)
	}
	if m.Warmth >= 0.95 || m.Warmth <= 0.75 {
		t.Fatalf("after forty warm moments warmth should be high but not maxed, got %.2f", m.Warmth)
	}
}

func TestSheKnowsWhoSheIsTalkingTo(t *testing.T) {
	r := newRig(t)
	sys, _ := r.b.writerSystem(r.b.Cfg.Get(), r.b.Mood(), "what is my name")
	if !strings.Contains(sys, "The person you are talking with is Thomas") {
		t.Fatalf("the writer is not told who the user is:\n%s", sys)
	}
	res, err := r.b.runTool(context.Background(), StepCall{Tool: "people.find", Name: "thomas"})
	if err != nil || res.(map[string]any)["known"] != true {
		t.Fatalf("looking up the user's own name should find them: %v %v", res, err)
	}
}

func TestFactsAboutHerselfAreNotStoredAsMemories(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"personal","content":"Nova is someone Thomas talks with every day."}],"people":[]}`, nil
	}
	if _, err := r.say("Nova, I talk with you every day."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if mems, _ := r.b.S.Memories(); len(mems) != 0 {
		t.Fatalf("a fact about her was stored: %+v", mems)
	}
}

func TestALaterCloserRelationshipReplacesAnEmptyOne(t *testing.T) {
	r := newRig(t)
	r.b.rememberPerson("Bethany", "Unknown", "Mentioned by Thomas.", r.now)
	p, _ := r.b.S.FindPerson("Bethany")
	if p.Relationship != "" || p.Notes != "" {
		t.Fatalf("a placeholder relationship and an empty note should not be stored: %+v", p)
	}
	r.b.rememberPerson("Bethany Bonnici", "wife", "Not very technical.", r.now)
	people, _ := r.b.S.People()
	if len(people) != 1 {
		t.Fatalf("the full name should be recognised as the same person, got %+v", people)
	}
	p = people[0]
	if p.Name != "Bethany Bonnici" || p.Relationship != "wife" || p.Closeness != 0.9 || len(p.Aliases) != 1 || p.Aliases[0] != "Bethany" {
		t.Fatalf("want Bethany Bonnici, wife, very close, also called Bethany; got %+v", p)
	}
}

func TestNotKnowingSomethingIsNotAMemory(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"personal","content":"Thomas's name is unknown."}],"people":[]}`, nil
	}
	if _, err := r.say("what is my name?"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if mems, _ := r.b.S.Memories(); len(mems) != 0 {
		t.Fatalf("a question must not become a memory: %+v", mems)
	}
}

func TestRouteInstructionsDoNotChangeWithTheClock(t *testing.T) {
	r := newRig(t)
	before := r.b.routeSystem(r.b.Cfg.Get())
	r.now = r.now.Add(7 * time.Minute)
	if r.b.routeSystem(r.b.Cfg.Get()) != before {
		t.Fatal("the router's instructions changed within the same day, which defeats the model's cache")
	}
}

func TestSleepTakesASecondLookAtWhatMattered(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["notice"] = func(llm.Request) (string, error) {
		return strings.Replace(calmNotice, `"importance":1`, `"importance":4`, 1), nil
	}
	seen := 0
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		seen++
		if seen == 1 {
			return `{"changes":[],"people":[]}`, nil // missed the first time
		}
		return `{"changes":[{"op":"add","ref":0,"category":"family","content":"Thomas's wife is called Bethany."}],"people":[{"name":"Bethany","relationship":"wife","note":""}]}`, nil
	}
	r.fake.Handlers["reflect"] = func(llm.Request) (string, error) {
		return `{"journal":"","reflections":[],"share":"","proposals":[]}`, nil
	}
	if _, err := r.say("My wife is called Bethany."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if mems, _ := r.b.S.Memories(); len(mems) != 0 {
		t.Fatal("setup: the first capture should have missed it")
	}
	if _, err := r.b.Sleep(context.Background()); err != nil {
		t.Fatal(err)
	}
	mems, _ := r.b.S.Memories()
	if p, ok := r.b.S.FindPerson("Bethany"); len(mems) != 1 || !ok || p.Relationship != "wife" {
		t.Fatalf("sleep should have recovered the fact and the person: %+v %+v", mems, p)
	}
}

func TestAPronounIsResolvedFromTheLinesBefore(t *testing.T) {
	r := newRig(t)
	r.b.rememberPerson("Bethany", "wife", "", r.now)
	sess := r.b.NewSession()
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "user_msg", Text: "Bethany is my wife.", SessionID: sess.ID})
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "nova_msg", Text: "Tell me about her.", SessionID: sess.ID})
	r.fake.Handlers["capture"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[0].Content, "Bethany is my wife.") {
			t.Error("capture was not shown the lines before the message")
		}
		return `{"changes":[{"op":"add","ref":0,"category":"family","content":"Bethany is not very technical."}],"people":[{"name":"Bethany","relationship":"wife","note":"Not very technical."}]}`, nil
	}
	if _, err := r.say("no she isnt very technical"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	p, _ := r.b.S.FindPerson("Bethany")
	if len(mems) != 1 || p.Notes != "Not very technical." {
		t.Fatalf("the fact about \"she\" should be saved against Bethany: %+v %+v", mems, p)
	}
}

func TestAGuessedRelationshipIsNotStored(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[],"people":[{"name":"Bethany","relationship":"sister","note":""}]}`, nil
	}
	if _, err := r.say("Who is Bethany?"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if p, ok := r.b.S.FindPerson("Bethany"); !ok || p.Relationship != "" {
		t.Fatalf("she may note the name, but not a relationship nobody stated: %+v", p)
	}
}

func TestAnUpdateCanAddADetailToAFact(t *testing.T) {
	r := newRig(t)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas has a wife called Bethany.", CreatedAt: r.now})
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "user_msg", Text: "Bethany is my wife.", SessionID: r.b.NewSession().ID})
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"update","ref":1,"category":"family","content":"Thomas has a wife called Bethany, who is not very technical."}],"people":[]}`, nil
	}
	if _, err := r.say("no she isnt very technical"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	if len(mems) != 1 || !strings.Contains(mems[0].Content, "not very technical") {
		t.Fatalf("the detail should have been added to the existing fact: %+v", mems)
	}
}

func TestARelationGuessedFromAQuestionIsNotAMemory(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"family","content":"Thomas has a brother called Link Bonnici."}],"people":[{"name":"Link Bonnici","relationship":"brother","note":""}]}`, nil
	}
	if _, err := r.say("question 4. who is Link Bonnici and Atreus Bonnici"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if mems, _ := r.b.S.Memories(); len(mems) != 0 {
		t.Fatalf("the names are in the question but \"brother\" is not: %+v", mems)
	}
}

func TestAChildIntroducedWithoutTheWordSon(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"family","content":"Thomas's son Link Bonnici is 7 years old."}],
		  "people":[{"name":"Link Bonnici","relationship":"son","note":"7 years old."},{"name":"Atreus","relationship":"son","note":"7 months old."}]}`, nil
	}
	if _, err := r.say("Link Bonnici is mine and Boos 7 year old boy. Atreus is our 7month old"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	link, _ := r.b.S.FindPerson("Link Bonnici")
	atreus, _ := r.b.S.FindPerson("Atreus")
	mems, _ := r.b.S.Memories()
	if link.Relationship != "son" || link.Closeness != 0.9 || atreus.Relationship != "son" || len(mems) != 1 {
		t.Fatalf("children described by age should be kept as children: %+v %+v %+v", link, atreus, mems)
	}
}

func TestReflectionsDoNotCrowdOutFacts(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 8; i++ {
		r.b.S.AddMemory(store.Memory{Kind: "reflection", Content: "A lofty thought number " + string(rune('A'+i)) + ".", CreatedAt: r.now})
	}
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas's wife is Bethany.", CreatedAt: r.now})
	sys, _ := r.b.writerSystem(r.b.Cfg.Get(), r.b.Mood(), "hello")
	if strings.Count(sys, "A lofty thought") != 2 || !strings.Contains(sys, "Thomas's wife is Bethany.") {
		t.Fatalf("want the fact and only two reflections in the prompt:\n%s", sys)
	}
}

func TestAnInventedFactAboutTheUserIsCaught(t *testing.T) {
	r := newRig(t)
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			return "So Link and Atreus are co-founders of the company.", nil
		}
		if !strings.Contains(q.System, "co-founders of the company") {
			t.Error("the rewrite was not told what was invented")
		}
		return "Who are Link and Atreus to you?", nil
	}
	r.fake.Handlers["claims"] = func(q llm.Request) (string, error) {
		if strings.Contains(q.Messages[0].Content, "Draft reply:\nWho are") {
			t.Error("the second draft should not be checked again")
		}
		return `{"invented":"co-founders of the company"}`, nil
	}
	reply, err := r.say("I made 50% of them :P")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "Who are Link and Atreus to you?" || reply.Run.Rewrites != 1 || !reply.Run.Verified {
		t.Fatalf("want the question after one rewrite, got %q %+v", reply.Text, reply.Run)
	}
}

func TestACorrectReplyFlaggedByTheCheckerIsLeftAlone(t *testing.T) {
	r := newRig(t)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas has a wife named Bethany.", CreatedAt: r.now})
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "You have a wife named Bethany.", nil }
	r.fake.Handlers["claims"] = func(llm.Request) (string, error) { return `{"invented":"You have a wife named Bethany."}`, nil }
	reply, err := r.say("what do you know about my family?")
	if err != nil || reply.Run.Rewrites != 0 || !reply.Run.Verified {
		t.Fatalf("everything in that reply is in her notes, so it must stand: %+v %v", reply.Run, err)
	}
}

func TestACheckerThatMisquotesIsIgnored(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["claims"] = func(llm.Request) (string, error) { return `{"invented":"you own a bakery"}`, nil }
	reply, err := r.say("hello")
	if err != nil || reply.Run.Rewrites != 0 || !reply.Run.Verified {
		t.Fatalf("a quote that is not in the draft must not cost a rewrite: %+v %v", reply.Run, err)
	}
}

func TestMemoriesKeepTheirSourceAndTheirHistory(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"personal","content":"Thomas lives in Munich."}],"people":[]}`, nil
	}
	if _, err := r.say("I live in Munich."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"update","ref":1,"category":"personal","content":"Thomas lives in Berlin."}],"people":[]}`, nil
	}
	if _, err := r.say("I moved, I live in Berlin now."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	if len(mems) != 1 || mems[0].Content != "Thomas lives in Berlin." || mems[0].Source != "I moved, I live in Berlin now." || mems[0].SourceEvent == 0 {
		t.Fatalf("the current memory should carry the message it came from: %+v", mems)
	}
	replaced, _ := r.b.S.Replaced()
	if replaced[mems[0].ID] != "Thomas lives in Munich." {
		t.Fatalf("what she believed before should be kept: %+v", replaced)
	}
}

func TestHistoryKeepsEveryEarlierWording(t *testing.T) {
	r := newRig(t)
	a, _ := r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas lives in Munich.", CreatedAt: r.now.Add(-48 * time.Hour)})
	b, _ := r.b.S.Supersede(a.ID, store.Memory{Kind: "fact", Content: "Thomas lives in Berlin.", CreatedAt: r.now.Add(-24 * time.Hour)})
	c, _ := r.b.S.Supersede(b.ID, store.Memory{Kind: "fact", Content: "Thomas lives in Lisbon.", CreatedAt: r.now})
	history, _ := r.b.S.Histories()
	got := history[c.ID]
	if len(got) != 2 || got[0].Content != "Thomas lives in Berlin." || got[1].Content != "Thomas lives in Munich." {
		t.Fatalf("every earlier wording should be kept, newest first: %+v", got)
	}
	// Replaced memories are history, not forgotten.
	r.b.S.SetMemoryArchived(c.ID, true)
	forgotten, _ := r.b.S.ForgottenMemories()
	if len(forgotten) != 1 || forgotten[0].ID != c.ID {
		t.Fatalf("only the memory put away should be forgotten: %+v", forgotten)
	}
}

func TestPinnedMemoriesAreAlwaysRecalled(t *testing.T) {
	r := newRig(t)
	mems := []store.Memory{}
	for i := range 30 {
		m, _ := r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas likes coffee number " + string(rune('A'+i)) + ".", CreatedAt: r.now.Add(time.Duration(i) * time.Minute)})
		mems = append(mems, m)
	}
	pin, _ := r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas has hay fever in spring.", Importance: store.Pinned, CreatedAt: r.now.Add(-time.Hour)})
	mems = append(mems, pin)
	for _, picked := range [][]store.Memory{rankMemories(mems, "coffee", r.now, 5), r.b.rank(mems, "coffee", r.now, 5)} {
		if len(picked) != 5 || picked[0].ID != pin.ID {
			t.Fatalf("a pinned memory should always be recalled, oldest first: %+v", picked)
		}
	}
}

func TestRestatingAFactLeavesNoHistory(t *testing.T) {
	r := newRig(t)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas lives in Berlin.", CreatedAt: r.now})
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"update","ref":1,"category":"personal","content":"Thomas lives in Berlin."}],"people":[]}`, nil
	}
	if _, err := r.say("yes I still live in Berlin"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if replaced, _ := r.b.S.Replaced(); len(replaced) != 0 {
		t.Fatalf("nothing changed, so nothing should be recorded as replaced: %+v", replaced)
	}
}

func TestExamplesAreInHerPromptAndCanBeReplaced(t *testing.T) {
	r := newRig(t)
	sys, _ := r.b.writerSystem(r.b.Cfg.Get(), r.b.Mood(), "hi")
	if !strings.Contains(sys, "Thomas: you silly head\nNova: Guilty.") {
		t.Fatalf("the default examples, with names filled in, should be in the prompt:\n%s", sys[:600])
	}
	r.b.S.SetPrompt("examples", "{user}: oi\n{name}: Oi yourself.", r.now)
	sys, _ = r.b.writerSystem(r.b.Cfg.Get(), r.b.Mood(), "hi")
	if !strings.Contains(sys, "Nova: Oi yourself.") || strings.Contains(sys, "Guilty") {
		t.Fatal("the user's own examples should replace the defaults")
	}
}

func TestANewSessionStartsCleanButSheRemembers(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"family","content":"Thomas has a son called Link."}],"people":[]}`, nil
	}
	if _, err := r.say("I have a son called Link."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	r.b.NewSession()
	var sys string
	var msgs int
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		sys, msgs = q.System+"\n"+q.Messages[len(q.Messages)-1].Content, len(q.Messages)
		return "A morning at the harbour?", nil
	}
	r.now = r.now.Add(5 * time.Minute)
	if _, err := r.say("new topic: what should we do this weekend?"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if msgs != 1 {
		t.Fatalf("the new session should carry no earlier thread, got %d messages", msgs)
	}
	if !strings.Contains(sys, "Thomas has a son called Link.") {
		t.Fatal("her memories should come with her into the new session")
	}
	if !strings.Contains(sys, "Earlier, in other conversations") || !strings.Contains(sys, "I have a son called Link.") {
		t.Fatalf("she should be told what the earlier session was about:\n%s", sys[len(sys)-500:])
	}
	open, _ := r.b.S.OpenSessions()
	if len(open) != 2 || open[0].Title != "new topic: what should we do this weekend?" || open[1].Summary != "You told me about your day." {
		t.Fatalf("want two open sessions, the older one summarised: %+v", open)
	}
}

func TestLongSilenceStartsANewSession(t *testing.T) {
	r := newRig(t)
	r.say("morning")
	r.now = r.now.Add(30 * time.Minute)
	r.say("still here")
	r.now = r.now.Add(3 * time.Hour)
	r.say("afternoon")
	r.b.Wait()
	open, _ := r.b.S.OpenSessions()
	if len(open) != 2 || open[1].Messages != 4 || open[0].Messages != 2 {
		t.Fatalf("want a morning session of 4 messages and an afternoon one of 2: %+v", open)
	}
}

func TestAReopenedSessionIsContinued(t *testing.T) {
	r := newRig(t)
	r.say("first thing")
	first, _ := r.b.CurrentSession()
	r.b.NewSession()
	r.say("second thing")
	r.now = r.now.Add(5 * time.Hour)
	if _, ok := r.b.OpenSession(first.ID); !ok {
		t.Fatal("an unslept session should reopen")
	}
	r.say("back to the first thing")
	r.b.Wait()
	again, _ := r.b.S.GetSession(first.ID)
	if again.Messages != 4 {
		t.Fatalf("the message should have gone into the reopened session, which has %d", again.Messages)
	}
}

func TestSleepClosesTheOpenSessions(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["reflect"] = func(llm.Request) (string, error) {
		return `{"journal":"A day.","reflections":[],"share":"","proposals":[]}`, nil
	}
	r.say("hello there")
	r.b.Wait()
	if _, err := r.b.Sleep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if open, _ := r.b.S.OpenSessions(); len(open) != 0 {
		t.Fatalf("nothing should be open after sleep: %+v", open)
	}
	closed, _ := r.b.S.SessionsSleptAt(r.now)
	if len(closed) != 1 || closed[0].Summary == "" {
		t.Fatalf("the slept-on session should be kept, with a summary: %+v", closed)
	}
	if _, ok := r.b.OpenSession(closed[0].ID); ok {
		t.Fatal("a slept-on session must not reopen")
	}
	r.now = r.now.Add(time.Minute)
	r.say("good morning")
	if open, _ := r.b.S.OpenSessions(); len(open) != 1 || open[0].Title != "good morning" {
		t.Fatalf("the first message after sleep should start a session: %+v", open)
	}
}

func TestWhenSheSpeaksFirstSheOpensHerOwnSession(t *testing.T) {
	r := newRig(t)
	r.b.S.Set("last_brief_day", dayKey(r.now))
	r.b.S.AddIntention(store.Intention{Kind: "followup", Text: "Ask about the dentist", TriggerAt: r.now, ExpiresAt: r.now.Add(time.Hour), CreatedAt: r.now})
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Did you call the dentist?", nil }
	r.b.Tick(context.Background())
	open, _ := r.b.S.OpenSessions()
	if len(open) != 1 || open[0].StartedBy != "nova" || open[0].Title != "Checking in" {
		t.Fatalf("want one session she started: %+v", open)
	}
	r.now = r.now.Add(2 * time.Minute)
	r.say("not yet")
	if again, _ := r.b.S.GetSession(open[0].ID); again.Messages != 3 {
		t.Fatalf("the answer belongs in her session, which has %d messages", again.Messages)
	}
}

func TestMessagesFromBeforeSessionsAreGivenSessions(t *testing.T) {
	r := newRig(t)
	t0 := r.now.Add(-6 * time.Hour)
	r.b.S.AddEvent(store.Event{At: t0, Kind: "user_msg", Text: "old morning chat"})
	r.b.S.AddEvent(store.Event{At: t0.Add(time.Minute), Kind: "nova_msg", Text: "yes"})
	r.b.S.AddEvent(store.Event{At: t0.Add(2 * time.Minute), Kind: "sleep", Text: "Slept."})
	r.b.S.Set("last_sleep_at", t0.Add(2*time.Minute).Format(time.RFC3339))
	r.b.S.AddEvent(store.Event{At: r.now.Add(-10 * time.Minute), Kind: "user_msg", Text: "after the nap"})
	r.b.backfillSessions()
	open, _ := r.b.S.OpenSessions()
	if len(open) != 1 || open[0].Title != "after the nap" || open[0].Messages != 1 {
		t.Fatalf("only the session since the last sleep should be open: %+v", open)
	}
	if cur, ok := r.b.CurrentSession(); !ok || cur.ID != open[0].ID {
		t.Fatal("the open session should be current")
	}
}

func TestAPromiseToAddIsMadeTrue(t *testing.T) {
	r := newRig(t)
	sess := r.b.NewSession()
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "user_msg", Text: "do you know when my birthday is? it should be a repeated calendar entry", SessionID: sess.ID})
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "nova_msg", Text: "No, when is it?", SessionID: sess.ID})
	routes := 0
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		routes++
		if routes == 1 {
			return `{"intent":"chat","mode":"once","steps":[]}`, nil // the miss that happened for real
		}
		if !strings.Contains(q.Messages[0].Content, "intends to reply") {
			t.Error("the second plan was not told what she promised")
		}
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Thomas's birthday","date":"1994-08-03","time":"","duration_min":0,"location":"","kind":"event","repeat":"yearly"}]}`, nil
	}
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			return "So I'll add August 3rd as a repeated entry for your birthday.", nil
		}
		if !strings.Contains(q.Messages[len(q.Messages)-1].Content, `"repeats": "yearly"`) {
			t.Error("the second draft was not shown what was saved")
		}
		return "Done. Your birthday is in the calendar, every year on the 3rd of August.", nil
	}
	reply, err := r.say("i was born on the 3rd of August 1994")
	if err != nil {
		t.Fatal(err)
	}
	// Next August, and the one after: it repeats.
	items, _ := r.b.S.CalendarBetween(time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2029, 1, 1, 0, 0, 0, 0, time.Local))
	if len(items) != 2 || items[0].At.Format("2006-01-02") != "2027-08-03" || items[1].At.Format("2006-01-02") != "2028-08-03" {
		t.Fatalf("the birthday should fall on 3 August every year: %+v", items)
	}
	if !strings.HasPrefix(reply.Text, "Done.") || !reply.Run.Verified || reply.Run.Intent != "calendar" {
		t.Fatalf("want the confirmed reply: %q %+v", reply.Text, reply.Run)
	}
}

func TestAPromiseThatCannotBeKeptIsNotVerified(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "I'll add that to your calendar.", nil }
	reply, err := r.say("ok")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Run.Verified {
		t.Fatal("a reply promising a calendar entry that was never made must not be marked verified")
	}
	if items, _ := r.b.S.CalendarBetween(r.now.AddDate(-1, 0, 0), r.now.AddDate(2, 0, 0)); len(items) != 0 {
		t.Fatalf("nothing should have been added: %+v", items)
	}
}

func TestRepeatingReminderMovesOnAfterFiring(t *testing.T) {
	r := newRig(t)
	r.b.S.Set("last_brief_day", dayKey(r.now))
	res, err := r.b.runTool(context.Background(), StepCall{Tool: "calendar.add", Title: "Take the bins out", Date: "2026-11-01", Time: "08:30", Kind: "reminder", Repeat: "weekly"})
	if err != nil {
		t.Fatal(err, res)
	}
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Bins.", nil }
	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 0 {
		t.Fatal("a repeating reminder whose first date is past must not fire the moment it is made")
	}
	r.now = time.Date(2026, 11, 22, 8, 31, 0, 0, time.Local) // the next Sunday
	r.b.S.Set("last_brief_day", dayKey(r.now))
	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 1 {
		t.Fatalf("it should fire on its next occurrence, got %d", n)
	}
	r.now = r.now.Add(2 * time.Hour)
	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 1 {
		t.Fatal("and only once per occurrence")
	}
}

func TestABirthdayGivenAsADateOfBirthIsAdded(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Thomas's birthday","date":"1994-08-03","time":"","duration_min":0,"location":"","kind":"event","repeat":"yearly"}]}`, nil
	}
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Saved, every year on the 3rd of August.", nil }
	// A fresh session, and the word "birthday" is never used.
	reply, err := r.say("so I was born on the 3rd of august 1994")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := r.b.S.AllCalendar()
	if len(all) != 1 || all[0].Repeat != "yearly" || !reply.Run.Steps[1].OK {
		t.Fatalf("the entry should be added: the date saved is the date they gave. %+v %+v", all, reply.Run.Steps[1])
	}
}

func TestTryAgainTellsThePlannerWhatFailed(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Dentist","date":"not-a-date","time":"","duration_min":0,"location":"","kind":"event","repeat":"none"}]}`, nil
	}
	if _, err := r.say("put the dentist in the calendar for the 3rd of December"); err != nil {
		t.Fatal(err)
	}
	var seen string
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		seen = q.Messages[0].Content
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Dentist","date":"2026-12-03","time":"","duration_min":0,"location":"","kind":"event","repeat":"none"}]}`, nil
	}
	r.now = r.now.Add(time.Minute)
	if _, err := r.say("the calendar failed to create. do you want to try again"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "These tool steps failed earlier") || !strings.Contains(seen, `"title":"Dentist"`) {
		t.Fatalf("the planner was not told what failed:\n%s", seen)
	}
	if all, _ := r.b.S.AllCalendar(); len(all) != 1 {
		t.Fatalf("the retry should have added the entry: %+v", all)
	}
}

func TestNothingIsAddedWithoutADate(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Thomas's birthday","date":"2026-11-20","time":"","duration_min":0,"location":"","kind":"event","repeat":"yearly"}]}`, nil
	}
	reply, err := r.say("do you know when my birthday is? it should repeat in the calendar")
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := r.b.S.AllCalendar(); len(all) != 0 {
		t.Fatalf("no date was given, so nothing may be added: %+v", all)
	}
	if reply.Run.Steps[1].OK || !strings.Contains(reply.Run.Steps[1].Note, "has not said when") {
		t.Fatalf("the refusal should be recorded: %+v", reply.Run.Steps[1])
	}
}

func TestTheSameRepeatingEntryReplacesTheOldOne(t *testing.T) {
	r := newRig(t)
	add := StepCall{Tool: "calendar.add", Title: "Thomas's birthday", Date: "2026-11-20", Kind: "event", Repeat: "yearly"}
	r.b.runTool(context.Background(), add)
	add.Date = "1994-08-03"
	r.b.runTool(context.Background(), add)
	all, _ := r.b.S.AllCalendar()
	if len(all) != 1 || all[0].At.Format("2006-01-02") != "1994-08-03" {
		t.Fatalf("want one birthday, on the corrected date: %+v", all)
	}
	found, _ := r.b.runTool(context.Background(), StepCall{Tool: "calendar.find", Query: "when is my birthday"})
	items := found.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["next"] != "2027-08-03" {
		t.Fatalf("find should give the next occurrence: %+v", items)
	}
}

func TestANamedWeekdayCorrectsThePlannedDate(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local) // a Friday
	if got := fixWeekday("2026-11-20", "remind me every week on monday at 09:00", now); got != "2026-11-23" {
		t.Fatalf("got %s, want Monday the 23rd", got)
	}
	if got := fixWeekday("2026-11-23", "on monday please", now); got != "2026-11-23" {
		t.Fatalf("a date already on the named day must stay: %s", got)
	}
	if got := fixWeekday("2026-11-20", "lunch tomorrow", now); got != "2026-11-20" {
		t.Fatalf("no weekday named, so no change: %s", got)
	}
}

func TestUnsupportedNumbers(t *testing.T) {
	facts := `{"temp_c":13.6,"wind_kph":38,"usual_wind_kph":12}`
	if bad := unsupportedNumbers("It's 14° with wind at 38 km/h against a usual 12.", facts); len(bad) != 0 {
		t.Fatalf("supported numbers were flagged: %v", bad)
	}
	bad := unsupportedNumbers("It's 14° and there's a 70% chance of rain.", facts)
	if len(bad) != 1 || bad[0] != "70" {
		t.Fatalf("want [70], got %v", bad)
	}
}

func TestGate(t *testing.T) {
	cfg := testConfig()
	day := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	night := time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC)
	if why := gate(cfg, day, "followup", time.Time{}, 0); why != "" {
		t.Fatalf("a first follow-up in the afternoon should pass, got %q", why)
	}
	if gate(cfg, night, "followup", time.Time{}, 0) == "" {
		t.Fatal("follow-ups must wait during quiet hours")
	}
	if gate(cfg, night, "reminder", time.Time{}, 99) != "" {
		t.Fatal("a reminder the user asked for always goes out")
	}
	if gate(cfg, day, "followup", day.Add(-10*time.Minute), 1) == "" {
		t.Fatal("cooldown should hold a second message back")
	}
	if gate(cfg, day, "followup", time.Time{}, cfg.InitiativeBudget) == "" {
		t.Fatal("the daily budget should hold further messages back")
	}
}

func TestCompareNeedsHistoryThenFlags(t *testing.T) {
	w := Weather{TempC: 14, WindKph: 38}
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	if compare(w, []store.Reading{{At: now.AddDate(0, 0, -1), TempC: 19, WindKph: 12}}) != nil {
		t.Fatal("one reading is not enough to call anything usual")
	}
	hist := []store.Reading{
		{At: now.AddDate(0, 0, -3), TempC: 19, WindKph: 11},
		{At: now.AddDate(0, 0, -2), TempC: 20, WindKph: 13},
		{At: now.AddDate(0, 0, -1), TempC: 18, WindKph: 12},
	}
	c := compare(w, hist)
	if c == nil || c.UsualTempC != 19 || c.UsualWindKph != 12 || c.TempDeltaC != -5 {
		t.Fatalf("unexpected comparison: %+v", c)
	}
	if len(c.Flags) != 2 || c.Flags[0].What != "temperature" || c.Flags[1].Level != "warn" {
		t.Fatalf("want a temperature note and a wind warning, got %+v", c.Flags)
	}
}

func TestWeatherTurnEndToEnd(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"weather","mode":"once","steps":[{"tool":"weather.now","place":""}]}`, nil
	}
	r.fake.Handlers["notice"] = func(llm.Request) (string, error) {
		return `{"emotion":"concern","intensity":0.5,"touches":"Thomas's safety","warmth":0.3,"energy":0.1,"confidence":0,"importance":3,"follow_up":{"text":"Ask how the dentist call went","reason":"They keep putting it off","when":"tomorrow"},"resolved":[]}`, nil
	}
	var writerSaw string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		writerSaw = q.Messages[0].Content
		return "Take the jacket. It's 14° with wind at 38 km/h.", nil
	}
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"add","ref":0,"category":"health","content":"Thomas is afraid of the dentist."}],"people":[{"name":"Maria","relationship":"sister","note":"Lives in Porto."}]}`, nil
	}

	reply, err := r.say("Do I need a jacket? I still need to call the dentist, my sister Maria keeps asking.")
	if err != nil {
		t.Fatal(err)
	}
	r.b.Wait()

	if !strings.Contains(reply.Text, "jacket") || !reply.Run.Verified || reply.Run.Rewrites != 0 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
	if got := stepNames(r.finished(reply)); got != "route,weather.now,writer,verify,notice" {
		t.Fatalf("steps = %s", got)
	}
	if !strings.Contains(writerSaw, `"wind_kph": 38`) {
		t.Fatalf("the writer was not shown the weather facts:\n%s", writerSaw)
	}
	if got := r.b.State().Feeling; got != "concern" {
		t.Fatalf("feeling = %q, want concern", got)
	}
	if m := r.b.Mood(); m.Warmth <= baseline.Warmth {
		t.Fatalf("warmth should have risen: %+v", m)
	}
	open, _ := r.b.S.Intentions("open")
	if len(open) != 1 || open[0].Kind != "followup" || !open[0].TriggerAt.Equal(r.now.Add(20*time.Hour)) {
		t.Fatalf("want one follow-up due in 20 hours, got %+v", open)
	}
	mems, _ := r.b.S.Memories()
	if len(mems) != 1 || mems[0].Category != "health" {
		t.Fatalf("memory was not captured: %+v", mems)
	}
	if p, ok := r.b.S.FindPerson("maria"); !ok || p.Closeness != 0.9 {
		t.Fatalf("the sister should be remembered as very close: %+v", p)
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-time.Hour), r.now.Add(time.Hour))
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ","); got != "user_msg,nova_msg,appraisal" {
		t.Fatalf("stream = %s", got)
	}
	if evs[0].Importance != 3 {
		t.Fatalf("the user's message should carry the noticed importance, got %d", evs[0].Importance)
	}
}

func TestInventedNumberEarnsOneRewrite(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"weather","mode":"once","steps":[{"tool":"weather.now"}]}`, nil
	}
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			return "It's 14° with a 70% chance of rain.", nil
		}
		if !strings.Contains(q.System, "70") || len(q.Messages) != 1 {
			t.Error("the rewrite was not told which number was wrong")
		}
		return "It's 14° and clear.", nil
	}
	reply, err := r.say("weather?")
	if err != nil {
		t.Fatal(err)
	}
	if drafts != 2 || reply.Run.Rewrites != 1 || !reply.Run.Verified || reply.Text != "It's 14° and clear." {
		t.Fatalf("drafts=%d reply=%+v", drafts, reply)
	}
}

func TestStillWrongAfterRewriteIsSentUnverified(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "You have 3 meetings.", nil }
	reply, err := r.say("what's on?")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Run.Verified || reply.Run.Rewrites != 1 {
		t.Fatalf("want an unverified reply after one rewrite, got %+v", reply.Run)
	}
}

func TestDraftThatCopiesThePromptIsRejected(t *testing.T) {
	r := newRig(t)
	drafts := 0
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			return "Nova: Sure.\nThomas: and then?\nNova: Well.", nil
		}
		return "Nova: Sure, go ahead.", nil
	}
	reply, err := r.say("can I tell you something?")
	if err != nil {
		t.Fatal(err)
	}
	if drafts != 2 || reply.Text != "Sure, go ahead." || !reply.Run.Verified {
		t.Fatalf("drafts=%d reply=%q verified=%v", drafts, reply.Text, reply.Run.Verified)
	}
}

func TestHistoryAlternatesAndStartsWithTheUser(t *testing.T) {
	cfg := testConfig()
	msgs := history(cfg, []store.Event{
		{Kind: "initiative", Text: "Did you call the dentist?"},
		{Kind: "user_msg", Text: "Not yet."},
		{Kind: "user_msg", Text: "After lunch."},
		{Kind: "nova_msg", Text: "Good."},
	})
	roles := ""
	for _, m := range msgs {
		roles += m.Role[:1]
	}
	if roles != "uaua" || msgs[2].Content != "Not yet.\nAfter lunch." {
		t.Fatalf("roles=%s msgs=%+v", roles, msgs)
	}
}

func TestDuplicateStepsRunOnce(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"memory","mode":"once","steps":[{"tool":"memory.search","query":"work"},{"tool":"memory.search","query":"work"}]}`, nil
	}
	reply, err := r.say("what do you know about my work?")
	if err != nil {
		t.Fatal(err)
	}
	if got := stepNames(r.finished(reply)); got != "route,memory.search,writer,verify,notice" {
		t.Fatalf("steps = %s", got)
	}
}

func TestCaptureKeepsDurableFactsOnly(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[0].Content, "Take facts from this message only:\nI work as a backend developer") {
			t.Error("capture should be told to take facts from the new message only")
		}
		return `{"changes":[
		  {"op":"add","ref":0,"category":"work","content":"Thomas works as a backend developer."},
		  {"op":"add","ref":0,"category":"personal","content":"Thomas needs a jacket today."},
		  {"op":"add","ref":0,"category":"personal","content":"I work with Go."}],"people":[]}`, nil
	}
	if _, err := r.say("I work as a backend developer, mostly Go."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	if len(mems) != 1 || mems[0].Content != "Thomas works as a backend developer." {
		t.Fatalf("only the durable third-person fact should be kept, got %+v", mems)
	}
}

func TestUpdateCannotOverwriteAnUnrelatedMemory(t *testing.T) {
	r := newRig(t)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas has a sister called Maria.", CreatedAt: r.now})
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Thomas lives in Munich.", CreatedAt: r.now})
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[
		  {"op":"update","ref":1,"category":"work","content":"Thomas works as a backend developer."},
		  {"op":"update","ref":2,"category":"personal","content":"Thomas lives in Berlin."}],"people":[]}`, nil
	}
	if _, err := r.say("I moved to Berlin."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	if len(mems) != 2 || mems[0].Content != "Thomas has a sister called Maria." || mems[1].Content != "Thomas lives in Berlin." {
		t.Fatalf("the sister must survive and the city must be replaced, got %+v", mems)
	}
}

func TestInventedMemoriesAndPeopleAreDropped(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[
		  {"op":"add","ref":0,"category":"personal","content":"Thomas is a 30-year-old software engineer with a passion for coding."},
		  {"op":"add","ref":0,"category":"work","content":"Thomas's manager Peter moved the deadline."}],
		  "people":[{"name":"Peter","relationship":"manager","note":""},{"name":"Emily","relationship":"friend","note":""}]}`, nil
	}
	if _, err := r.say("My manager Peter moved the deadline again."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	if len(mems) != 1 || !strings.Contains(mems[0].Content, "deadline") {
		t.Fatalf("only the fact the user stated should be kept, got %+v", mems)
	}
	people, _ := r.b.S.People()
	if len(people) != 1 || people[0].Name != "Peter" {
		t.Fatalf("only the person the user named should be kept, got %+v", people)
	}
}

func TestNothingIsRemovedUnlessTheUserAsks(t *testing.T) {
	r := newRig(t)
	r.b.S.AddCalendarItem(store.CalendarItem{Title: "Dentist", At: r.now.Add(5 * time.Hour)})
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.remove","id":1}]}`, nil
	}
	reply, err := r.say("I'm a bit stressed about the dentist.")
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := r.b.S.CalendarBetween(r.now.Add(-time.Hour), r.now.Add(24*time.Hour)); len(left) != 1 {
		t.Fatal("the appointment was deleted without being asked")
	}
	if reply.Run.Steps[1].OK {
		t.Fatal("the refused removal should show as a failed step")
	}
}

func TestNothingIsAddedThatWasNotMentioned(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Lunch with Maria","date":"2026-11-20","time":"13:00","duration_min":60,"location":"","kind":"event","repeat":"none"}]}`, nil
	}
	if _, err := r.say("I'm stressed, the deadline moved again."); err != nil {
		t.Fatal(err)
	}
	if items, _ := r.b.S.CalendarBetween(r.now.Add(-24*time.Hour), r.now.Add(48*time.Hour)); len(items) != 0 {
		t.Fatalf("an event nobody mentioned was added: %+v", items)
	}
}

func TestEchoedInstructionsAreCutFromTheDraft(t *testing.T) {
	sys := "You are Nova.\n- One to four sentences. No greeting, no advice they did not ask for."
	got := cutEchoes("Take the jacket.\n\n- One to four sentences. No greeting, no advice they did not ask for.\nmore", sys)
	if got != "Take the jacket." {
		t.Fatalf("got %q", got)
	}
}

func TestRequestsDoNotBecomeFollowUps(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"weather","mode":"once","steps":[{"tool":"weather.now","place":""}]}`, nil
	}
	r.fake.Handlers["notice"] = func(llm.Request) (string, error) {
		return strings.Replace(calmNotice, `{"text":"","reason":"","when":"none"}`, `{"text":"Did you check the weather?","reason":"x","when":"today"}`, 1), nil
	}
	if _, err := r.say("Is it going to rain?"); err != nil {
		t.Fatal(err)
	}
	if open, _ := r.b.S.Intentions("open"); len(open) != 0 {
		t.Fatalf("a weather question is not something to follow up on: %+v", open)
	}
}

func TestRepetitionLoopIsCut(t *testing.T) {
	got := cutRepeats("Take the jacket. It is windy. Sheepish sheep Sheepish sheep Sheepish sheep Sheepish sheep")
	if got != "Take the jacket. It is windy." {
		t.Fatalf("got %q", got)
	}
	if plain := "No, no, it is fine. Really."; cutRepeats(plain) != plain {
		t.Fatal("ordinary text must be left alone")
	}
}

func TestAnEmptySecondDraftFallsBackToTheFirst(t *testing.T) {
	r := newRig(t)
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			return "You have 3 meetings.", nil
		}
		return strings.Split(q.System, "\n")[0], nil // nothing but an echo of her instructions
	}
	reply, err := r.say("what's on?")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "You have 3 meetings." || reply.Run.Verified {
		t.Fatalf("want the first draft, unverified; got %q verified=%v", reply.Text, reply.Run.Verified)
	}
}

func TestRouterExamplesUseRealDates(t *testing.T) {
	r := newRig(t)
	sys := r.b.routeSystem(r.b.Cfg.Get())
	if !strings.Contains(sys, `"date":"2026-11-21","time":"13:00"`) || strings.Contains(sys, "2030") {
		t.Fatalf("examples should carry tomorrow's real date:\n%s", sys)
	}
}

func TestFollowUpThatEchoesTheQuestionIsDropped(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["notice"] = func(llm.Request) (string, error) {
		return strings.Replace(calmNotice, `{"text":"","reason":"","when":"none"}`, `{"text":"What do I have on tomorrow?","reason":"to check","when":"tomorrow"}`, 1), nil
	}
	if _, err := r.say("What do I have on tomorrow?"); err != nil {
		t.Fatal(err)
	}
	if open, _ := r.b.S.Intentions("open"); len(open) != 0 {
		t.Fatalf("no follow-up should be queued, got %+v", open)
	}
}

func TestStepsModeAsksForTheNextStep(t *testing.T) {
	r := newRig(t)
	item, _ := r.b.S.AddCalendarItem(store.CalendarItem{Title: "Dentist", At: r.now.Add(5 * time.Hour), DurationMin: 30})
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"steps","steps":[{"tool":"calendar.list","from":"2026-11-20"}]}`, nil
	}
	asked := 0
	r.fake.Handlers["next"] = func(q llm.Request) (string, error) {
		asked++
		if asked == 1 {
			if !strings.Contains(q.Messages[0].Content, "Dentist") {
				t.Error("the next-step call was not shown the list result")
			}
			return `{"done":false,"step":{"tool":"calendar.remove","id":1}}`, nil
		}
		return `{"done":true,"step":{"tool":"weather.now","place":""}}`, nil
	}
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Done, the dentist is off your calendar.", nil }
	reply, err := r.say("cancel the dentist today")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Run.Mode != "steps" || asked != 2 {
		t.Fatalf("mode=%s next calls=%d", reply.Run.Mode, asked)
	}
	left, _ := r.b.S.CalendarBetween(r.now.Add(-24*time.Hour), r.now.Add(24*time.Hour))
	if len(left) != 0 {
		t.Fatalf("item %d should be gone, calendar has %+v", item.ID, left)
	}
}

func TestLearnedPlanningModeOverridesHerPick(t *testing.T) {
	r := newRig(t)
	r.b.S.Set("planning.calendar", "steps")
	got := r.b.planningMode(r.b.Cfg.Get(), plan{Intent: "calendar", Mode: "once"})
	if got != "steps" {
		t.Fatalf("mode = %s, want the learned steps", got)
	}
}

func TestRouterDownStillAnswers(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) { return "", errors.New("boom") }
	reply, err := r.say("hello")
	if err != nil || reply.Text == "" {
		t.Fatalf("a failed route should fall back to plain chat: %v %+v", err, reply)
	}
	if reply.Run.Steps[0].OK {
		t.Fatal("the failed route step should be recorded as failed")
	}
}

func TestFollowUpIsAskedOnceThenLetGo(t *testing.T) {
	r := newRig(t)
	r.b.S.AddIntention(store.Intention{Kind: "followup", Text: "Ask about the dentist", Reason: "health",
		TriggerAt: r.now.Add(time.Hour), ExpiresAt: r.now.Add(72 * time.Hour), CreatedAt: r.now})
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Did you get to call the dentist?", nil }
	r.b.S.Set("last_brief_day", dayKey(r.now))

	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 0 {
		t.Fatalf("no model should run before the trigger time, got %d writer calls", n)
	}

	r.now = r.now.Add(2 * time.Hour)
	r.b.Tick(context.Background())
	evs, _ := r.b.S.RecentEvents(5, "initiative")
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "dentist") {
		t.Fatalf("want one initiative, got %+v", evs)
	}
	sent, _ := r.b.S.Intentions("sent")
	if len(sent) != 1 {
		t.Fatalf("the follow-up should be waiting for an answer, got %+v", sent)
	}

	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 1 {
		t.Fatalf("she must not ask twice, got %d writer calls", n)
	}

	r.now = r.now.Add(49 * time.Hour)
	r.b.Tick(context.Background())
	expired, _ := r.b.S.Intentions("expired")
	if len(expired) != 1 {
		t.Fatalf("an unanswered follow-up should be let go after two days, got %+v", expired)
	}
}

func TestAnswerSettlesAFollowUp(t *testing.T) {
	r := newRig(t)
	i, _ := r.b.S.AddIntention(store.Intention{Kind: "followup", Text: "Ask about the dentist",
		TriggerAt: r.now, ExpiresAt: r.now.Add(72 * time.Hour), CreatedAt: r.now})
	r.b.S.SetIntentionStatus(i.ID, "sent", r.now)
	r.fake.Handlers["notice"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.System, "#1 Ask about the dentist") {
			t.Error("notice was not shown the waiting follow-up")
		}
		return strings.Replace(calmNotice, `"resolved":[]`, `"resolved":[1]`, 1), nil
	}
	r.now = r.now.Add(10 * time.Minute)
	if _, err := r.say("I called the dentist, all booked."); err != nil {
		t.Fatal(err)
	}
	done, _ := r.b.S.Intentions("done")
	if len(done) != 1 || !done[0].Answered {
		t.Fatalf("the follow-up should be done and marked answered: %+v", done)
	}
}

func TestReminderGoesOutDuringCooldown(t *testing.T) {
	r := newRig(t)
	r.b.S.AddEvent(store.Event{At: r.now.Add(-5 * time.Minute), Kind: "initiative", Text: "earlier", Meta: []byte(`{"kind":"followup"}`)})
	r.b.S.AddCalendarItem(store.CalendarItem{Kind: "reminder", Title: "Take the bread out", At: r.now.Add(-time.Minute)})
	r.b.S.AddIntention(store.Intention{Kind: "followup", Text: "Ask about the run", TriggerAt: r.now.Add(-time.Minute), ExpiresAt: r.now.Add(time.Hour), CreatedAt: r.now})
	r.b.S.Set("last_brief_day", dayKey(r.now))
	var asked string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		asked = q.Messages[0].Content
		return "Time to take the bread out.", nil
	}
	r.b.Tick(context.Background())
	if !strings.Contains(asked, "bread") {
		t.Fatalf("the reminder should have been delivered, writer saw: %q", asked)
	}
	r.b.Tick(context.Background())
	if n := r.fake.CallsFor("writer"); n != 1 {
		t.Fatalf("the follow-up must wait out the cooldown, got %d writer calls", n)
	}
}

func TestSleepReflectsProposesAndTidies(t *testing.T) {
	r := newRig(t)
	for _, c := range []string{
		"Thomas lives in Lisbon.", "Thomas works as a developer.", "Thomas likes espresso.",
		"Thomas runs on Sundays.", "Thomas has a sister called Maria.", "Thomas's home is Lisbon.",
		"Thomas dislikes early meetings.", "Thomas used to live in Porto.",
	} {
		r.b.S.AddMemory(store.Memory{Kind: "fact", Content: c, CreatedAt: r.now})
	}
	if _, err := r.say("I keep putting off the dentist."); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()

	r.fake.Handlers["reflect"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[0].Content, "putting off the dentist") {
			t.Error("reflection was not shown the day's stream")
		}
		return `{"journal":"A quiet day. He is avoiding the dentist.","reflections":[{"evidence":[1],"text":"He is not forgetting the dentist, he is avoiding it.","importance":4},{"evidence":[99],"text":"Silence is a kind of music.","importance":5}],"share":"I think the dentist worries you more than you say.","proposals":[{"kind":"value","target":"5","after":"Ask once, then let go. If it is about health, ask twice.","reason":"One ask was not enough for the dentist."},{"kind":"planning","target":"nonsense","after":"steps","reason":"x"}]}`, nil
	}
	r.fake.Handlers["tidy"] = func(llm.Request) (string, error) {
		return `{"merge":[{"keep":1,"drop":[6],"content":"Thomas lives in Lisbon, his home."}],"archive":[{"ref":8,"why":"He moved."}]}`, nil
	}
	r.now = r.now.Add(17 * time.Hour) // 02:00
	report, err := r.b.Sleep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Reflections) != 1 || report.Proposals != 1 || report.Tidied != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if st := r.b.State(); st.State != "awake" || st.Pending != 1 {
		t.Fatalf("she should be awake with one proposal waiting: %+v", st)
	}
	mems, _ := r.b.S.Memories()
	if len(mems) != 7 { // 8 facts - 2 tidied + 1 reflection
		t.Fatalf("want 7 active memories, got %d", len(mems))
	}
	morning, _ := r.b.S.Intentions("open")
	if len(morning) != 1 || morning[0].Kind != "reflection" || morning[0].TriggerAt.Hour() != 7 {
		t.Fatalf("her thought should be queued for the morning: %+v", morning)
	}

	// Nothing about her values changes until the user approves.
	vals, _ := r.b.S.Values()
	if vals[4].Origin != "seed" {
		t.Fatal("a value changed before approval")
	}
	pending, _ := r.b.S.Proposals("pending", 10)
	if pending[0].Before != vals[4].Text {
		t.Fatalf("the proposal should quote the current value, got %q", pending[0].Before)
	}
	if _, err := r.b.DecideProposal(pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	vals, _ = r.b.S.Values()
	if vals[4].Origin != "hers" || !strings.Contains(vals[4].Text, "ask twice") {
		t.Fatalf("the approved value was not applied: %+v", vals[4])
	}
	if _, err := r.b.DecideProposal(pending[0].ID, true); err == nil {
		t.Fatal("a proposal can only be decided once")
	}

	// Tidying is undoable.
	tidied, _ := r.b.S.TidySince(r.now.Add(-time.Minute))
	if len(tidied) != 2 {
		t.Fatalf("want two tidy entries, got %d", len(tidied))
	}
	if err := r.b.UndoTidy(tidied[0].ID); err != nil {
		t.Fatal(err)
	}
	first, _ := r.b.S.GetMemory(mems[0].ID)
	if first.Content != "Thomas lives in Lisbon." {
		t.Fatalf("undo should restore the original wording, got %q", first.Content)
	}
	if mems, _ = r.b.S.Memories(); len(mems) != 8 {
		t.Fatalf("undo should bring the merged memory back, have %d", len(mems))
	}
}

func TestSleepWithNothingLivedSkipsTheModel(t *testing.T) {
	r := newRig(t)
	report, err := r.b.Sleep(context.Background())
	if err != nil || report.Skipped == "" {
		t.Fatalf("want a skipped sleep, got %+v %v", report, err)
	}
	if n := r.fake.CallsFor("reflect"); n != 0 {
		t.Fatal("no model should run when nothing happened")
	}
}

func TestFailedReflectionLeavesTheDayForNextTime(t *testing.T) {
	r := newRig(t)
	r.say("hello")
	r.b.Wait()
	r.fake.Handlers["reflect"] = func(llm.Request) (string, error) { return "", errors.New("model unloaded") }
	if _, err := r.b.Sleep(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if r.b.S.Get("last_sleep_at") != "" || r.b.S.Get("state") != "awake" {
		t.Fatal("a failed sleep must not be recorded, and she must wake up")
	}
}

func TestMorningBriefIsQueuedOncePerDay(t *testing.T) {
	r := newRig(t)
	var saw string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		saw = q.Messages[0].Content
		return "Morning. Clear and 14° today, nothing on the calendar.", nil
	}
	r.b.Tick(context.Background())
	r.b.Tick(context.Background())
	evs, _ := r.b.S.RecentEvents(5, "initiative")
	if len(evs) != 1 {
		t.Fatalf("want exactly one brief, got %d", len(evs))
	}
	if !strings.Contains(saw, "calendar_today") || !strings.Contains(saw, "temp_c") {
		t.Fatalf("the brief was not given the day's facts: %s", saw)
	}
}

func TestStatedRange(t *testing.T) {
	// Friday 20 November 2026.
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for said, want := range map[string]string{
		"what is on my calendar next week?": "2026-11-23 2026-11-29",
		"anything on this week":             "2026-11-20 2026-11-22",
		"what are we doing this weekend?":   "2026-11-21 2026-11-22",
		"what's on tomorrow":                "2026-11-21 2026-11-21",
	} {
		from, to, ok := statedRange(said, now)
		if !ok || from+" "+to != want {
			t.Errorf("%q = %s %s, want %s", said, from, to, want)
		}
	}
	if _, _, ok := statedRange("when is the dentist?", now); ok {
		t.Error("no stretch of days was named")
	}
}

func TestDropTackedQuestion(t *testing.T) {
	if got := dropTackedQuestion("Done, the dentist is in for Tuesday at 10:30. Anything else you need?"); got != "Done, the dentist is in for Tuesday at 10:30." {
		t.Errorf("got %q", got)
	}
	if got := dropTackedQuestion("What time is the lunch?"); got != "What time is the lunch?" {
		t.Errorf("a reply that is only a question must stay: %q", got)
	}
}

func TestMentionedPlanIsNotAdded(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"calendar","mode":"once","steps":[{"tool":"calendar.add","title":"Harbour with the boys","date":"2026-11-21","time":"09:00","duration_min":120,"location":"","kind":"event","repeat":"none"}]}`, nil
	}
	if _, err := r.say("I think I'll take the boys to the harbour on Saturday morning"); err != nil {
		t.Fatal(err)
	}
	if items, _ := r.b.S.AllCalendar(); len(items) != 0 {
		t.Fatalf("a plan they only mentioned was written to the calendar: %+v", items)
	}
}

type fakeWeb struct {
	results []WebResult
	shows   []PodcastShow
	asked   []string
}

func (f *fakeWeb) Search(_ context.Context, q string) ([]WebResult, error) {
	f.asked = append(f.asked, q)
	return f.results, nil
}

func (f *fakeWeb) Read(_ context.Context, address string) (WebPage, error) {
	f.asked = append(f.asked, address)
	return WebPage{Title: "A page", URL: address, Text: "Some text."}, nil
}

func (f *fakeWeb) Podcasts(_ context.Context, name string) ([]PodcastShow, error) {
	f.asked = append(f.asked, name)
	return f.shows, nil
}

func TestPodcastFeedTurn(t *testing.T) {
	r := newRig(t)
	web := &fakeWeb{shows: []PodcastShow{{Name: "The WAN Show", By: "Linus Tech Tips", Feed: "https://feeds.megaphone.fm/LMG3928170156", Episodes: 414}}}
	r.b.Web = web
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"podcasts","mode":"once","steps":[{"tool":"podcasts.find","query":"WAN Show"}]}`, nil
	}
	writes := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		writes++
		if !strings.Contains(q.Messages[len(q.Messages)-1].Content, "https://feeds.megaphone.fm/LMG3928170156") {
			t.Errorf("the writer was not given the feed: %s", q.Messages[len(q.Messages)-1].Content)
		}
		if writes == 1 {
			return "Here it is: https://www.youtube.com/feeds/videos.xml?channel_id=UCmadeup.", nil
		}
		return "Here it is: https://feeds.megaphone.fm/LMG3928170156.", nil
	}
	reply, err := r.say("can you find the rss feed for the wan show?")
	if err != nil {
		t.Fatal(err)
	}
	if writes != 2 || !strings.Contains(reply.Text, "LMG3928170156") || strings.Contains(reply.Text, "UCmadeup") {
		t.Fatalf("a made-up address must be sent back and the real one given: %d writes, %q", writes, reply.Text)
	}
	if len(web.asked) != 1 || web.asked[0] != "WAN Show" {
		t.Fatalf("asked = %v", web.asked)
	}
}

func TestSearchingHeardForAShowLooksItUp(t *testing.T) {
	r := newRig(t)
	web := &fakeWeb{shows: []PodcastShow{{Name: "The WAN Show", By: "Linus Tech Tips", Feed: "https://feeds.megaphone.fm/LMG3928170156"}}}
	r.b.Web = web
	// The planner reads "search for the podcast" as a look through what
	// she has heard.
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"podcasts","mode":"once","steps":[{"tool":"podcasts.heard","query":"WAN Show"}]}`, nil
	}
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[len(q.Messages)-1].Content, "https://feeds.megaphone.fm/LMG3928170156") {
			t.Errorf("the writer was not given the directory's answer: %s", q.Messages[len(q.Messages)-1].Content)
		}
		return "Found it: https://feeds.megaphone.fm/LMG3928170156", nil
	}
	if _, err := r.say("search for the podcast the wan show"); err != nil {
		t.Fatal(err)
	}
	if len(web.asked) != 1 || web.asked[0] != "WAN Show" {
		t.Fatalf("asked = %v", web.asked)
	}
}

func TestMadeUpLinksAreFound(t *testing.T) {
	facts := `{"feed":"https://example.org/feed.xml?a=1&b=2","page":"https://example.com/news/"}`
	if bad := unsupportedLinks("It is https://example.org/feed.xml?a=1&b=2. Or see (https://example.com/news).", facts); len(bad) != 0 {
		t.Errorf("addresses from the facts were flagged: %v", bad)
	}
	if bad := unsupportedLinks("Try https://example.org/feed2.xml, it should work.", facts); len(bad) != 1 || bad[0] != "https://example.org/feed2.xml" {
		t.Errorf("a made-up address was not flagged: %v", bad)
	}
	if got := factsText([]Fact{{Tool: "web.read", Result: map[string]any{"url": "https://e.org/?a=1&b=2"}}}); !strings.Contains(got, "a=1&b=2") {
		t.Errorf("an & in an address must stay as it is: %s", got)
	}
}

func TestWebSearchTurn(t *testing.T) {
	r := newRig(t)
	web := &fakeWeb{results: []WebResult{{Title: "List of tallest lighthouses", URL: "https://en.wikipedia.org/wiki/List_of_tallest_lighthouses", Snippet: "Jeddah Light is 133 metres tall."}}}
	r.b.Web = web
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"web","mode":"once","steps":[{"tool":"web.search","query":"tallest lighthouse"},{"tool":"web.read","url":"https://made-up.example/page"}]}`, nil
	}
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		last := q.Messages[len(q.Messages)-1].Content
		if !strings.Contains(last, "133 metres") || !strings.Contains(last, "en.wikipedia.org") {
			t.Errorf("the writer was not given the search result: %s", last)
		}
		return "Jeddah Light, at 133 metres, according to Wikipedia.", nil
	}
	reply, err := r.say("what is the tallest lighthouse in the world?")
	if err != nil {
		t.Fatal(err)
	}
	if !reply.Run.Verified {
		t.Fatalf("a number from the search result should pass the check: %+v", reply.Run.Steps)
	}
	if len(web.asked) != 1 || web.asked[0] != "tallest lighthouse" {
		t.Fatalf("only the search should have gone out, not the made-up address: %v", web.asked)
	}

	cfg := r.b.Cfg.Get()
	cfg.WebSearch = false
	r.b.Cfg.Set(cfg)
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		if !strings.Contains(q.Messages[len(q.Messages)-1].Content, "switched off in Settings") {
			t.Error("the writer should be told that looking things up is switched off")
		}
		return "I can't look that up while searching is switched off in Settings.", nil
	}
	r.say("and the second tallest?")
	if len(web.asked) != 1 {
		t.Fatalf("nothing may go online when the setting is off: %v", web.asked)
	}
}

func TestParseSearchResults(t *testing.T) {
	page := `<html><body><div class="results">
	<div class="result results_links result--ad"><h2><a class="result__a" href="//duckduckgo.com/y.js?ad=1">Buy lighthouses</a></h2></div>
	<div class="result results_links web-result"><h2 class="result__title"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Flights%3Fa%3D1&amp;rut=x">Tall <b>lights</b></a></h2>
	  <a class="result__snippet" href="#">The <b>tallest</b> one is   in Jeddah.</a></div>
	<div class="result results_links web-result"><h2 class="result__title"><a class="result__a" href="https://www.example.com/b">Second</a></h2></div>
	</div></body></html>`
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	got := parseResults(doc)
	if len(got) != 2 {
		t.Fatalf("want the two real results, got %+v", got)
	}
	if got[0].Title != "Tall lights" || got[0].URL != "https://example.org/lights?a=1" || got[0].Snippet != "The tallest one is in Jeddah." {
		t.Errorf("first result = %+v", got[0])
	}
	if siteOf(got[1].URL) != "example.com" {
		t.Errorf("site = %q", siteOf(got[1].URL))
	}
}

func TestReadPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>Boiling eggs</title><script>var x = "ignore me";</script></head><body>
		<nav><p>Home and a long list of menu links that is not the article at all</p></nav>
		<article><h1>How to boil an egg</h1><p>Lower the egg into boiling water and leave it for six minutes for a runny yolk.</p><p>ok</p></article>
		<footer><p>Copyright notice that goes on for more than forty characters in total</p></footer></body></html>`)
	}))
	defer srv.Close()
	page, err := (&DuckDuckGo{AllowLocal: true}).Read(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Boiling eggs" || page.Text != "How to boil an egg\nLower the egg into boiling water and leave it for six minutes for a runny yolk." {
		t.Errorf("page = %+v", page)
	}
	if _, err := (&DuckDuckGo{}).Read(context.Background(), srv.URL); err == nil {
		t.Error("a page on this machine must not be readable")
	}
	if _, err := (&DuckDuckGo{}).Read(context.Background(), "file:///etc/hosts"); err == nil {
		t.Error("only web addresses may be read")
	}
}

func TestReadPageKeepsLinksAndFeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><title>The Show</title>
		<link rel="alternate" type="application/rss+xml" title="Episodes" href="/episodes.rss"></head><body>
		<main><p>Every week we talk about tech news, and you can <a href="https://example.com/listen?x=1&amp;y=2">listen on our site</a> any time.</p>
		<ul><li><a href="/subscribe">Subscribe</a></li><li><a href="#top">Top</a></li></ul></main>
		<footer><a href="https://feeds.example.net/show">RSS</a> <a href="https://player.example/subscribe?url=https://feeds.example.net/show">Player RSS</a></footer></body></html>`)
	}))
	defer srv.Close()
	page, err := (&DuckDuckGo{AllowLocal: true}).Read(context.Background(), srv.URL+"/show/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listen on our site (https://example.com/listen?x=1&y=2)", "Subscribe (" + srv.URL + "/subscribe)"} {
		if !strings.Contains(page.Text, want) {
			t.Errorf("the text should hold %q:\n%s", want, page.Text)
		}
	}
	if strings.Contains(page.Text, "Top") {
		t.Errorf("a link to the same page is not worth keeping:\n%s", page.Text)
	}
	if len(page.Feeds) != 2 || page.Feeds[0] != srv.URL+"/episodes.rss" || page.Feeds[1] != "https://feeds.example.net/show" {
		t.Errorf("feeds = %v", page.Feeds)
	}
}

func TestReadFeeds(t *testing.T) {
	atom := `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Linus Tech Tips</title>
	<entry><title>New  video</title><link rel="alternate" href="https://www.youtube.com/watch?v=abc"/><published>2026-11-20T18:00:00+00:00</published></entry></feed>`
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><title>The WAN Show</title>
	<item><title>Episode 1</title><link>https://example.com/1</link><pubDate>Fri, 20 Nov 2026 22:00:00 GMT</pubDate></item></channel></rss>`
	for _, c := range []struct{ kind, body, title, line string }{
		{"text/xml; charset=UTF-8", atom, "Linus Tech Tips", "- 2026-11-20: New video (https://www.youtube.com/watch?v=abc)"},
		{"application/rss+xml", rss, "The WAN Show", "- 2026-11-20: Episode 1 (https://example.com/1)"},
		{"text/html", rss, "The WAN Show", "Episode 1"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", c.kind)
			io.WriteString(w, c.body)
		}))
		page, err := (&DuckDuckGo{AllowLocal: true}).Read(context.Background(), srv.URL)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if !page.IsFeed || page.Title != c.title || !strings.Contains(page.Text, "working feed.") || !strings.Contains(page.Text, c.line) {
			t.Errorf("%s: page = %+v", c.kind, page)
		}
	}
}

func TestPodcastDirectory(t *testing.T) {
	var asked url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, `{"resultCount":2,"results":[
		{"collectionName":"The WAN Show","artistName":"Linus Tech Tips","feedUrl":"https://feeds.megaphone.fm/LMG3928170156","trackCount":414,"releaseDate":"2026-11-21T05:30:00Z","primaryGenreName":"Tech News"},
		{"collectionName":"No feed here","artistName":"Someone"}]}`)
	}))
	defer srv.Close()
	shows, err := (&DuckDuckGo{AllowLocal: true, Directory: srv.URL}).Podcasts(context.Background(), "wan show")
	if err != nil {
		t.Fatal(err)
	}
	if asked.Get("term") != "wan show" || asked.Get("media") != "podcast" {
		t.Errorf("asked = %v", asked)
	}
	want := PodcastShow{Name: "The WAN Show", By: "Linus Tech Tips", Feed: "https://feeds.megaphone.fm/LMG3928170156", Episodes: 414, Latest: "2026-11-21", Genre: "Tech News"}
	if len(shows) != 1 || shows[0] != want {
		t.Errorf("shows = %+v", shows)
	}
}

func TestAnotherWordForTheSameRelationIsNotInvented(t *testing.T) {
	told := "Thomas's child Atreus is 7 months old. Thomas's wife is Bethany."
	if w := unsupportedClaim("You have a seven-month-old baby named Atreus.", told); w != "" {
		t.Errorf("baby for child was flagged: %q", w)
	}
	if w := unsupportedClaim("Your sister Maria will love that.", told); w == "" {
		t.Error("a sister nobody mentioned should be flagged")
	}
}

func TestBracketedAsidesAreCut(t *testing.T) {
	cfg := testConfig()
	if got := cleanDraft(cfg, "Night night, Thomas. [Goodbye for now.]"); got != "Night night, Thomas." {
		t.Errorf("got %q", got)
	}
}

func TestNotesAfterAReplyAreCut(t *testing.T) {
	got := cleanDraft(testConfig(), "See you later.\n\nNOTES TO SELF (Thomas cannot see them):\nHe is leaving.")
	if got != "See you later." {
		t.Errorf("got %q", got)
	}
}

func TestListsTurn(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"lists","mode":"once","steps":[{"tool":"lists.add","list":"my shopping list","items":["milk","eggs","champagne"]}]}`, nil
	}
	if _, err := r.say("add milk and eggs to the shopping list"); err != nil {
		t.Fatal(err)
	}
	items, _ := r.b.S.ListItems("shopping", r.now)
	if len(items) != 2 || items[0].Text != "milk" || items[1].Text != "eggs" {
		t.Fatalf("only what was said should be on the list: %+v", items)
	}
	// Adding the same thing again changes nothing.
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"lists","mode":"once","steps":[{"tool":"lists.add","list":"groceries","items":["Milk"]}]}`, nil
	}
	r.say("put milk on the groceries list")
	if items, _ = r.b.S.ListItems("shopping", r.now); len(items) != 2 {
		t.Fatalf("milk was added twice: %+v", items)
	}
	res, err := r.b.runTool(context.Background(), StepCall{Tool: "lists.done", Items: []string{"the milk"}})
	if err != nil {
		t.Fatal(err)
	}
	if left := res.(map[string]any)["still_on_list"].([]string); len(left) != 1 || left[0] != "eggs" {
		t.Fatalf("still on list = %v", left)
	}
	if listName("To-Do list") != "to-do" || listName("") != "to-do" || listName("my packing list") != "packing" {
		t.Error("list names are not settled as expected")
	}
}

func TestAsksForList(t *testing.T) {
	for said, want := range map[string]bool{
		"add milk to the shopping list":                 true,
		"oh and coffee too":                             true,
		"we should really sort out the garage sometime": false,
	} {
		if asksForList(said) != want {
			t.Errorf("asksForList(%q) = %v", said, !want)
		}
	}
}

func TestSleepLearnsWhoseNicknameItIs(t *testing.T) {
	r := newRig(t)
	r.say("well done little one, you passed them all")
	r.fake.Handlers["nicknames"] = func(q llm.Request) (string, error) {
		return `{"nicknames":[{"line":"well done little one","nickname":"little one","for":"companion","person":""},{"line":"x","nickname":"sunshine","for":"companion","person":""}]}`, nil
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-time.Hour), r.now.Add(time.Hour))
	got := r.b.learnNicknames(context.Background(), r.b.Cfg.Get(), evs)
	if len(got) != 1 || got[0].Name != "little one" || got[0].For != "companion" {
		t.Fatalf("want only the nickname that was said, got %+v", got)
	}
	_, notes, _ := r.b.writerParts(r.b.Cfg.Get(), r.b.Mood(), "hi")
	if !strings.Contains(notes, `"little one" is Thomas's pet name for you`) {
		t.Fatalf("the writer is not told whose nickname it is:\n%s", notes)
	}
	if got := r.b.dropBorrowedNames("Good to see you back, little one. How are the boys?"); got != "Good to see you back. How are the boys?" {
		t.Errorf("got %q", got)
	}
}

func TestBeingWrongCostsConfidence(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		return "It's 14° with a 70% chance of rain.", nil // 70 is in no fact, twice
	}
	before := r.b.Mood().Confidence
	r.say("what's the weather?")
	if after := r.b.Mood().Confidence; after >= before {
		t.Fatalf("a reply that went out wrong should cost confidence: %.2f -> %.2f", before, after)
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-time.Hour), r.now.Add(time.Hour))
	found := false
	for _, e := range evs {
		found = found || e.Kind == "mistake"
	}
	if !found {
		t.Fatal("the mistake should be written down for the night")
	}
}

func TestUrgent(t *testing.T) {
	earlier := []store.Event{{Kind: "user_msg", Text: "production database is down, serious incident"}, {Kind: "nova_msg", Text: "What do you need?"}}
	if !urgent("what are my options?", earlier) {
		t.Error("the emergency is still on")
	}
	if urgent("it's back, primary is up", earlier) {
		t.Error("the emergency is over")
	}
	if urgent("add milk to the shopping list", nil) {
		t.Error("nothing urgent here")
	}
}

func TestQuotedFactIsNotAnEcho(t *testing.T) {
	facts := "[\n {\n  \"added\": [\n   \"write the incident report\",\n   \"set up disk space alerts\"\n  ]\n }\n]"
	draft := `Done. "write the incident report" and "set up disk space alerts" are on your to-do list.`
	if got := cutEchoes(draft, facts); got != draft {
		t.Errorf("the reply was cut: %q", got)
	}
}

func TestSheAsksWhatSheIsCuriousAbout(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	if !r.b.wonder(cfg, "What do you do for work?", "I do not know yet.") {
		t.Fatal("the question should be held")
	}
	if r.b.wonder(cfg, "What do you do for work?", "again") {
		t.Fatal("the same question is not held twice")
	}
	if r.b.wonder(cfg, "What does 'Boo' mean to you besides being Bethany's nickname?", "x") {
		t.Fatal("a question about what a word means to them is not one a friend asks")
	}
	var told string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		told = q.Messages[len(q.Messages)-1].Content
		if strings.Contains(told, "wanting to ask") {
			return "Sounds like a good day. What do you do for work, by the way?", nil
		}
		return "Sounds like a good day.", nil
	}
	// The first message of a conversation is not the moment.
	r.say("hello there")
	if strings.Contains(told, "wanting to ask") {
		t.Fatal("she should not open a conversation with her question")
	}
	r.say("just had a nice walk by the sea")
	if !strings.Contains(told, "What do you do for work?") {
		t.Fatalf("she was not given her question to ask:\n%s", told)
	}
	sent, _ := r.b.S.Intentions("sent")
	if len(sent) != 1 || sent[0].Kind != "curiosity" {
		t.Fatalf("the question should be marked as asked: %+v", sent)
	}
	// Questions never go out as a reach-out of their own.
	r.b.wonder(cfg, "Where did you grow up?", "I would like to know.")
	r.now = r.now.Add(5 * time.Hour)
	r.b.Tick(context.Background())
	if evs, _ := r.b.S.RecentEvents(5, "initiative"); len(evs) != 0 {
		t.Fatalf("a question went out as a reach-out: %+v", evs)
	}
}

func TestTwoReachOutsDoNotShareASession(t *testing.T) {
	r := newRig(t)
	r.b.S.Set("last_brief_day", dayKey(r.now))
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Time to take the bins out.", nil }
	r.b.S.AddIntention(store.Intention{Kind: "reminder", Text: "Take the bins out", TriggerAt: r.now, ExpiresAt: r.now.Add(time.Hour), CreatedAt: r.now})
	r.b.Tick(context.Background())
	r.now = r.now.Add(5 * time.Minute)
	r.b.S.AddIntention(store.Intention{Kind: "reminder", Text: "Send the invoice", TriggerAt: r.now, ExpiresAt: r.now.Add(time.Hour), CreatedAt: r.now})
	r.b.Tick(context.Background())
	open, _ := r.b.S.OpenSessions()
	if len(open) != 2 || open[0].Title != "Reminder: Send the invoice" || open[1].Title != "Reminder: Take the bins out" {
		t.Fatalf("each reach-out should have its own named session: %+v", open)
	}
}

func TestConnectedToolShapesAreValid(t *testing.T) {
	tools := []mcp.Tool{{Server: "notes", Name: "search_notes", Description: "Search notes",
		Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"words"},"limit":{"type":"integer"},"tags":{"type":"array","items":{"type":"string"}}},"required":["query"],"additionalProperties":false}`)}}
	var parsed map[string]any
	if err := json.Unmarshal(planSchemaWith(shapesWith(tools)), &parsed); err != nil {
		t.Fatalf("the plan schema with a connected tool is not valid JSON: %v", err)
	}
	if !strings.Contains(shapesWith(tools), `"const":"mcp.notes.search_notes"`) || !strings.Contains(shapesWith(tools), `"required":["query"]`) {
		t.Fatalf("the tool's shape is missing: %s", shapesWith(tools))
	}
	if err := json.Unmarshal(planSchemaWith(shapesWith(nil)), &parsed); err != nil {
		t.Fatal(err)
	}
}

func TestSheDoesNotClaimWorkThatDidNotHappen(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) { return "", errors.New("the planner broke") }
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if drafts == 1 {
			if !strings.Contains(q.Messages[len(q.Messages)-1].Content, "none of your tools ran") {
				t.Error("she was not told that nothing ran")
			}
			return "Got it. I've added oat milk to your shopping list.", nil
		}
		return "That did not work just now. Can you ask me again?", nil
	}
	reply, _ := r.say("add oat milk to the shopping list")
	if strings.Contains(reply.Text, "added") {
		t.Fatalf("she claimed work that never happened: %q", reply.Text)
	}
}

type fakeEmbedder struct{}

// Texts about a child land near each other; everything else lands apart.
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		n := norm(t)
		v := []float32{0.05, 0.05, 1}
		if strings.Contains(n, "little guy") || strings.Contains(n, "months old") || strings.Contains(n, "baby") {
			v = []float32{1, 0.1, 0}
		}
		out[i] = v
	}
	return out, nil
}

func TestMemoriesAreFoundByMeaning(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	cfg.Embed = config.Endpoint{BaseURL: "http://embed.test/v1", Model: "embed"}
	r.b.Cfg.Set(cfg)
	r.b.E = fakeEmbedder{}
	for i := 0; i < 20; i++ {
		r.b.S.AddMemory(store.Memory{Content: "Thomas likes a walk by the sea on day " + string(rune('a'+i)) + ".", CreatedAt: r.now, UpdatedAt: r.now})
	}
	r.b.S.AddMemory(store.Memory{Content: "Thomas's child Atreus is 7 months old.", CreatedAt: r.now.Add(-90 * 24 * time.Hour), UpdatedAt: r.now.Add(-90 * 24 * time.Hour)})
	r.b.embedMissing(context.Background())
	mems, _ := r.b.S.Memories()
	has := func(list []store.Memory) bool {
		for _, m := range list {
			if strings.Contains(m.Content, "Atreus") {
				return true
			}
		}
		return false
	}
	// No word in common with the memory, and it is the oldest one.
	if has(rankMemories(mems, "how is the little guy sleeping", r.now, 5)) {
		t.Fatal("word matching alone should not have found it: the test proves nothing")
	}
	if !has(r.b.rank(mems, "how is the little guy sleeping", r.now, 5)) {
		t.Fatal("the memory about the baby should be found by meaning")
	}
}

func TestSheNoticesALongGap(t *testing.T) {
	r := newRig(t)
	r.say("hello")
	r.now = r.now.Add(5 * 24 * time.Hour)
	var told string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		told = q.Messages[len(q.Messages)-1].Content
		return "Good to hear from you.", nil
	}
	r.say("hey, I'm back")
	if !strings.Contains(told, "5 days ago") {
		t.Fatalf("she was not told how long it had been:\n%s", told)
	}
}

func TestSurenessAndFeedback(t *testing.T) {
	cfg := testConfig()
	if l, b := sureness(cfg, []Fact{{Tool: "web.search", Result: 1}}, true); l != "fairly" || b != "looked it up online" {
		t.Errorf("web: %q %q", l, b)
	}
	if l, _ := sureness(cfg, []Fact{{Tool: "calendar.list", Result: 1}}, true); l != "sure" {
		t.Errorf("calendar: %q", l)
	}
	if l, _ := sureness(cfg, []Fact{{Tool: "calendar.add", Error: "no"}}, true); l != "unsure" {
		t.Errorf("failed tool: %q", l)
	}
	if l, _ := sureness(cfg, nil, true); l != "" {
		t.Errorf("plain talk should carry no mark: %q", l)
	}

	r := newRig(t)
	reply, _ := r.say("hello there")
	before := r.b.Mood().Confidence
	if err := r.b.Feedback(reply.Event.ID, false); err != nil {
		t.Fatal(err)
	}
	if r.b.Mood().Confidence >= before {
		t.Error("a reply marked not good should cost her some confidence")
	}
	r.b.Feedback(reply.Event.ID, false) // twice counts once
	evs, _ := r.b.S.RecentEvents(10, "feedback")
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "did not like") || !strings.Contains(evs[0].Text, "hello there") {
		t.Fatalf("the verdict should be written down once, with what was asked: %+v", evs)
	}
}

func TestAFalseClaimIsNeverSent(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		return "You're right, I'm sorry. I've taken them off. Anything else?", nil // both drafts claim it
	}
	reply, _ := r.say("no that's wrong, I never asked for batteries")
	if strings.Contains(reply.Text, "taken them off") || !strings.Contains(reply.Text, "have not changed") {
		t.Fatalf("a claim of work that never happened went out: %q", reply.Text)
	}
}

func TestListRemove(t *testing.T) {
	r := newRig(t)
	r.b.S.AddListItem("shopping", "batteries", r.now)
	r.b.S.AddListItem("shopping", "milk", r.now)
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"lists","mode":"once","steps":[{"tool":"lists.remove","list":"shopping","items":["batteries"]}]}`, nil
	}
	r.say("take the batteries off the shopping list")
	items, _ := r.b.S.ListItems("shopping", r.now)
	if len(items) != 1 || items[0].Text != "milk" {
		t.Fatalf("batteries should be gone: %+v", items)
	}
}

func TestSheAsksEvenAfterAQuestionOfHerOwn(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	r.b.S.SavePerson(store.Person{Name: "Bethany", Relationship: "wife"}, r.now)
	r.b.S.SetJSON("nicknames", []Nickname{{Name: "Boo", For: "unknown"}})
	r.b.wonder(cfg, "How long have you been diving?", "I would like to know.")
	r.b.wonderAboutGaps(cfg)
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		told := q.Messages[len(q.Messages)-1].Content
		if strings.Contains(told, "wanting to ask") {
			if !strings.Contains(told, `Who do you mean when you say "Boo"?`) {
				t.Errorf("the hole in what she knows should be asked first:\n%s", told)
			}
			return "That sounds lovely. Who do you mean when you say Boo?", nil
		}
		return "Good to see you. How was your day?", nil // she ends on a question of her own
	}
	r.say("hello there")
	reply, _ := r.say("just had a nice walk by the sea")
	if !strings.HasSuffix(reply.Text, "Boo?") {
		t.Fatalf("her own earlier question should not stop her asking, or cut the question off: %q", reply.Text)
	}
	var after string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		after = q.Messages[len(q.Messages)-1].Content
		return "Boo it is.", nil
	}
	r.say("Boo is Bethany, it's what I call her")
	if !strings.Contains(after, `"Boo" is Thomas's name for Bethany`) {
		t.Fatalf("she should know whose name it is before she answers:\n%s", after)
	}
	for _, n := range r.b.Nicknames() {
		if n.Name == "Boo" && n.For != "Bethany" {
			t.Fatalf("the answer should settle whose name it is: %+v", n)
		}
	}
}

func TestNamesMatchAsWholeWords(t *testing.T) {
	names := PersonNames(store.Person{Name: "Tom Bonnici", Aliases: []string{"Tommy"}})
	for text, want := range map[string]bool{
		"I'll see you tomorrow.":    false,
		"Tom's bike is broken.":     true,
		"Tommy came over.":          true,
		"Bonnici family dinner.":    false,
		"we linked the two topics.": false,
	} {
		if got := Mentions(text, names); got != want {
			t.Errorf("Mentions(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestWhatTheUserSetIsNotOverwritten(t *testing.T) {
	r := newRig(t)
	p, _ := r.b.S.SavePerson(store.Person{Name: "Bethany", Closeness: 0.3}, r.now)
	rel := "friend"
	if _, err := r.b.EditPerson(p.ID, PersonEdit{Relationship: &rel}); err != nil {
		t.Fatal(err)
	}
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[],"people":[{"name":"Bethany","relationship":"wife","note":"Works nights."}]}`, nil
	}
	if _, err := r.say("My wife Bethany works nights."); err != nil {
		t.Fatal(err)
	}
	got, _ := r.b.S.FindPerson("bethany")
	if got.Relationship != "friend" || got.Closeness != 0.7 || !strings.Contains(got.Notes, "Works nights.") {
		t.Fatalf("a relationship the user set should stand, notes still grow: %+v", got)
	}
}

func TestMergingKeepsNamesNotesAndRelationship(t *testing.T) {
	r := newRig(t)
	beth, _ := r.b.S.SavePerson(store.Person{Name: "Beth", Notes: "Says hi.", Closeness: 0.3}, r.now)
	bethany, _ := r.b.S.SavePerson(store.Person{Name: "Bethany", Relationship: "wife", Closeness: 0.9, Notes: "Works nights."}, r.now)
	into, err := r.b.MergePerson(beth.ID, bethany.ID)
	if err != nil {
		t.Fatal(err)
	}
	people, _ := r.b.S.People()
	if len(people) != 1 || into.Relationship != "wife" || len(into.Aliases) != 1 || into.Aliases[0] != "Beth" || into.Notes != "Works nights.\nSays hi." {
		t.Fatalf("the duplicate should fold into the real person: %+v %+v", into, people)
	}
}

func TestAForgottenPersonStaysForgotten(t *testing.T) {
	r := newRig(t)
	p, _ := r.b.S.SavePerson(store.Person{Name: "Rex", Closeness: 0.3}, r.now)
	if err := r.b.ForgetPerson(p.ID); err != nil {
		t.Fatal(err)
	}
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[],"people":[{"name":"Rex","relationship":"","note":"Barks at night."}]}`, nil
	}
	if _, err := r.say("Rex kept me up all night."); err != nil {
		t.Fatal(err)
	}
	if people, _ := r.b.S.People(); len(people) != 0 {
		t.Fatalf("a forgotten person should not come back: %+v", people)
	}
}

func TestPersonViewCountsMentionsAndConnections(t *testing.T) {
	r := newRig(t)
	link, _ := r.b.S.SavePerson(store.Person{Name: "Link", Relationship: "son", Closeness: 0.9}, r.now)
	r.b.S.SavePerson(store.Person{Name: "Bethany", Relationship: "wife", Closeness: 0.9}, r.now)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "Bethany and Link went to the beach.", CreatedAt: r.now})
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: "The two topics are linked.", CreatedAt: r.now})
	r.b.S.AddEvent(store.Event{At: r.now.Add(-time.Hour), Kind: "user_msg", Text: "Link lost a tooth."})
	r.b.S.AddEvent(store.Event{At: r.now.Add(-20 * 24 * time.Hour), Kind: "user_msg", Text: "Link started school."})
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "user_msg", Text: "Here is the link to it."})
	view, err := r.b.Person(link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Memories) != 1 || view.Mentions != 2 || view.Weeks[11] != 1 || view.Weeks[9] != 1 {
		t.Fatalf("only whole-word mentions should count: %+v", view)
	}
	if len(view.Connections) != 1 || view.Connections[0].Name != "Bethany" {
		t.Fatalf("Bethany is named alongside Link: %+v", view.Connections)
	}
}

func TestTalkingOverHerStopsTheReply(t *testing.T) {
	r := newRig(t)
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) {
		// The user cuts in while she is still writing.
		if !r.b.Interrupt() {
			t.Error("there was a reply to stop")
		}
		return "Let me tell you all about it.", nil
	}
	_, err := r.say("Tell me about Lisbon")
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	msgs, _ := r.b.S.SessionEvents(r.b.sessionFor(r.now, "user").ID, 0, "user_msg", "nova_msg")
	if len(msgs) != 1 || msgs[0].Kind != "user_msg" {
		t.Fatalf("thread = %+v, want only what the user said", msgs)
	}
	if r.b.Interrupt() {
		t.Error("nothing should be left to stop between turns")
	}
	r.fake.Handlers["writer"] = func(llm.Request) (string, error) { return "Sounds good.", nil }
	if reply, err := r.say("Actually, tell me about Porto"); err != nil || reply.Text == "" {
		t.Fatalf("the next message was not answered: %v", err)
	}
}

// testConfig is the default configuration with the names the tests use.
func testConfig() config.Config {
	c := config.Default()
	c.UserName, c.CompanionName = "Thomas", "Nova"
	return c
}

func TestWhatSheOverhearsIsNeverKept(t *testing.T) {
	r := newRig(t)
	secret := "the surprise party is on Saturday at Mark's"
	r.b.Overhear(secret)
	var notes string
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		notes = q.Messages[len(q.Messages)-1].Content
		return "Sounds good.", nil
	}
	ctx := WithSaid(context.Background(), Said{Aloud: true, Room: r.b.Room()})
	if _, err := r.b.HandleMessage(ctx, "what do you think of that?"); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	if !strings.Contains(notes, secret) {
		t.Errorf("the reply should be able to see what was just said nearby:\n%s", notes)
	}
	if r.b.Room() != "" {
		t.Error("what was overheard is shown to one reply only")
	}
	evs, _ := r.b.S.EventsBetween(r.now.Add(-24*time.Hour), r.now.Add(24*time.Hour))
	for _, e := range evs {
		if strings.Contains(e.Text, "surprise party") {
			t.Errorf("overheard words were stored in a %s event", e.Kind)
		}
	}
	mems, _ := r.b.S.Memories()
	for _, m := range mems {
		if strings.Contains(m.Content, "surprise party") {
			t.Errorf("overheard words became a memory: %q", m.Content)
		}
	}
}

func TestNothingIsOverheardWhenTheRoomIsOff(t *testing.T) {
	r := newRig(t)
	c := r.b.Cfg.Get()
	c.RoomMinutes = 0
	r.b.Cfg.Set(c)
	r.b.Overhear("anything at all")
	if r.b.Room() != "" {
		t.Error("with the room off nothing is kept")
	}
}
