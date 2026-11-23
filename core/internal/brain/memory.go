package brain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true, "not": true, "you": true, "your": true,
	"with": true, "this": true, "that": true, "have": true, "has": true, "had": true, "was": true, "were": true,
	"will": true, "would": true, "can": true, "could": true, "should": true, "what": true, "when": true,
	"where": true, "who": true, "why": true, "how": true, "all": true, "any": true, "get": true, "got": true,
	"did": true, "about": true, "into": true, "from": true, "they": true, "them": true, "their": true,
	"there": true, "here": true, "then": true, "than": true, "just": true, "like": true, "some": true,
	"more": true, "most": true, "also": true, "been": true, "being": true, "does": true,
}

func tokens(text string) []string {
	out := []string{}
	for _, t := range strings.Fields(norm(text)) {
		if len(t) >= 3 && !stopwords[t] {
			out = append(out, t)
		}
	}
	return out
}

// rankMemories picks the memories worth showing a model for this query:
// keyword overlap first, then importance and recency. Small stores are
// returned whole. The result is oldest first so prompts stay stable.
func rankMemories(mems []store.Memory, query string, now time.Time, limit int) []store.Memory {
	if len(mems) <= limit {
		return mems
	}
	if pinned, rest := splitPinned(mems); len(pinned) > 0 {
		return withPinned(pinned, rankMemories(rest, query, now, max(0, limit-len(pinned))), limit)
	}
	terms := map[string]bool{}
	for _, t := range tokens(query) {
		terms[t] = true
	}
	oldest := now
	for _, m := range mems {
		if m.UpdatedAt.Before(oldest) {
			oldest = m.UpdatedAt
		}
	}
	span := float64(now.Sub(oldest))
	if span < 1 {
		span = 1
	}
	type scored struct {
		m     store.Memory
		score float64
	}
	all := make([]scored, len(mems))
	for i, m := range mems {
		overlap := 0
		for _, t := range tokens(m.Category + " " + m.Content) {
			if terms[t] {
				overlap++
			}
		}
		recency := 1 - float64(now.Sub(m.UpdatedAt))/span
		all[i] = scored{m, float64(overlap)*2 + recency + float64(m.Importance)*0.3}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	picked := make([]store.Memory, limit)
	for i := range picked {
		picked[i] = all[i].m
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].CreatedAt.Before(picked[j].CreatedAt) })
	return picked
}

// splitPinned separates the memories the user pinned from the rest.
func splitPinned(mems []store.Memory) (pinned, rest []store.Memory) {
	for _, m := range mems {
		if m.Importance >= store.Pinned {
			pinned = append(pinned, m)
		} else {
			rest = append(rest, m)
		}
	}
	return pinned, rest
}

// withPinned puts the pinned memories back with the ones ranked from the
// rest. Pinned ones always take their places; past the limit, the newest
// pinned win. The result is oldest first, like any ranking.
func withPinned(pinned, ranked []store.Memory, limit int) []store.Memory {
	if len(pinned) > limit {
		sort.SliceStable(pinned, func(i, j int) bool { return pinned[i].CreatedAt.After(pinned[j].CreatedAt) })
		pinned = pinned[:limit]
	}
	picked := append(append([]store.Memory{}, pinned...), ranked...)
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].CreatedAt.Before(picked[j].CreatedAt) })
	return picked
}

type captureOut struct {
	Changes []struct {
		Op       string `json:"op"`
		Ref      int    `json:"ref"`
		Category string `json:"category"`
		Content  string `json:"content"`
	} `json:"changes"`
	People []struct {
		Name         string `json:"name"`
		Relationship string `json:"relationship"`
		Note         string `json:"note"`
	} `json:"people"`
}

