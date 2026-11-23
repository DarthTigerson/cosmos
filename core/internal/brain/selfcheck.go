package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cosmos/core/internal/store"
)

// Self-knowledge. Asked "are you okay?" or "your logs say you are uneasy,
// what is wrong?", she used to make something up. Now she can look at how
// she has actually been running: which of her models are up, which tools
// failed and why, how often her replies needed a rewrite, how slow her
// voice has been. Code builds the report from her own records; she never
// reads a raw log, which holds web pages and pasted text that could steer
// her, and would only crowd out a small model.

// ServerHealth is how one of her model servers is doing.
type ServerHealth struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// failure is one kind of thing that went wrong, and how often.
type failure struct {
	Step  string `json:"step"`
	Why   string `json:"why"`
	Times int    `json:"times"`
}

// failuresIn groups the failed steps of some replies by step and reason,
// most frequent first. The arguments a step was called with are left out:
// they hold the user's words, and the reason is what matters.
func failuresIn(runs []store.Run, limit int) []failure {
	by := map[string]*failure{}
	order := []string{}
	for _, r := range runs {
		for _, st := range r.Steps {
			if st.OK || st.Tier == "voice" {
				continue
			}
			why := st.Note
			if i := strings.Index(why, " (asked with "); i >= 0 {
				why = why[:i]
			}
			why = clip(strings.TrimSpace(why), 140)
			key := st.Name + "|" + why
			if by[key] == nil {
				by[key] = &failure{Step: st.Name, Why: why}
				order = append(order, key)
			}
			by[key].Times++
		}
	}
	out := []failure{}
	for _, k := range order {
		out = append(out, *by[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Times > out[j].Times })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// selfCheck is the self.check tool: a short account of how she has been
// running today.
func (b *Brain) selfCheck(ctx context.Context) (map[string]any, error) {
	now := b.Now()
	since := dayStart(now)
	report := map[string]any{"since": since.Format("15:04")}

	if b.Health != nil {
		report["models"] = b.Health(ctx)
	}

	runs, err := b.S.RunsSince(since)
	if err != nil {
		return nil, err
	}
	rewritten, unverified := 0, 0
	audio := []int64{}
	for _, r := range runs {
		if r.Rewrites > 0 {
			rewritten++
		}
		if !r.Verified {
			unverified++
		}
		for _, st := range r.Steps {
			if st.Name == "first audio" {
				audio = append(audio, st.Millis)
			}
		}
	}
	report["replies_today"] = len(runs)
	report["replies_rewritten"] = rewritten
	report["replies_sent_unchecked"] = unverified
	if f := failuresIn(runs, 6); len(f) > 0 {
		report["failed_steps"] = f
	}
	if len(audio) > 0 {
		sort.Slice(audio, func(i, j int) bool { return audio[i] < audio[j] })
		report["voice_seconds_to_first_word"] = fmt.Sprintf("%.1f (median of %d spoken replies)", float64(audio[len(audio)/2])/1000, len(audio))
	}

	events, _ := b.S.EventsBetween(since, now.Add(time.Second), "mistake", "appraisal")
	caught, told := 0, 0
	feelings := []string{}
	for _, e := range events {
		switch {
		case e.Kind == "mistake" && strings.HasPrefix(e.Text, "I got a reply wrong"):
			caught++
		case e.Kind == "mistake":
			told++
		case e.Kind == "appraisal":
			var m struct {
				Emotion string `json:"emotion"`
				Touches string `json:"touches"`
			}
			if json.Unmarshal(e.Meta, &m) == nil && m.Emotion != "" {
				feelings = append(feelings, e.At.Format("15:04")+" "+m.Emotion+": "+clip(m.Touches, 100))
			}
		}
	}
	report["drafts_my_checks_stopped"] = caught
	report["times_told_i_got_it_wrong"] = told
	if len(feelings) > 3 {
		feelings = feelings[len(feelings)-3:]
	}
	if len(feelings) > 0 {
		report["what_moved_my_mood_last"] = feelings
	}
	report["mood_now"] = b.Mood().Describe(now)

	// Work she does on her own that has stopped.
	stuck := []string{}
	if books, err := b.S.Books("failed"); err == nil {
		for _, bk := range books {
			stuck = append(stuck, "book \""+clip(bk.Title, 60)+"\": "+clip(bk.Error, 100))
		}
	}
	if eps, err := b.S.Episodes(0, 5, "failed"); err == nil {
		for _, e := range eps {
			stuck = append(stuck, "episode \""+clip(e.Title, 60)+"\": "+clip(e.Error, 100))
		}
	}
	if len(stuck) > 0 {
		report["stuck"] = stuck
	}
	return report, nil
}

// failureNotes describes what failed over some replies, for her sleep:
// the same failure night after night is a bug worth reporting.
func failureNotes(runs []store.Run) string {
	f := failuresIn(runs, 8)
	if len(f) == 0 {
		return "(nothing failed)\n"
	}
	var sb strings.Builder
	for _, x := range f {
		fmt.Fprintf(&sb, "- %s failed %d time(s): %s\n", x.Step, x.Times, x.Why)
	}
	return sb.String()
}
