package brain

import (
	"strings"
)

// The checks in this file catch what a review of her replies found her
// getting wrong most often: saying a job was done when the tool had
// failed, saying she could see or hear what she could not, stopping in
// the middle of a sentence, asking again what she had already asked, and
// bringing the same thing up reply after reply.

// changeTools are the tools that change something of the user's. A reply
// may only say it added, removed or changed something when one of them
// worked.
var changeTools = map[string]bool{
	"calendar.add": true, "calendar.remove": true,
	"lists.add": true, "lists.done": true, "lists.remove": true,
}

// claimsChange reports whether a draft says she has just changed something:
// added it, removed it, renamed it, updated it.
func claimsChange(draft string) bool {
	d := " " + norm(draft) + " "
	for _, w := range []string{" i ve added ", " i added ", " i have added ", " i ve put ", " i put it ", " i ve removed ", " i removed ",
		" i ve taken ", " i took it off ", " i took them off ", " i ve cancelled ", " i cancelled ", " i ve canceled ", " i canceled ",
		" i ve marked ", " i marked ", " i ve ticked ", " i ticked ", " i ve scheduled ", " i scheduled ", " i ve set a reminder ",
		" i ve updated ", " i have updated ", " i updated ", " i ve renamed ", " i renamed ", " i ve changed ", " i changed the ",
		" i ve created ", " i created ", " i ve made a separate ", " i ve made a new ",
		" is on the list now ", " is on your list now ", " are on your list now ", " is on the list ", " now on your list "} {
		if strings.Contains(d, w) {
			return true
		}
	}
	return false
}

// changedSomething reports whether a tool that changes things worked this
// turn. A list step that was left alone ("added": false) changed nothing.
func changedSomething(facts []Fact) bool {
	for _, f := range facts {
		if !changeTools[f.Tool] || f.Error != "" {
			continue
		}
		if m, ok := f.Result.(map[string]any); ok && m["added"] == false {
			continue
		}
		return true
	}
	return false
}

// toolErrors lists what each failed tool said, for the rewrite.
func toolErrors(facts []Fact) string {
	lines := []string{}
	for _, f := range facts {
		if f.Error != "" {
			lines = append(lines, f.Tool+": "+clip(f.Error, 160))
		}
	}
	return strings.Join(lines, "; ")
}

// withoutChangeClaims takes out the sentences that say something was
// changed, and says plainly that it was not.
func withoutChangeClaims(draft string) string {
	kept := []string{}
	for _, line := range splitSentences(draft) {
		if !claimsChange(line) {
			kept = append(kept, line)
		}
	}
	kept = append(kept, "To be straight with you: that did not go through, so nothing was changed.")
	return strings.Join(kept, " ")
}

// claimsSenses reports whether a draft says she sees or hears something
// right now. Without a picture with the message she has only words, and
// "I see the camera feed now" is made up.
func claimsSenses(draft string) bool {
	d := " " + norm(draft) + " "
	for _, w := range []string{" camera feed ", " i can see you ", " i see you now ", " i can see him ", " i can see her ", " i can see them ",
		" i can see the ", " i saw you ", " i m watching you ", " i am watching you ", " watching you work ", " seeing you in real time ",
		" i heard her ", " i heard him ", " i heard them ", " i can hear her ", " i can hear him ", " i can hear the ", " humming "} {
		if strings.Contains(d, w) {
			return true
		}
	}
	return false
}

// unfinished reports whether a draft stops in the middle of a sentence:
// "The feed is", "Here it is:". Usually what was cut off was the part
// they asked for.
func unfinished(draft string) bool {
	d := strings.TrimSpace(draft)
	if d == "" {
		return false
	}
	if strings.HasSuffix(d, ":") || strings.HasSuffix(d, ",") {
		return true
	}
	words := strings.Fields(norm(d))
	if len(words) == 0 {
		return false
	}
	last := d[len(d)-1]
	if last == '.' || last == '!' || last == '?' || last == ')' || last == '"' {
		return false
	}
	switch words[len(words)-1] {
	case "is", "are", "was", "the", "a", "an", "to", "at", "of", "and", "or", "for", "your", "my":
		return true
	}
	return false
}

// sameQuestion reports whether two questions ask the same thing: most of
// the words of the shorter one are in the other.
func sameQuestion(a, b string) bool {
	if norm(a) == norm(b) {
		return true
	}
	// Two questions about the same name ask the same thing: "Who is Boo
	// again?" after "Who do you mean when you say Boo?".
	for _, n := range askedNames(a) {
		for _, m := range askedNames(b) {
			if n == m {
				return true
			}
		}
	}
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if len(ta) > len(tb) {
		ta, tb = tb, ta
	}
	have := map[string]bool{}
	for _, t := range tb {
		have[t] = true
	}
	shared := 0
	for _, t := range ta {
		if have[t] {
			shared++
		}
	}
	return shared >= 2 && float64(shared) >= 0.6*float64(len(ta))
}