// capture reads one finished exchange and keeps her long-term memory of
// the user up to date: new facts are added, changed facts replace the old
// ones, and people who were mentioned are remembered.
func (b *Brain) capture(ctx context.Context, userText, before string, sourceEvent int64) {
	if strings.TrimSpace(userText) == "" {
		return
	}
	cfg := b.Cfg.Get()
	all, _ := b.S.Memories()
	facts := []store.Memory{}
	for _, m := range all {
		if m.Kind == "fact" {
			facts = append(facts, m)
		}
	}
	// Only the memories near this exchange: enough to spot an update
	// without sending the whole store every turn.
	known := rankMemories(facts, userText+" "+before, b.Now(), 20)
	var list strings.Builder
	for i, m := range known {
		fmt.Fprintf(&list, "#%d [%s] %s\n", i+1, m.Category, m.Content)
	}
	if len(known) == 0 {
		list.WriteString("(none yet)\n")
	}
	system := strings.Join([]string{
		"You keep a long-term memory of durable facts about " + cfg.UserName + ", updated from one exchange.",
		"A durable fact is still true next month: who they are, family, work, health conditions, lasting preferences, ongoing projects.",
		"A milestone or a change in someone's life is durable too: a child starting to crawl, losing a first tooth, a promotion, a move. Write what happened and to whom, using the name they used, and leave the date out: \"" + cfg.UserName + "'s son Tom lost his first tooth.\" It is a new fact (op add), never an update of an older one.",
		"Not durable, leave out: to-dos and plans (\"needs to call the dentist\"), what they need today, passing moods, questions, the weather, the time.",
		"Write each fact in the third person, starting with their name: \"" + cfg.UserName + " has a sister called Maria.\"",
		"",
		"Memory so far:",
		list.String(),
		"changes:",
		`- a new durable fact not covered above: op "add", ref 0.`,
		`- a fact that replaces one above (same subject, new value): op "update", ref = its number.`,
		"- anything already known, or not durable: leave it out.",
		`Example: "my wife is Anna and we have two kids, Tom is 9 and Mia is 2" gives three changes ("` + cfg.UserName + `'s wife is Anna.", "` + cfg.UserName + `'s son Tom is 9 years old.", "` + cfg.UserName + `'s daughter Mia is 2 years old.") and three people (Anna, wife; Tom, son; Mia, daughter). Take every fact in the message, not only the first.`,
		`Never guess what someone is. "Rex kept me up all night" says nothing about who or what Rex is: no change, and Rex with relationship "".`,
		"people: anyone " + cfg.UserName + " mentioned by name, with their relationship to " + cfg.UserName + " (sister, friend, colleague, partner, ...) and one short note. Leave empty if nobody was named.",
		"Most exchanges change nothing. Empty lists are the usual answer.",
	}, "\n")
	var out captureOut
	err := b.ask(ctx, llm.Request{
		Tier: "worker", Role: "capture", System: system,
		// Facts come from the user's newest message only. The lines before
		// it are there to say who "she" or "it" refers to.
		Messages:  []llm.Message{{Role: "user", Content: "Earlier in the conversation, only for working out who or what a word like \"she\" or \"it\" refers to:\n" + before + "\n\nThe new message from " + cfg.UserName + ". Take facts from this message only:\n" + userText}},
		Schema:    captureSchema,
		MaxTokens: 600,
	}, &out)
	if err != nil {
		b.Log.Printf("capture: %v", err)
		return
	}

	// Logged so a review can tell a capture that proposed nothing from one
	// whose proposals were all turned down by the checks below.
	b.Log.Printf("capture proposed: %+v", out)
	now := b.Now()
	seen := map[string]bool{}
	for _, c := range out.Changes {
		content := strings.TrimSpace(c.Content)
		n := norm(content)
		prior := ""
		if c.Op == "update" && c.Ref >= 1 && c.Ref <= len(known) {
			prior = known[c.Ref-1].Content
		}
		// Facts are about the user, never about her. A fact in the first person was copied from the user's words
		// instead of being restated; it would read as hers later.
		if n == "" || seen[n] || strings.HasPrefix(n, "i ") || strings.HasPrefix(n, "my ") || passing(n) || !grounded(cfg.UserName, userText, namesIn(before), prior+" "+b.knownAbout(content, facts), content) || strings.Contains(" "+n+" ", " "+norm(cfg.CompanionName)+" ") {
			continue
		}
		seen[n] = true
		// Something that has just happened is kept with when: "lost his
		// first tooth" means little a year on without it. The month is
		// added here, where the date is known for certain.
		if c.Op == "add" && recent(userText) && len(numbersIn(content)) == 0 {
			// "this week" goes; the month says it better and stays true.
			for _, w := range recencyWords {
				content = strings.ReplaceAll(content, strings.TrimRight(w, " "), "")
			}
			content = strings.TrimSuffix(strings.TrimSpace(content), ".") + " (" + b.Now().Format("January 2006") + ")."
		}
		// Lexical check for repeats the model missed. An update is compared
		// with every memory except the one it replaces: adding a detail to
		// a fact naturally contains the old wording.
		replacing := int64(0)
		if c.Op == "update" && c.Ref >= 1 && c.Ref <= len(known) {
			replacing = known[c.Ref-1].ID
		}
		dup := false
		for _, m := range facts {
			if m.ID == replacing {
				continue
			}
			e := norm(m.Content)
			if e == n || strings.Contains(e, n) || strings.Contains(n, e) {
				dup = true
				break
			}
		}
		if replacing != 0 {
			// An update may only replace a memory about the same thing.
			// Without this a confused model overwrites an unrelated fact.
			// The old wording is kept, set aside and linked to the new one.
			// An "update" that says the same thing changes nothing.
			if old := known[c.Ref-1]; !dup && norm(old.Content) != n && sharesSubject(cfg.UserName, old.Content, content) {
				switch {
				case !dropsDetail(cfg.UserName, old.Content, content):
					b.S.Supersede(old.ID, store.Memory{Kind: "fact", Category: c.Category, Content: content, Importance: old.Importance,
						CreatedAt: now, SourceEvent: sourceEvent, Source: clip(userText, 300)})
				case !coveredBy(cfg.UserName, old.Content, content):
					// The new wording lost something the old one had: both
					// stay, so "is 7 years old" survives "is autistic".
					b.S.AddMemory(store.Memory{Kind: "fact", Category: c.Category, Content: content, CreatedAt: now,
						SourceEvent: sourceEvent, Source: clip(userText, 300)})
				}
			}
			continue
		}
		if !dup {
			b.S.AddMemory(store.Memory{Kind: "fact", Category: c.Category, Content: content, CreatedAt: now,
				SourceEvent: sourceEvent, Source: clip(userText, 300)})
		}
	}
	// A person counts as named when their name is in the message or in the
	// lines just before it ("she" two messages after "Bethany").
	said := " " + norm(userText+" "+before) + " "
	for _, p := range out.People {
		// Only people the user actually named.
		if name := norm(p.Name); name != "" && strings.Contains(said, " "+name+" ") {
			// The relationship has to be in the user's words too. Asked
			// "who is Bethany?", a model will happily guess "sister".
			rel := p.Relationship
			if !stated(rel, " "+spellOutAges(norm(userText))+" ") {
				rel = ""
			}
			// A note may not smuggle in a relation the user never stated.
			note := p.Note
			theirs := " " + spellOutAges(norm(userText)) + " "
			for _, kin := range kinWords {
				if strings.Contains(" "+norm(note)+" ", " "+kin+" ") && !stated(kin, theirs) {
					note = ""
					break
				}
			}
			b.rememberPerson(p.Name, rel, note, now)
		}
	}
}

