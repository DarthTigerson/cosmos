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
func Addressed(heard, name string, also []string) bool {
	names := append([]string{name}, also...)
	words := wordsIn(heard)
	for i := range words {
		// A name heard as two words: "no va" for "Nova".
		pair := ""
		if i+1 < len(words) {
			pair = words[i] + words[i+1]
		}
		for _, n := range names {
			for _, part := range wordsIn(n) {
				if soundsLike(words[i], part) || (pair != "" && soundsLike(pair, part)) {
					return true
				}
			}
		}
	}
	return false
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
