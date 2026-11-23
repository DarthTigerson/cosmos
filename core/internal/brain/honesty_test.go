package brain

import (
	"strings"
	"testing"

	"cosmos/core/internal/llm"
)

func TestClaimedAddOverFailedToolIsRewritten(t *testing.T) {
	r := newRig(t)
	// The planner writes an item nobody said a word of, so lists.add fails.
	r.fake.Handlers["route"] = func(q llm.Request) (string, error) {
		return `{"intent":"lists","mode":"once","steps":[{"tool":"lists.add","list":"to-do","items":["champagne"]}]}`, nil
	}
	drafts := 0
	r.fake.Handlers["writer"] = func(q llm.Request) (string, error) {
		drafts++
		if strings.Contains(q.System, "nothing was changed") {
			return "That did not go through: the list would not take it.", nil
		}
		return "Got it, I've added that to your to-do list.", nil
	}
	reply, err := r.say("add that to the to-do list please")
	if err != nil {
		t.Fatal(err)
	}
	if drafts != 2 || strings.Contains(reply.Text, "I've added") {
		t.Fatalf("a claim over a failed add went out (%d drafts): %q", drafts, reply.Text)
	}
}

func TestLooseListItem(t *testing.T) {
	said := "its very windy in my front balcony, have to make sure things dont fly off. ok lets add that to your todo please"
	item, ok := mostlySaid("secure balcony items (wind 25 kph)", said)
	if !ok || item != "secure balcony items" {
		t.Fatalf("mostlySaid = %q, %v", item, ok)
	}
	if _, ok := mostlySaid("champagne", "add milk and eggs to the shopping list"); ok {
		t.Error("an item nobody mentioned was let through")
	}
}

func TestClaimsChange(t *testing.T) {
	for draft, want := range map[string]bool{
		"Got it, I've added all three tasks.":           true,
		"I've updated the list title to include today.": true,
		"I'd love to add that, what should it say?":     false,
		"The weather looks clear in Marsaxlokk.":        false,
		"\"fix bluetooth mouse\" is on the list now.":   true,
	} {
		if got := claimsChange(draft); got != want {
			t.Errorf("claimsChange(%q) = %v", draft, got)
		}
	}
	failed := []Fact{{Tool: "lists.add", Error: "nothing was added"}}
	if changedSomething(failed) || changedSomething([]Fact{{Tool: "lists.add", Result: map[string]any{"added": false}}}) {
		t.Error("a failed or skipped add counted as a change")
	}
	if !changedSomething([]Fact{{Tool: "lists.add", Result: map[string]any{"added": []string{"milk"}}}}) {
		t.Error("a working add did not count")
	}
}

func TestSensesAndUnfinished(t *testing.T) {
	if !claimsSenses("That is a bold move. I see the camera feed now.") || !claimsSenses("I heard her humming it earlier.") {
		t.Error("made-up seeing or hearing was not caught")
	}
	if claimsSenses("I see what you mean.") {
		t.Error("\"I see\" as in understand was caught")
	}
	for draft, want := range map[string]bool{
		"There it is: 9to5Mac Happy Hour. The feed is": true,
		"Here it is:": true,
		"Here it is: https://feedpress.me/9to5machappyhour": false,
		"Sleep well.": false,
	} {
		if got := unfinished(draft); got != want {
			t.Errorf("unfinished(%q) = %v", draft, got)
		}
	}
}

func TestAskedQuestionsAreNotAskedAgain(t *testing.T) {
	asked := []string{"Who do you mean when you say \"Boo\"?"}
	got := dropAskedQuestions("I'll keep watching your hours. Who is \"Boo\" again? I think I missed it.", []string{"Who is \"Boo\" you mentioned earlier?"})
	if strings.Contains(got, "Boo") {
		t.Errorf("the Boo question went out again: %q", got)
	}
	if got := dropAskedQuestions("Glad you are back. What are you building today?", asked); !strings.Contains(got, "building") {
		t.Errorf("a new question was dropped: %q", got)
	}
}

func TestStaleTopics(t *testing.T) {
	replies := []string{
		"Did you manage to fix the Bluetooth mouse yet?",
		"Glad the store works. Is the Bluetooth mouse still on your list?",
		"Noon is a good break. Did you fix the Bluetooth mouse?",
	}
	worn := staleTopics(replies, "i did. it is now noon, time for my own break", []string{"Thomas", "Clarke"}, 3)
	if strings.Join(worn, " ") != "bluetooth mouse" {
		t.Fatalf("worn = %v", worn)
	}
	if got := dropStale("Enjoy the break. Did you fix the Bluetooth mouse?", worn); got != "Enjoy the break." {
		t.Errorf("dropStale = %q", got)
	}
	if again := staleTopics(replies, "the bluetooth mouse is still broken", nil, 3); len(again) != 0 {
		t.Errorf("a topic they raised themselves counted as worn: %v", again)
	}
}

func TestAsksWeather(t *testing.T) {
	for said, want := range map[string]bool{
		"Clarke, how strong is the wind?":                  true,
		"do you happen to know the direction of the wind?": true,
		"its very windy in my front balcony":               false,
		"what is vigy?":                                    false,
	} {
		if got := asksWeather(said); got != want {
			t.Errorf("asksWeather(%q) = %v", said, got)
		}
	}
	if compass(315) != "north-west" || compass(0) != "north" || compass(350) != "north" || compass(180) != "south" {
		t.Error("compass points are off")
	}
}

func TestUnsupportedClaimSkipsI(t *testing.T) {
	if w := unsupportedClaim("Here is the feed https://feedpress.me/x I'm glad we found it.", "feed"); w != "" {
		t.Errorf("unsupportedClaim flagged %q", w)
	}
}

func TestBorrowedNameLeavesNoHole(t *testing.T) {
	r := newRig(t)
	r.b.S.SetJSON("nicknames", []Nickname{{Name: "little one", For: "companion"}, {Name: "my little one", For: "companion"}})
	for draft, want := range map[string]string{
		`Yes, that is fine. I will keep calling you "little one" whenever I speak to you.`: "Yes, that is fine.",
		"I'm glad you like being called my little one. Did you rest?":                      "Did you rest?",
		"Good to see you, little one.":                                                     "Good to see you.",
	} {
		if got := r.b.dropBorrowedNames(draft); got != want {
			t.Errorf("dropBorrowedNames(%q) = %q, want %q", draft, got, want)
		}
	}
}