// passing reports wording that marks a fact as about this moment only,
// which has no place in long-term memory.
func passing(n string) bool {
	padded := " " + n + " "
	for _, w := range []string{" today ", " tonight ", " tomorrow ", " right now ", " said ", " asked ", " wants to know ", " needs to ", " has to ",
		// "Thomas's name is unknown" is the absence of a fact, usually made
		// from a question, and it would contradict what she does know.
		" is feeling ", " feels ", " currently ", " at the moment ", " last night ", " this morning ",
		// What someone is doing this minute must not overwrite what is
		// lasting: "smokes a lot" is not "is going out to smoke".
		" going out ", " is going to ", " about to ", " is heading ", " just now ",
		" unknown ", " not known ", " unclear ", " does not know ", " doesn t know ", " not sure ", " unsure ", " no information "} {
		if strings.Contains(padded, w) {
			return true
		}
	}
	return false
}

// detailsOf lists the specifics of a fact that are easy to lose when it is
// reworded: numbers, capitalised names and anything in parentheses, as
// normalised words. The user's own name is not a detail.
func detailsOf(userName, text string) []string {
	skip := norm(userName)
	out := []string{}
	add := func(s string) {
		for _, w := range strings.Fields(norm(s)) {
			if w != skip && (len(w) >= 2 || strings.ContainsAny(w, "0123456789")) {
				out = append(out, w)
			}
		}
	}
	for _, w := range strings.Fields(namesIn(text)) {
		add(w)
	}
	for _, w := range strings.Fields(norm(text)) {
		if strings.ContainsAny(w, "0123456789") {
			add(w)
		}
	}
	for rest := text; ; {
		i := strings.Index(rest, "(")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], ")")
		if j < 0 {
			break
		}
		add(rest[i+1 : i+j])
		rest = rest[i+j+1:]
	}
	return out
}

