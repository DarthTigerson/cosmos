package brain

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var linkPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"'` + "`" + `]+`)

// unsupportedLinks returns the web addresses in a draft that appear nowhere
// in the material the writer was given. An address counts as given with
// or without a closing slash.
func unsupportedLinks(draft string, given ...string) []string {
	all := strings.Join(given, "\n")
	bad := []string{}
	for _, l := range linkPattern.FindAllString(draft, -1) {
		// The full stop or bracket after an address belongs to the sentence.
		l = strings.TrimRight(l, ".,;:!?)]}*")
		if strings.Contains(all, l) || strings.Contains(all, strings.TrimSuffix(l, "/")) {
			continue
		}
		dup := false
		for _, b := range bad {
			dup = dup || b == l
		}
		if !dup {
			bad = append(bad, l)
		}
	}
	return bad
}

// numbersIn pulls every number out of a text: "14°", "38 km/h", "2.5".
func numbersIn(text string) []float64 {
	out := []float64{}
	r := []rune(text)
	for i := 0; i < len(r); {
		if r[i] < '0' || r[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(r) && r[j] >= '0' && r[j] <= '9' {
			j++
		}
		// One decimal part, with a point or a comma.
		if j+1 < len(r) && (r[j] == '.' || r[j] == ',') && r[j+1] >= '0' && r[j+1] <= '9' {
			k := j + 1
			for k < len(r) && r[k] >= '0' && r[k] <= '9' {
				k++
			}
			if v, err := strconv.ParseFloat(strings.Replace(string(r[i:k]), ",", ".", 1), 64); err == nil {
				out = append(out, v)
			}
			i = k
			continue
		}
		if v, err := strconv.ParseFloat(string(r[i:j]), 64); err == nil {
			out = append(out, v)
		}
		i = j
	}
	return out
}

// unsupportedNumbers returns the numbers in a draft that appear nowhere in
// the material the writer was given. A number also counts as supported when
// it is the rounding of a given one ("14" for 13.6).
func unsupportedNumbers(draft string, given ...string) []string {
	allowed := map[float64]bool{}
	for _, g := range given {
		for _, n := range numbersIn(g) {
			allowed[n] = true
			allowed[math.Round(n)] = true
			allowed[math.Abs(n)] = true
			// "1 pm" for 13:00.
			if n >= 13 && n <= 23 && n == math.Trunc(n) {
				allowed[n-12] = true
			}
		}
	}
	bad := []string{}
	seen := map[float64]bool{}
	for _, n := range numbersIn(draft) {
		if allowed[n] || seen[n] {
			continue
		}
		seen[n] = true
		bad = append(bad, strconv.FormatFloat(n, 'f', -1, 64))
	}
	return bad
}
