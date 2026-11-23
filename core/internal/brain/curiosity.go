package brain

import (
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/store"
)

// Curiosity is how she grows by asking. While she sleeps she notes what
// she does not know about the user's life; awake, she asks one of those
// questions when a conversation has room for it. A question is never a
// reach-out of its own: it waits for a moment in a chat.

const (
	curiosityOpen  = 5                // questions she holds at once
	curiosityRest  = 40 * time.Minute // between one question and the next
	curiosityKeeps = 10 * 24 * time.Hour
)

// wonder adds a question to the ones she wants to ask, unless she is
// already holding it or has too many.
func (b *Brain) wonder(cfg config.Config, question, why string) bool {
	question = strings.TrimSpace(question)
	if question == "" || !strings.HasSuffix(question, "?") || !plainQuestion(question) {
		return false
	}
	held, _ := b.S.Intentions()
	open := 0
	now := b.Now()
	for _, i := range held {
		if i.Kind != "curiosity" {
			continue
		}
		// The same question, or one on the same subject: still waiting, or
		// asked or settled within the keep window. Asking about the same
		// thing again a few days later is nagging.
		if norm(i.Text) == norm(question) || ((i.Status == "open" || now.Sub(i.CreatedAt) < curiosityKeeps) && sharesSubject(cfg.UserName, i.Text, question)) {
			return false
		}
		if i.Status == "open" {
			open++
		}
	}
	if open >= curiosityOpen {
		return false
	}
	b.S.AddIntention(store.Intention{Kind: "curiosity", Text: question, Reason: strings.TrimSpace(why),
		TriggerAt: now, ExpiresAt: now.Add(curiosityKeeps), Importance: 2, CreatedAt: now})
	return true
}

// wonderAboutGaps adds the questions that follow from plain holes in
// what she knows: a pet name with no owner, a person with no relation.
func (b *Brain) wonderAboutGaps(cfg config.Config) {
	for _, n := range b.Nicknames() {
		if n.For == "unknown" {
			b.wonder(cfg, "Who do you mean when you say \""+n.Name+"\"?", "They used the name and I do not know whose it is.")
		}
	}
	people, _ := b.S.People()
	for _, p := range people {
		// Notes that already explain them are an answer; a name that is
		// another person's pet name is not a person to ask about.
		if strings.TrimSpace(p.Relationship) == "" && strings.TrimSpace(p.Notes) == "" && norm(p.Name) != norm(cfg.UserName) && !isAliasOfAnother(p, people) {
			b.wonder(cfg, "Who is "+p.Name+" to you?", "They mentioned "+p.Name+" and I do not know who that is.")
		}
	}
}

// isAliasOfAnother reports whether a person's name is an alias of somebody
// else in the list.
func isAliasOfAnother(p store.Person, people []store.Person) bool {
	for _, o := range people {
		if o.ID == p.ID {
			continue
		}
		for _, a := range o.Aliases {
			if norm(a) == norm(p.Name) {
				return true
			}
		}
	}
	return false
}

// stepping reports a message that says they are leaving, resting or not
// well. Then is no time for a new question.
func stepping(text string) bool {
	padded := " " + norm(text) + " "
	for _, w := range []string{" nap ", " sleep ", " bed ", " rest ", " lie down ", " going out ", " break ", " brb ", " cigarette ", " cigy ", " smoke ",
		" not feeling well ", " unwell ", " headache ", " tired ", " exhausted ", " good night ", " goodnight "} {
		if strings.Contains(padded, w) {
			return true
		}
	}
	return false
}

// plainQuestion keeps out the questions a small model likes to write and
// nobody likes to be asked: about what a word "means to you", or with
// three clauses. A friend's question is short and about something real.
func plainQuestion(q string) bool {
	n := " " + norm(q) + " "
	for _, bad := range []string{" mean to you ", " means to you ", " deeper ", " besides ", " dynamic ", " signify ", " represent ", " how does that make you feel ", " line "} {
		if strings.Contains(n, bad) {
			return false
		}
	}
	return len(strings.Fields(q)) <= 16
}

