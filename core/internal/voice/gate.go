package voice

import "strings"

// The gate: what she makes of something said, once it is written out
// and she knows whose voice it was. The call and the voice check both
// decide through it, so what is tested is what she does.

// Verdict is what something said is to her.
type Verdict string

const (
	// Hers: her own voice, back through the speakers.
	Hers Verdict = "hers"
	// Noise: nothing a person said, though the recogniser wrote something.
	Noise Verdict = "noise"
	// Overheard: said nearby, not to her. Kept a few minutes, unanswered.
	Overheard Verdict = "overheard"
	// Answer: said to her.
	Answer Verdict = "answer"
)

// Things a speech recogniser writes when it is handed a noise.
var notSpeech = map[string]bool{"you": true, "thank you.": true, "thanks for watching!": true, "thank you for watching.": true, "bye.": true, ".": true, "okay.": true}

// Said is what is known about one thing said.
type Said struct {
	Words string
	// Addressed: her name, or something like it, is in it.
	Addressed bool
	// Who spoke: "you", "her", "person", "someone", or "" when she knows
	// no voices yet.
	Who string
}

// Gate is how she is listening.
type Gate struct {
	// ByName: she waits for her name. Off, everything said is for her.
	ByName bool
	// Open: she is talking with someone, so her name is not needed.
	Open bool
	// AnswerOthers: someone other than the user who says her name is
	// answered too.
	AnswerOthers bool
}

// Decide is what she makes of it.
func (g Gate) Decide(s Said) Verdict {
	words := strings.TrimSpace(s.Words)
	if s.Who == "her" {
		return Hers
	}
	if len(words) <= 1 || notSpeech[strings.ToLower(words)] {
		return Noise
	}
	if !g.ByName {
		return Answer
	}
	// Someone else, when she answers only the user: what they say is
	// what is going on around her, even with her name in it.
	if !g.AnswerOthers && (s.Who == "person" || s.Who == "someone") {
		return Overheard
	}
	if s.Addressed || g.Open {
		return Answer
	}
	return Overheard
}