// dropsDetail reports whether rewording an old fact as a new one loses a
// detail the old one had. A value swapped for another of the same kind
// ("works at Acme" to "works at Globex") is a real update: it drops one
// detail but brings one. Losing more than the new wording brings means the
// new one is a different, narrower fact, not a replacement.
func dropsDetail(userName, old, new string) bool {
	have := map[string]bool{}
	for _, w := range strings.Fields(norm(new)) {
		have[w] = true
	}
	was := map[string]bool{}
	for _, w := range strings.Fields(norm(old)) {
		was[w] = true
	}
	dropped, brought := 0, 0
	for _, d := range detailsOf(userName, old) {
		if !have[d] {
			dropped++
		}
	}
	for _, d := range detailsOf(userName, new) {
		if !was[d] {
			brought++
		}
	}
	return dropped > 0 && brought < dropped
}

// coveredBy reports whether every meaningful word of the new fact is
// already in the old one, so there is nothing in it worth keeping.
func coveredBy(userName, old, new string) bool {
	skip := strings.ToLower(userName)
	was := map[string]bool{}
	for _, t := range tokens(old) {
		was[t] = true
	}
	for _, t := range tokens(new) {
		if t != skip && !was[t] {
			return false
		}
	}
	return true
}

// recent reports whether the user is telling of something that has just
// happened.
func recent(said string) bool {
	padded := " " + norm(said) + " "
	for _, w := range recencyWords {
		if strings.Contains(padded, w) {
			return true
		}
	}
	return false
}

var recencyWords = []string{" yesterday ", " today ", " this week ", " last week ", " this morning ", " last night ", " just ", " recently ", " the other day ", " this month ", " at the weekend "}

// knownAbout gathers what she already holds about the people named in a
// new fact. "Link lost a tooth" may be saved as "Thomas's son Link ..."
// without the word son being said again: she was told once who he is.
func (b *Brain) knownAbout(fact string, facts []store.Memory) string {
	var sb strings.Builder
	for _, name := range strings.Fields(namesIn(fact)) {
		if p, ok := b.S.FindPerson(strings.ToLower(name)); ok {
			sb.WriteString(" " + p.Relationship + " ")
		}
		for _, m := range facts {
			if strings.Contains(" "+norm(m.Content)+" ", " "+norm(name)+" ") {
				sb.WriteString(" " + m.Content + " ")
			}
		}
	}
	return sb.String()
}