// questionToAsk picks the question to put in this reply, if the moment
// has room for one: an ordinary chat, nothing urgent, not while being
// corrected, not as the very first thing, and not too soon after her
// last one. When she asks it, it is the only question in the reply.
func (b *Brain) questionToAsk(intent, text string, recent []store.Event) (store.Intention, bool) {
	if intent != "chat" || urgent(text, recent) || corrects(text) || stepping(text) || len(recent) < 2 {
		return store.Intention{}, false
	}
	// They are asking her something: that comes first.
	if strings.HasSuffix(strings.TrimSpace(text), "?") {
		return store.Intention{}, false
	}
	if last, err := time.Parse(time.RFC3339, b.S.Get("curiosity_asked_at")); err == nil && b.Now().Sub(last) < curiosityRest {
		return store.Intention{}, false
	}
	// A hole in what she knows (whose name is this?) before anything she
	// merely wonders about.
	open, _ := b.S.Intentions("open")
	var pick *store.Intention
	for n := range open {
		i := open[n]
		if i.Kind != "curiosity" {
			continue
		}
		if strings.HasPrefix(i.Text, "Who ") {
			return i, true
		}
		if pick == nil {
			pick = &open[n]
		}
	}
	if pick != nil {
		return *pick, true
	}
	return store.Intention{}, false
}

// learnFromAnswer takes in what the user has just told her about a pet
// name she did not understand. "Boo is Bethany" settles it there and
// then: the name is Bethany's from now on, and any question she was
// holding about it is done.
func (b *Brain) learnFromAnswer(said string) {
	names := b.Nicknames()
	padded := " " + norm(said) + " "
	changed := false
	for n := range names {
		if names[n].For != "unknown" || !strings.Contains(padded, " "+norm(names[n].Name)+" ") {
			continue
		}
		var owner *store.Person
		for _, word := range strings.Fields(namesIn(said)) {
			p, ok := b.S.FindPerson(strings.ToLower(word))
			if !ok || norm(p.Name) == norm(names[n].Name) || norm(word) == norm(names[n].Name) {
				continue
			}
			owner = &p
			break
		}
		// "Boo is my wife" names no one, but only one person is her.
		if owner == nil {
			owner = b.onlyKin(padded)
		}
		if owner == nil {
			continue
		}
		names[n].For = owner.Name
		changed = true
		b.addAlias(*owner, names[n].Name, b.Now())
		held, _ := b.S.Intentions("open", "sent")
		for _, i := range held {
			if i.Kind == "curiosity" && strings.Contains(" "+norm(i.Text)+" ", " "+norm(names[n].Name)+" ") {
				b.S.SetIntentionStatus(i.ID, "done", b.Now())
			}
		}
	}
	if changed {
		b.S.SetJSON("nicknames", names)
	}
}

// onlyKin finds the one person a message points to by a kin word ("my
// wife"), when exactly one known person has that relationship.
func (b *Brain) onlyKin(padded string) *store.Person {
	people, _ := b.S.People()
	var found *store.Person
	for _, kin := range kinWords {
		if !strings.Contains(padded, " "+kin+" ") {
			continue
		}
		for i := range people {
			if !strings.Contains(" "+norm(people[i].Relationship)+" ", " "+kin+" ") {
				continue
			}
			if found != nil && found.ID != people[i].ID {
				return nil
			}
			found = &people[i]
		}
	}
	return found
}

// asked records that a question went out, so it is not asked again and
// the next message can settle it.
func (b *Brain) asked(i store.Intention) {
	b.S.SetIntentionStatus(i.ID, "sent", b.Now())
	b.S.Set("curiosity_asked_at", b.Now().Format(time.RFC3339))
}

// knownFacts lists what she knows about the user, newest last.
func (b *Brain) knownFacts(limit int) string {
	mems, _ := b.S.Memories()
	lines := []string{}
	for _, m := range mems {
		if m.Kind == "fact" {
			lines = append(lines, "- "+m.Content)
		}
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	if len(lines) == 0 {
		return "(nothing yet)"
	}
	return strings.Join(lines, "\n")
}

// knownAnswer reports whether a question asks who somebody is when she
// already knows who they are.
func knownAnswer(cfg config.Config, question string, b *Brain) bool {
	for _, name := range strings.Fields(namesIn(question)) {
		if p, ok := b.S.FindPerson(strings.ToLower(name)); ok && strings.TrimSpace(p.Relationship) != "" && strings.HasPrefix(norm(question), "who is") {
			return true
		}
	}
	return false
}
