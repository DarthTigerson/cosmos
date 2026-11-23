package brain

import (
	"strings"
	"testing"

	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// updateKeeps runs one update over an old fact and returns what is held
// afterwards.
func updateKeeps(t *testing.T, old, updated, said string) []string {
	t.Helper()
	r := newRig(t)
	r.b.S.AddMemory(store.Memory{Kind: "fact", Content: old, CreatedAt: r.now})
	r.fake.Handlers["capture"] = func(llm.Request) (string, error) {
		return `{"changes":[{"op":"update","ref":1,"category":"family","content":"` + updated + `"}],"people":[]}`, nil
	}
	if _, err := r.say(said); err != nil {
		t.Fatal(err)
	}
	r.b.Wait()
	mems, _ := r.b.S.Memories()
	out := []string{}
	for _, m := range mems {
		out = append(out, m.Content)
	}
	return out
}

func TestAnUpdateThatDropsADetailKeepsTheOldFact(t *testing.T) {
	got := updateKeeps(t, "Thomas's son Link is 7 years old.", "Thomas's son Link is autistic.", "Link is autistic")
	if len(got) != 2 || got[0] != "Thomas's son Link is 7 years old." {
		t.Fatalf("the age must survive: %v", got)
	}
	got = updateKeeps(t, "Thomas has a cat named Grey (Gandalf the Grey).", "Thomas's cat is named Grey.", "my cat is named Grey")
	if len(got) != 1 || !strings.Contains(got[0], "Gandalf") {
		t.Fatalf("the full name must survive and nothing be added: %v", got)
	}
	got = updateKeeps(t, "Thomas's wife is Bethany Bonnici, who loves BTS music.", "Thomas's wife is Boo.", "my wife is Boo")
	if len(got) != 2 || !strings.Contains(got[0], "BTS") {
		t.Fatalf("the name and BTS must survive: %v", got)
	}
}

func TestAnUpdateThatSwapsAValueStillReplaces(t *testing.T) {
	got := updateKeeps(t, "Thomas works at Acme.", "Thomas works at Globex.", "I work at Globex now")
	if len(got) != 1 || got[0] != "Thomas works at Globex." {
		t.Fatalf("a swapped value is a real update: %v", got)
	}
}

func TestWhatIsHappeningNowIsNotAFact(t *testing.T) {
	if !passing(norm("Thomas is going out to smoke a cigarette.")) {
		t.Fatal("a moment-only statement should not be kept")
	}
	if passing(norm("Thomas smokes a lot.")) {
		t.Fatal("a lasting habit should be kept")
	}
}

func TestSoftwareIsNotAPerson(t *testing.T) {
	r := newRig(t)
	r.b.rememberPerson("Claude", "LLM model", "", r.now)
	r.b.rememberPerson("Cursor", "", "an IDE he uses", r.now)
	if people, _ := r.b.S.People(); len(people) != 0 {
		t.Fatalf("software was stored as people: %+v", people)
	}
}

func TestANicknameBecomesAnAliasNotAPerson(t *testing.T) {
	r := newRig(t)
	r.b.rememberPerson("Bethany Bonnici", "wife", "", r.now)
	r.b.rememberPerson("Boo", "wife", "nickname for Bethany Bonnici", r.now)
	people, _ := r.b.S.People()
	if len(people) != 1 || len(people[0].Aliases) != 1 || people[0].Aliases[0] != "Boo" {
		t.Fatalf("Boo should be an alias: %+v", people)
	}
	r.b.rememberPerson("Sweetie", "", "Bethany's nickname", r.now)
	if p, ok := r.b.S.FindPerson("sweetie"); !ok || p.Name != "Bethany Bonnici" {
		t.Fatalf("possessive form not understood: %+v", p)
	}
}

func TestAnAnswerWithAKinWordSettlesANickname(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	r.b.S.SavePerson(store.Person{Name: "Bethany Bonnici", Relationship: "wife"}, r.now)
	r.b.S.SetJSON("nicknames", []Nickname{{Name: "Boo", For: "unknown"}})
	r.b.wonderAboutGaps(cfg)
	r.b.learnFromAnswer("Boo is my Wife. thats her nickname.")
	if n := r.b.Nicknames(); n[0].For != "Bethany Bonnici" {
		t.Fatalf("nickname not settled: %+v", n)
	}
	if p, ok := r.b.S.FindPerson("boo"); !ok || p.Name != "Bethany Bonnici" {
		t.Fatalf("alias not added: %+v", p)
	}
	if open, _ := r.b.S.Intentions("open"); len(open) != 0 {
		t.Fatalf("the question should be closed: %+v", open)
	}
}

func TestAKinWordIsNotEnoughWhenTwoPeopleFit(t *testing.T) {
	r := newRig(t)
	r.b.S.SavePerson(store.Person{Name: "Anna", Relationship: "friend"}, r.now)
	r.b.S.SavePerson(store.Person{Name: "Maria", Relationship: "friend"}, r.now)
	r.b.S.SetJSON("nicknames", []Nickname{{Name: "Boo", For: "unknown"}})
	r.b.learnFromAnswer("Boo is my friend")
	if n := r.b.Nicknames(); n[0].For != "unknown" {
		t.Fatalf("it should stay unknown: %+v", n)
	}
}

func TestAKnownPetNameIsNotRefiledForSomeone(t *testing.T) {
	r := newRig(t)
	r.b.S.SavePerson(store.Person{Name: "Alistair Sammut", Relationship: "friend"}, r.now)
	r.b.S.SetJSON("nicknames", []Nickname{{Name: "little one", For: "companion"}})
	r.b.S.AddEvent(store.Event{At: r.now, Kind: "user_msg", Text: "hello little one, say hi to Alistair"})
	r.fake.Handlers["nicknames"] = func(llm.Request) (string, error) {
		return `{"nicknames":[{"line":"hello little one","nickname":"little one","for":"person","person":"Alistair Sammut"}]}`, nil
	}
	evs, _ := r.b.S.RecentEvents(5, "user_msg")
	r.b.learnNicknames(t.Context(), r.b.Cfg.Get(), evs)
	if n := r.b.Nicknames(); len(n) != 1 || n[0].For != "companion" {
		t.Fatalf("her pet name was refiled: %+v", n)
	}
}

func TestSheDoesNotAskWhoSomeoneIsWhenNotesSayOrTheyAreAnAlias(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	r.b.S.SavePerson(store.Person{Name: "Ryan", Notes: "former manager"}, r.now)
	r.b.S.SavePerson(store.Person{Name: "Bethany", Relationship: "wife", Aliases: []string{"Boo"}}, r.now)
	r.b.S.SavePerson(store.Person{Name: "Boo"}, r.now)
	r.b.wonderAboutGaps(cfg)
	if open, _ := r.b.S.Intentions("open"); len(open) != 0 {
		t.Fatalf("nothing should be asked: %+v", open)
	}
}

func TestNoNewQuestionWhenTheyAreLeavingOrUnwell(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	r.b.wonder(cfg, "Who is Ryan to you?", "x")
	recent := []store.Event{{Kind: "user_msg", Text: "hi"}, {Kind: "assistant_msg", Text: "hello"}}
	if _, ok := r.b.questionToAsk("chat", "im going to nap for a moment", recent); ok {
		t.Fatal("she asked while they were going to rest")
	}
	if _, ok := r.b.questionToAsk("chat", "nice walk today", recent); !ok {
		t.Fatal("setup: she should ask in an ordinary moment")
	}
}

func TestAQuestionAlreadyAskedIsNotWonderedAgain(t *testing.T) {
	r := newRig(t)
	cfg := r.b.Cfg.Get()
	r.b.wonder(cfg, "What do you do for work?", "x")
	open, _ := r.b.S.Intentions("open")
	r.b.S.SetIntentionStatus(open[0].ID, "done", r.now)
	if r.b.wonder(cfg, "Where do you work these days?", "x") {
		t.Fatal("a settled question on the same subject was asked again")
	}
	r.now = r.now.Add(curiosityKeeps + 1)
	if !r.b.wonder(cfg, "Where do you work these days?", "x") {
		t.Fatal("after the keep window it may be wondered again")
	}
}

func TestADeclinedProposalCannotBeReadBackwards(t *testing.T) {
	r := newRig(t)
	p, _ := r.b.S.AddProposal(store.Proposal{Kind: "writing", After: "Never use pet names like Father", Status: "pending", CreatedAt: r.now})
	r.b.DecideProposal(p.ID, false)
	evs, _ := r.b.S.RecentEvents(5, "system")
	if len(evs) == 0 || !strings.Contains(evs[0].Text, "NOT how") {
		t.Fatalf("the declined event is ambiguous: %+v", evs)
	}
}
