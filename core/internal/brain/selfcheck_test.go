package brain

import (
	"context"
	"strings"
	"testing"

	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

func TestSelfCheckReportsWhatFailed(t *testing.T) {
	r := newRig(t)
	failed := store.Step{Name: "lists.add", Tier: "tool", OK: false,
		Note: `nothing was added: say which things should go on the to-do list (asked with {"items":["secure balcony items"]})`}
	for i := 0; i < 3; i++ {
		r.b.S.AddRun(store.Run{At: r.now, Request: "add that", Intent: "lists", Mode: "once", Verified: true,
			Steps: []store.Step{{Name: "route", Tier: "worker", OK: true}, failed, {Name: "first audio", Tier: "voice", OK: true, Millis: 15000}}})
	}
	r.b.Health = func(context.Context) map[string]ServerHealth {
		return map[string]ServerHealth{"main": {State: "running"}, "embed": {State: "failed", Error: "model file not found"}}
	}
	var asked string
	r.fake.Handlers["route"] = func(llm.Request) (string, error) {
		return `{"intent":"self","mode":"once","steps":[{"tool":"self.check"}]}`, nil
	}
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		asked = q.Messages[len(q.Messages)-1].Content
		return "My list tool turned down the same item three times.", nil
	}
	reply, err := r.say("your logs say you are uneasy, what is wrong?")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lists.add", `"times": 3`, "nothing was added", "model file not found", "15.0", "how you yourself are running"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the writer was not given %q:\n%s", want, asked)
		}
	}
	// The arguments hold the user's words and stay out of the report.
	if strings.Contains(asked, "secure balcony") {
		t.Error("a failed step's arguments reached the report")
	}
	if !reply.Run.Verified {
		t.Errorf("a reply from the report should pass its checks: %+v", reply.Run.Steps)
	}
}

func TestApprovedBugGoesOnThePlatformList(t *testing.T) {
	r := newRig(t)
	p, _ := r.b.S.AddProposal(store.Proposal{Kind: "bug", After: "lists.add turned down 3 items the user asked for", Status: "pending", CreatedAt: r.now})
	if _, err := r.b.DecideProposal(p.ID, true); err != nil {
		t.Fatal(err)
	}
	items, _ := r.b.S.ListItems("cosmos platform tasks", r.now)
	if len(items) != 1 || !strings.HasPrefix(items[0].Text, "Bug: lists.add") {
		t.Fatalf("the bug did not reach the list: %+v", items)
	}
	if style := r.b.S.Prompt("writer_style"); style != "" {
		t.Errorf("a bug report became a writing rule: %q", style)
	}
	if notes := failureNotes(nil); notes != "(nothing failed)\n" {
		t.Errorf("failureNotes(nil) = %q", notes)
	}
}