// questionWords open a question with a capital letter without being a
// name.
var questionWords = map[string]bool{"who": true, "what": true, "when": true, "where": true, "why": true, "how": true, "did": true,
	"does": true, "are": true, "can": true, "could": true, "would": true, "will": true, "have": true, "has": true, "the": true, "you": true}

// askedNames returns the names a question is about.
func askedNames(q string) []string {
	out := []string{}
	for _, w := range strings.Fields(namesIn(q)) {
		if n := norm(w); !questionWords[n] && !ordinaryWord[n] && !strings.HasPrefix(w, "I'") {
			out = append(out, n)
		}
	}
	return out
}

// questionsIn returns the sentences of a text that are questions.
func questionsIn(text string) []string {
	out := []string{}
	for _, s := range splitSentences(text) {
		if strings.HasSuffix(s, "?") {
			out = append(out, s)
		}
	}
	return out
}

// dropAskedQuestions takes out of a draft a question she has already
// asked. Once a question is in the thread a small model copies it into
// every reply: "Who is Boo?" went out seven times in one morning, after
// it had been answered.
func dropAskedQuestions(draft string, asked []string) string {
	if len(asked) == 0 {
		return draft
	}
	kept := []string{}
	for _, s := range splitSentences(draft) {
		repeat := false
		if strings.HasSuffix(s, "?") {
			for _, a := range asked {
				repeat = repeat || sameQuestion(s, a)
			}
		}
		if !repeat {
			kept = append(kept, s)
		}
	}
	// A draft that was nothing but the question is kept: a repeat is
	// better than no reply at all.
	if len(kept) == 0 {
		return draft
	}
	return strings.Join(kept, " ")
}

// plainWords are words long enough to count as a topic that are not one:
// they turn up in any reply.
var plainWords = map[string]bool{
	"sounds": true, "glad": true, "right": true, "think": true, "really": true, "going": true, "today": true,
	"thing": true, "things": true, "little": true, "great": true, "happy": true, "maybe": true, "enough": true,
	"before": true, "after": true, "still": true, "again": true, "while": true, "those": true, "these": true,
	"feels": true, "something": true, "everything": true, "anything": true, "nothing": true, "always": true,
	"wonderful": true, "though": true, "would": true, "could": true, "should": true, "where": true, "which": true,
	"about": true, "there": true, "their": true, "other": true, "every": true, "first": true,
	"thank": true, "thanks": true, "sorry": true, "whenever": true, "ready": true, "means": true, "makes": true,
}

// staleTopics finds the words she has used in several of her recent
// replies that the user has not brought up themselves: the Bluetooth
// mouse asked after six times, the cigarettes five times in half an hour.
// often is how many replies a word must be in to count.
func staleTopics(replies []string, said string, skip []string, often int) []string {
	theirs := map[string]bool{}
	for _, t := range tokens(said) {
		theirs[t] = true
	}
	for _, s := range skip {
		for _, t := range tokens(s) {
			theirs[t] = true
		}
	}
	count := map[string]int{}
	order := []string{}
	for _, r := range replies {
		seen := map[string]bool{}
		for _, t := range tokens(r) {
			if len(t) < 5 || plainWords[t] || theirs[t] || seen[t] {
				continue
			}
			seen[t] = true
			if count[t] == 0 {
				order = append(order, t)
			}
			count[t]++
		}
	}
	out := []string{}
	for _, t := range order {
		if count[t] >= often {
			out = append(out, t)
		}
	}
	return out
}

// dropStale takes out the sentences that bring up a worn-out topic. A
// draft that would be left with nothing is kept as it was.
func dropStale(draft string, stale []string) string {
	if len(stale) == 0 {
		return draft
	}
	bad := map[string]bool{}
	for _, s := range stale {
		bad[s] = true
	}
	kept := []string{}
	for _, s := range splitSentences(draft) {
		worn := false
		for _, t := range tokens(s) {
			worn = worn || bad[t]
		}
		if !worn {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return draft
	}
	return strings.Join(kept, " ")
}

// asksWeather reports whether a message asks about the weather, so a
// question the planner filed as chat still gets the forecast.
func asksWeather(text string) bool {
	t := " " + norm(text) + " "
	asking := strings.HasSuffix(strings.TrimSpace(text), "?")
	for _, w := range []string{" how strong ", " what s the weather ", " what is the weather ", " check the weather ", " will it rain ", " is it going to rain ", " forecast "} {
		asking = asking || strings.Contains(t, w)
	}
	if !asking {
		return false
	}
	for _, w := range []string{" wind ", " windy ", " rain ", " raining ", " weather ", " forecast ", " temperature ", " storm ", " degrees ", " sunny ", " cold outside ", " hot outside "} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}
