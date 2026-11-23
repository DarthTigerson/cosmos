package brain

import (
	"context"
	"strings"
	"time"

	"cosmos/core/internal/config"
	"cosmos/core/internal/llm"
	"cosmos/core/internal/store"
)

// Nickname is a pet name the user uses, and who it is for. "Little one"
// said to her is her name, not his: without knowing whose it is she
// picks it up and says it back to him.
type Nickname struct {
	Name string `json:"name"`
	// For is "companion", "user", or the name of someone they know.
	For     string    `json:"for"`
	Heard   string    `json:"heard"` // the line it was heard in
	LearnAt time.Time `json:"learned_at"`
}

func (b *Brain) Nicknames() []Nickname {
	out := []Nickname{}
	b.S.GetJSON("nicknames", &out)
	return out
}

// learnNicknames goes over what the user said and picks out pet names.
// It runs while she sleeps: one pass over the day costs one model call,
// where spotting them message by message would cost one per message.
func (b *Brain) learnNicknames(ctx context.Context, cfg config.Config, events []store.Event) []Nickname {
	var said strings.Builder
	for _, e := range events {
		if e.Kind == "user_msg" {
			said.WriteString("- " + clip(e.Text, 200) + "\n")
		}
	}
	if said.Len() == 0 {
		return nil
	}
	people, _ := b.S.People()
	names := []string{}
	for _, p := range people {
		names = append(names, p.Name)
	}
	system := strings.Join([]string{
		"These are things " + cfg.UserName + " said today to " + cfg.CompanionName + ", their companion AI.",
		"Find pet names and nicknames " + cfg.UserName + " used: an affectionate name used in place of a real one (\"little one\", \"buddy\", \"Boo\").",
		"For each give the nickname exactly as written, the line it is in, and who it is for:",
		`- "companion": ` + cfg.UserName + ` is calling ` + cfg.CompanionName + ` by it. A pet name used while speaking to her is hers ("well done, little one").`,
		`- "person": it is their name for someone they know. Give that person's name in person, or "" if it is not clear who.`,
		`- "user": ` + cfg.UserName + ` says it is what they themselves are called or want to be called.`,
		"People they know: " + strings.Join(names, ", ") + ".",
		"Real names, ordinary words and job titles are not nicknames. Most days there are none: an empty list is the usual answer.",
	}, "\n")
	var out struct {
		Nicknames []struct {
			Line     string `json:"line"`
			Nickname string `json:"nickname"`
			For      string `json:"for"`
			Person   string `json:"person"`
		} `json:"nicknames"`
	}
	if err := b.ask(ctx, llm.Request{
		Tier: "sleeper", Role: "nicknames", System: system,
		Messages: []llm.Message{{Role: "user", Content: clip(said.String(), 6000)}},
		Schema:   nicknameSchema, MaxTokens: 400,
	}, &out); err != nil {
		b.Log.Printf("nicknames: %v", err)
		return nil
	}
	known := b.Nicknames()
	heard := " " + norm(said.String()) + " "
	learned := []Nickname{}
	for _, n := range out.Nicknames {
		name := strings.TrimSpace(n.Nickname)
		key := norm(name)
		words := len(strings.Fields(key))
		// It has to be something they wrote, short, and not a real name.
		if key == "" || words > 3 || !strings.Contains(heard, " "+key+" ") || key == norm(cfg.UserName) || key == norm(cfg.CompanionName) {
			continue
		}
		who := n.For
		if who == "person" {
			// Whose it is may not be clear yet. It is kept anyway, so she
			// knows it is a name and can ask who it is for.
			who = "unknown"
			if p, ok := b.S.FindPerson(strings.ToLower(strings.TrimSpace(n.Person))); ok && norm(p.Name) != key {
				who = p.Name
			}
		}
		if who != "companion" && who != "user" && n.For != "person" {
			continue
		}
		if _, ok := b.S.FindPerson(key); ok {
			continue // that is somebody's actual name
		}
		dup := false
		for _, k := range known {
			// A name that is already hers, or his own, is not filed again
			// for someone else.
			dup = dup || (norm(k.Name) == key && (k.For == who || who == "unknown" || k.For == "companion" || k.For == "user"))
		}
		if dup {
			continue
		}
		nick := Nickname{Name: name, For: who, Heard: clip(n.Line, 160), LearnAt: b.Now()}
		known = append(known, nick)
		if p, ok := b.S.FindPerson(strings.ToLower(who)); ok {
			b.addAlias(p, name, b.Now())
		}
		learned = append(learned, nick)
	}
	if len(learned) > 0 {
		b.S.SetJSON("nicknames", known)
	}
	return learned
}

// nicknameNotes tells the writer whose each pet name is.
func (b *Brain) nicknameNotes(cfg config.Config) string {
	lines := []string{}
	for _, n := range b.Nicknames() {
		switch n.For {
		case "companion":
			lines = append(lines, "- \""+n.Name+"\" is "+cfg.UserName+"'s pet name for you. It is yours. Never call "+cfg.UserName+" that.")
		case "user":
			lines = append(lines, "- "+cfg.UserName+" goes by \""+n.Name+"\".")
		case "unknown":
			lines = append(lines, "- \""+n.Name+"\" is "+cfg.UserName+"'s pet name for someone. You do not know who yet. Do not guess: ask who they mean.")
		default:
			lines = append(lines, "- \""+n.Name+"\" is "+cfg.UserName+"'s name for "+n.For+". When they say it they mean "+n.For+". It is theirs to use, not yours.")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Pet names:\n" + strings.Join(lines, "\n")
}

// dropBorrowedNames takes out of a draft the pet name the user has for
// her: it is hers, and she must not call them by it.
func (b *Brain) dropBorrowedNames(draft string) string {
	for _, n := range b.Nicknames() {
		// Only the name that is hers. Their names for other people she
		// has to be able to say, to talk or ask about them.
		if n.For != "companion" {
			continue
		}
		kept := []string{}
		for _, line := range splitSentences(draft) {
			cut := line
			for {
				i := strings.Index(strings.ToLower(cut), strings.ToLower(n.Name))
				if i < 0 {
					break
				}
				before, after := strings.TrimRight(cut[:i], " "), cut[i+len(n.Name):]
				// "Good to see you, little one." loses the comma with the name.
				before = strings.TrimSuffix(before, ",")
				after = strings.TrimPrefix(after, ",")
				if before != "" && !strings.HasPrefix(after, ".") && !strings.HasPrefix(after, "!") && !strings.HasPrefix(after, "?") && !strings.HasPrefix(after, " ") {
					after = " " + after
				}
				cut = strings.TrimSpace(before + after)
			}
			// A sentence that was about the name ("I'll keep calling you
			// 'little one'") is left as `calling you " "` or "being
			// called my." without it, and goes as a whole.
			if cut != line && brokenByCut(cut) {
				continue
			}
			kept = append(kept, cut)
		}
		draft = strings.Join(kept, " ")
	}
	return draft
}

// brokenByCut reports a sentence left without sense once a name was taken
// out of it: empty quotes, or a word that needs the name after it.
func brokenByCut(s string) bool {
	if strings.Contains(s, `""`) || strings.Contains(s, `" "`) || strings.Contains(s, "“”") || strings.Contains(s, "“ ”") || strings.Contains(s, "''") {
		return true
	}
	words := strings.Fields(norm(s))
	if len(words) == 0 {
		return true
	}
	switch words[len(words)-1] {
	case "my", "your", "called", "calling", "call", "as":
		return true
	}
	return false
}