// spellOutAges separates a number glued to its unit ("7month old").
func spellOutAges(n string) string {
	var sb strings.Builder
	r := []rune(n)
	for i, c := range r {
		sb.WriteRune(c)
		if c >= '0' && c <= '9' && i+1 < len(r) && r[i+1] >= 'a' && r[i+1] <= 'z' {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// namesIn returns the capitalised words of a text: the names a pronoun in
// the next message may stand for.
func namesIn(text string) string {
	out := []string{}
	for _, w := range strings.Fields(text) {
		w = strings.Trim(w, ".,!?:;\"'()")
		if len(w) >= 3 && w[0] >= 'A' && w[0] <= 'Z' {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// kinWords are the words that tie a person to the user. A model asked
// "who are Link and Atreus?" will file "Thomas has a brother called Link":
// both names are in the message, the relation is invented.
var kinWords = []string{"brother", "sister", "wife", "husband", "spouse", "partner", "girlfriend", "boyfriend", "fiance",
	"son", "daughter", "child", "baby", "kid", "father", "mother", "dad", "mum", "mom", "parent",
	"cousin", "uncle", "aunt", "nephew", "niece", "grandfather", "grandmother",
	"friend", "boss", "manager", "colleague", "coworker", "founder", "cofounder", "employee", "client",
	"dog", "cat", "pet", "puppy", "kitten", "horse"}

// grounded reports whether a fact can be traced to what the user said: it
// has to share at least two meaningful words with their message (one for
// a very short fact), any number in it must be theirs, and any word that
// ties a person to them must be one they used.
// said is the user's message; names are extra names from the lines before;
// prior is the memory being updated, whose relations are already accepted.
func grounded(userName, said, names, prior, fact string) bool {
	skip := strings.ToLower(userName)
	have := map[string]bool{}
	for _, t := range tokens(said + " " + names) {
		have[t] = true
	}
	shared, total := 0, 0
	for _, t := range tokens(fact) {
		if t == skip {
			continue
		}
		total++
		if have[t] {
			shared++
		}
	}
	if len(unsupportedNumbers(fact, said)) > 0 {
		return false
	}
	theirs := " " + spellOutAges(norm(said)) + " " + norm(prior) + " "
	padded := " " + norm(fact) + " "
	for _, kin := range kinWords {
		if strings.Contains(padded, " "+kin+" ") && !stated(kin, theirs) {
			return false
		}
	}
	return shared >= 2 || (total <= 3 && shared >= 1)
}

// stated reports whether a relationship is in the user's words. A child
// is often introduced without the word ("our seven month old", "my boy").
func stated(relationship, said string) bool {
	r := norm(relationship)
	if r == "" {
		return false
	}
	if strings.Contains(said, " "+r+" ") {
		return true
	}
	for _, kin := range []string{"son", "daughter", "child", "baby", "kid"} {
		if r != kin {
			continue
		}
		for _, cue := range []string{" son ", " daughter ", " boy ", " girl ", " kid ", " child ", " baby ", " year old ", " month old ", " months old ", " years old "} {
			if strings.Contains(said, cue) {
				return true
			}
		}
	}
	return false
}

// sharesSubject reports whether two facts have a meaningful word in
// common, leaving the user's own name aside.
func sharesSubject(userName, a, b string) bool {
	skip := strings.ToLower(userName)
	words := map[string]bool{}
	for _, t := range tokens(a) {
		if t != skip {
			words[t] = true
		}
	}
	for _, t := range tokens(b) {
		if words[t] {
			return true
		}
	}
	return false
}

// closenessFor turns a relationship word into how much she should care.
func closenessFor(relationship string) float64 {
	r := strings.ToLower(relationship)
	for _, w := range []string{"partner", "wife", "husband", "spouse", "fiance", "girlfriend", "boyfriend", "mother", "father", "mum", "mom", "dad", "son", "daughter", "sister", "brother", "family", "parent", "child"} {
		if strings.Contains(r, w) {
			return 0.9
		}
	}
	if strings.Contains(r, "friend") {
		return 0.7
	}
	for _, w := range []string{"colleague", "coworker", "manager", "boss", "team"} {
		if strings.Contains(r, w) {
			return 0.4
		}
	}
	return 0.3
}

// placeholder reports a relationship that says nothing ("unknown").
func placeholder(relationship string) bool {
	switch norm(relationship) {
	case "", "unknown", "not known", "unclear", "none", "n a", "person", "someone", "acquaintance", "mentioned":
		return true
	}
	return false
}

// findKnown looks a person up by name, also matching a first name against
// a full one ("Bethany" and "Bethany Bonnici" are the same person).
func (b *Brain) findKnown(name string) (store.Person, bool) {
	if p, ok := b.S.FindPerson(name); ok {
		return p, true
	}
	first, rest, _ := strings.Cut(strings.ToLower(strings.TrimSpace(name)), " ")
	people, _ := b.S.People()
	for _, p := range people {
		pFirst, pRest, _ := strings.Cut(strings.ToLower(p.Name), " ")
		// Same first name, and at most one of the two has a surname.
		if pFirst == first && len(first) >= 3 && (rest == "" || pRest == "") {
			return p, true
		}
	}
	return store.Person{}, false
}

// rememberPerson records someone the user named, or adds to what is known
// about them. A relationship is replaced when the old one said nothing, or
// when the new one is closer ("my wife" after "someone I mentioned").
func (b *Brain) rememberPerson(name, relationship, note string, now time.Time) {
	name = strings.TrimSpace(name)
	cfg := b.Cfg.Get()
	// Someone the user asked her to forget is not taken up again.
	if name == "" || strings.EqualFold(name, cfg.UserName) || strings.EqualFold(name, cfg.CompanionName) || b.S.Forgotten(name) {
		return
	}
	relationship = strings.TrimSpace(relationship)
	if placeholder(relationship) {
		relationship = ""
	}
	note = strings.TrimSpace(note)
	// "Mentioned by Thomas." tells her nothing she could use later.
	if n := " " + norm(note) + " "; strings.HasPrefix(n, " mentioned by") || strings.Contains(n, " this message ") || strings.Contains(n, " no further ") || strings.Contains(n, " not provided ") || strings.Contains(n, " no details ") {
		note = "" // the model describing its own task, not the person
	}
	// Software is not a person: "Claude, LLM model" is a tool he mentioned.
	// The note only counts when no relationship was given: a friend whose
	// note says he works in AI is still a friend.
	what := relationship
	if placeholder(relationship) {
		what = note
	}
	for _, w := range strings.Fields(norm(what)) {
		switch w {
		case "model", "llm", "ai", "assistant", "bot", "software", "app", "tool", "ide", "program":
			return
		}
	}
	// A pet name for someone already known is one more name for them, not
	// another person ("Boo" for Bethany).
	if owner, ok := b.nicknameFor(name, relationship, note); ok {
		b.addAlias(owner, name, now)
		return
	}
	if p, ok := b.findKnown(name); ok {
		if note != "" && !strings.Contains(norm(p.Notes), norm(note)) {
			p.Notes = strings.TrimSpace(p.Notes + "\n" + note)
		}
		// What the user set by hand stands over what she picks up.
		if relationship != "" && !p.Has("relationship") && (placeholder(p.Relationship) || closenessFor(relationship) > p.Closeness) {
			p.Relationship = relationship
			if !p.Has("closeness") {
				p.Closeness = closenessFor(relationship)
			}
		}
		// Learning the full name keeps the short one as another name.
		if !p.Has("name") && len(name) > len(p.Name) && strings.HasPrefix(strings.ToLower(name), strings.ToLower(p.Name)+" ") {
			p.Aliases = append(p.Aliases, p.Name)
			p.Name = name
		}
		p.LastMentioned = now
		b.S.SavePerson(p, now)
		return
	}
	b.S.SavePerson(store.Person{
		Name: name, Relationship: relationship, Closeness: closenessFor(relationship),
		Notes: note, LastMentioned: now,
	}, now)
}

// nicknameFor reports whom a note or relationship says a name is a pet
// name for: "nickname for Bethany Bonnici", "Bethany's nickname", "short
// for Bethany". Only someone she already knows counts.
func (b *Brain) nicknameFor(name, relationship, note string) (store.Person, bool) {
	words := strings.Fields(norm(relationship + " " + note))
	for i := range words {
		rest := words[i:]
		var after []string
		switch {
		case len(rest) >= 2 && (rest[0] == "nickname" || rest[0] == "short") && (rest[1] == "for" || rest[1] == "of"):
			after = rest[2:]
		case len(rest) >= 3 && rest[0] == "pet" && rest[1] == "name" && (rest[2] == "for" || rest[2] == "of"):
			after = rest[3:]
		case len(rest) >= 2 && rest[0] == "s" && rest[1] == "nickname" || len(rest) >= 3 && rest[0] == "s" && rest[1] == "pet" && rest[2] == "name":
			// "Bethany's nickname": the name comes before the marker.
			for n := min(i, 2); n >= 1; n-- {
				if p, ok := b.findKnown(strings.Join(words[i-n:i], " ")); ok && norm(p.Name) != norm(name) {
					return p, true
				}
			}
			continue
		default:
			continue
		}
		for n := min(len(after), 3); n >= 1; n-- {
			if p, ok := b.findKnown(strings.Join(after[:n], " ")); ok && norm(p.Name) != norm(name) {
				return p, true
			}
		}
	}
	return store.Person{}, false
}

// addAlias gives a person one more name they go by, unless it is their
// name already or they have it.
func (b *Brain) addAlias(p store.Person, alias string, now time.Time) {
	alias = strings.TrimSpace(alias)
	if alias == "" || strings.EqualFold(alias, p.Name) {
		return
	}
	for _, a := range p.Aliases {
		if strings.EqualFold(a, alias) {
			return
		}
	}
	p.Aliases = append(p.Aliases, alias)
	p.LastMentioned = now
	b.S.SavePerson(p, now)
}
