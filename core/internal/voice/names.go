package voice

import (
	"strings"
	"unicode"
)

// Being spoken to. When she is always listening she answers only when
// her name is said, and a speech recogniser rarely spells a name the
// same way twice: "Clarke" comes back as "Clark", "Clerk" or "Klark".
// So a name is matched by how it sounds, not how it is written.

// Addressed says whether her name, or something that sounds like it, is
// in what was heard. also holds other spellings the user has added.
//
// Her name itself always counts. A word that only sounds like it counts
// where it is said the way a name is called, not where it is a word in
// a sentence: "Clark, are you there?" is for her; "the clerk at the
// store", "Claire and I" and "Clark Kent" are not.
func Addressed(heard, name string, also []string) bool {
	names := append([]string{name}, also...)
	words := tokens(heard)
	for i, w := range words {
		// A name heard as two words: "no va" for "Nova".
		pair := ""
		if i+1 < len(words) {
			pair = w.lower + words[i+1].lower
		}
		for _, n := range names {
			for _, part := range wordsIn(n) {
				if w.lower == part {
					return true
				}
				if soundsLike(w.lower, part) && calling(words, i, i) {
					return true
				}
				if pair != "" && soundsLike(pair, part) && calling(words, i, i+1) {
					return true
				}
			}
		}
	}
	return false
}

type token struct {
	lower string
	// Capital: written with a capital letter.
	capital bool
	// Comma: a comma or other mark came straight after it.
	comma bool
}

func tokens(s string) []token {
	var out []token
	var cur []rune
	flush := func(mark bool) {
		if len(cur) > 0 {
			out = append(out, token{lower: strings.ToLower(string(cur)), capital: unicode.IsUpper(cur[0])})
			cur = cur[:0]
		}
		if mark && len(out) > 0 {
			out[len(out)-1].comma = true
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			cur = append(cur, r)
		case unicode.IsSpace(r):
			flush(false)
		default:
			flush(true)
		}
	}
	flush(false)
	return out
}

// Words that, just before a name-like word, make it something talked
// about: "the clerk", "with Claire", "to Clark".
var talkedAbout = map[string]bool{
	"the": true, "a": true, "an": true, "my": true, "your": true, "his": true, "her": true,
	"our": true, "their": true, "to": true, "with": true, "and": true, "or": true, "at": true, "of": true, "for": true,
	"from": true, "by": true, "about": true, "like": true,
}

// calling says whether the words from..to are placed as a name being
// called: not after "the" or "with", not before "and" ("Claire and I"),
// and not before a surname ("Clark Kent").
func calling(words []token, from, to int) bool {
	last := to+1 == len(words) || words[to].comma
	if from > 0 && !words[from-1].comma {
		before := words[from-1].lower
		if talkedAbout[before] {
			return false
		}
		// "that clerk was rude", but "what do you think of that, Clark?"
		// often comes back without its comma.
		if (before == "this" || before == "that") && !last {
			return false
		}
	}
	if to+1 < len(words) && !words[to].comma {
		next := words[to+1]
		if next.lower == "and" || next.lower == "or" || next.lower == "s" {
			return false
		}
		if next.capital && next.lower != "i" {
			return false
		}
	}
	return true
}

// Ending says whether what was heard closes the conversation: thanking
// her or saying that is all.
func Ending(heard string) bool {
	h := " " + strings.Join(wordsIn(heard), " ") + " "
	for _, end := range []string{" thanks ", " thank you ", " that s all ", " that is all ", " that will be all ", " bye ", " goodbye ", " never mind ", " stop listening "} {
		if strings.Contains(h, end) {
			return true
		}
	}
	return false
}

func wordsIn(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
}

// soundsLike: the same word, the same sound, or a sound one step off
// when the spelling is close as well. The spelling check keeps "click"
// from passing for "Clarke".
func soundsLike(word, name string) bool {
	if word == name {
		return true
	}
	if len(word) < 3 || len(name) < 3 {
		return false
	}
	a, b := sound(word), sound(name)
	if a == b {
		return true
	}
	return len(b) >= 3 && distance(a, b) <= 1 && distance(word, name) <= 2
}

// sound is a rough spelling of how a word is said: letters that sound
// alike are made the same, vowels after the first letter and doubled
// letters are dropped. "Clarke", "Clark", "Klark" and "Clerk" all come
// out as "klrk".
func sound(w string) string {
	r := strings.NewReplacer("ph", "f", "ck", "k", "gh", "g", "wh", "w", "sch", "sk", "qu", "kw", "x", "ks", "z", "s", "q", "k").Replace(w)
	var out []byte
	for i := 0; i < len(r); i++ {
		c := r[i]
		if c == 'c' {
			if i+1 < len(r) && strings.IndexByte("eiy", r[i+1]) >= 0 {
				c = 's'
			} else {
				c = 'k'
			}
		}
		if i > 0 && strings.IndexByte("aeiouyhw", c) >= 0 {
			continue
		}
		if strings.IndexByte("aeiouy", c) >= 0 {
			c = 'a'
		}
		if len(out) > 0 && out[len(out)-1] == c {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// distance is the number of letters to add, drop or change to turn a
// into b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
