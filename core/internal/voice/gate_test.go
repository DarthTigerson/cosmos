package voice

import "testing"

func TestTheGate(t *testing.T) {
	byName := Gate{ByName: true}
	cases := []struct {
		name string
		gate Gate
		said Said
		want Verdict
	}{
		{"her own voice", byName, Said{Words: "It is sunny today.", Who: "her"}, Hers},
		{"a recogniser's ghost", byName, Said{Words: "Thank you.", Addressed: false}, Noise},
		{"you, by name", byName, Said{Words: "Clarke, what time is it?", Addressed: true, Who: "you"}, Answer},
		{"you, without her name", byName, Said{Words: "what time is it?", Who: "you"}, Overheard},
		{"you, while talking with her", Gate{ByName: true, Open: true}, Said{Words: "and tomorrow?", Who: "you"}, Answer},
		{"someone else, by name", byName, Said{Words: "Clarke, hello", Addressed: true, Who: "someone"}, Overheard},
		{"someone else, when others are answered", Gate{ByName: true, AnswerOthers: true}, Said{Words: "Clarke, hello", Addressed: true, Who: "person"}, Answer},
		{"anyone, answering everything", Gate{}, Said{Words: "what time is it?", Who: "someone"}, Answer},
		{"no voices known yet", byName, Said{Words: "Clarke, hi", Addressed: true}, Answer},
	}
	for _, c := range cases {
		if got := c.gate.Decide(c.said); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
