package voice

import "testing"

func TestHerNameIsCaughtHoweverItIsSpelled(t *testing.T) {
	for heard, want := range map[string]bool{
		"Clarke, what's the weather?":       true,
		"Hey Clark, add milk to the list":   true,
		"clerk can you hear me":             true,
		"Klark?":                            true,
		"okay Claire what time is it":       true,
		"Clarkey, you there?":               true,
		"click the button and wait":         false,
		"the clock is slow":                 false,
		"I told Mark we would go tomorrow.": false,
		"we went to the park":               false,
		"":                                  false,
	} {
		if got := Addressed(heard, "Clarke", nil); got != want {
			t.Errorf("Addressed(%q) = %v, want %v", heard, got, want)
		}
	}
}

func TestANameHeardAsTwoWords(t *testing.T) {
	if !Addressed("no va are you there", "Nova", nil) {
		t.Error(`"no va" should be heard as Nova`)
	}
	if Addressed("there is no way", "Nova", nil) {
		t.Error(`"no way" is not Nova`)
	}
}

func TestOtherSpellingsTheUserAdded(t *testing.T) {
	if Addressed("Sparky, lights off", "Clarke", nil) {
		t.Error("Sparky is not Clarke")
	}
	if !Addressed("Sparky, lights off", "Clarke", []string{"Sparky"}) {
		t.Error("an added name should be answered to")
	}
}

func TestTheNameIsASettingNotAWord(t *testing.T) {
	if !Addressed("Juniper, are you awake?", "Juniper", nil) || Addressed("Juniper, are you awake?", "Clarke", nil) {
		t.Error("only the configured name should match")
	}
}

func TestEnding(t *testing.T) {
	for heard, want := range map[string]bool{
		"thanks Clarke":        true,
		"That's all for now.":  true,
		"okay bye":             true,
		"what about tomorrow":  false,
		"thanksgiving is soon": false,
	} {
		if Ending(heard) != want {
			t.Errorf("Ending(%q) = %v", heard, !want)
		}
	}
}
