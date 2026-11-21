package brain

import (
	"math"
	"strings"
	"time"
)

// Mood is three slow numbers, each 0..1. Events push them; time pulls them
// back to her baseline. Feeling is the name of the last strong emotion.
type Mood struct {
	Warmth     float64   `json:"warmth"`
	Energy     float64   `json:"energy"`
	Confidence float64   `json:"confidence"`
	Feeling    string    `json:"feeling"`
	FeelingAt  time.Time `json:"feeling_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var baseline = Mood{Warmth: 0.6, Energy: 0.5, Confidence: 0.6}

const (
	moodHalfLife = 6 * time.Hour
	feelingLasts = 3 * time.Hour
	// How much of the remaining room one fully intense event takes.
	moodReach = 0.35
	// How much of the distance from baseline survives each event.
	moodKeep = 0.9
)

// Appraisal is how one event landed, as judged by the notice role.
type Appraisal struct {
	Emotion    string  `json:"emotion"`
	Intensity  float64 `json:"intensity"`
	Touches    string  `json:"touches"`
	Warmth     float64 `json:"warmth"`
	Energy     float64 `json:"energy"`
	Confidence float64 `json:"confidence"`
}

// emotionPush is the direction each feeling moves her warmth, energy and
// confidence. How far is the feeling's intensity.
var emotionPush = map[string][3]float64{
	"calm":       {0, 0, 0},
	"glad":       {0.5, 0.4, 0.3},
	"warmth":     {0.8, 0.2, 0.3},
	"tenderness": {0.8, -0.1, 0.1},
	"amusement":  {0.4, 0.6, 0.2},
	"curiosity":  {0.2, 0.6, 0.1},
	"hope":       {0.3, 0.5, 0.4},
	"pride":      {0.5, 0.4, 0.7},
	"concern":    {0.3, -0.3, -0.2},
	"worry":      {0.1, -0.4, -0.5},
	"sadness":    {0.1, -0.7, -0.3},
	"hurt":       {-0.7, -0.4, -0.7},
	"unease":     {-0.1, -0.2, -0.8},
	"anger":      {-0.5, 0.6, 0.1},
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// decayed moves each number toward baseline by the time passed.
func decayed(m Mood, now time.Time) Mood {
	if m.UpdatedAt.IsZero() || !now.After(m.UpdatedAt) {
		return m
	}
	k := math.Pow(0.5, float64(now.Sub(m.UpdatedAt))/float64(moodHalfLife))
	m.Warmth = baseline.Warmth + (m.Warmth-baseline.Warmth)*k
	m.Energy = baseline.Energy + (m.Energy-baseline.Energy)*k
	m.Confidence = baseline.Confidence + (m.Confidence-baseline.Confidence)*k
	m.UpdatedAt = now
	return m
}

// nudge moves one number by a push of -1..1. The push is scaled by the
// room left in that direction, so the number approaches 0 or 1 without
// reaching it, and each event that moves her first gives back a little of
// the distance from baseline. A long friendly conversation then settles
// at a high mood instead of pinning every number at the top. What she
// gives back grows with the push: a message that leaves her unmoved
// ("add milk to the list") leaves her mood where it was, and only time
// brings it home.
func nudge(value, base, push float64) float64 {
	give := (1 - moodKeep) * math.Min(1, 3*math.Abs(push))
	value = base + (value-base)*(1-give)
	if push > 0 {
		return value + push*moodReach*(1-value)
	}
	return value + push*moodReach*value
}

// applied returns the mood after an appraisal.
func applied(m Mood, a Appraisal, now time.Time) Mood {
	m = decayed(m, now)
	in := clamp(a.Intensity, 0, 1)
	m.Warmth = clamp(nudge(m.Warmth, baseline.Warmth, clamp(a.Warmth, -1, 1)*in), 0, 1)
	m.Energy = clamp(nudge(m.Energy, baseline.Energy, clamp(a.Energy, -1, 1)*in), 0, 1)
	m.Confidence = clamp(nudge(m.Confidence, baseline.Confidence, clamp(a.Confidence, -1, 1)*in), 0, 1)
	if in >= 0.2 && strings.TrimSpace(a.Emotion) != "" {
		m.Feeling = strings.ToLower(strings.TrimSpace(a.Emotion))
		m.FeelingAt = now
	}
	m.UpdatedAt = now
	return m
}

// Describe gives a few words for the mood: the recent feeling if there is
// one, otherwise a reading of the numbers.
func (m Mood) Describe(now time.Time) string {
	if m.Feeling != "" && now.Sub(m.FeelingAt) < feelingLasts {
		return m.Feeling
	}
	switch {
	case m.Energy < 0.3:
		return "quiet"
	case m.Warmth < 0.35:
		return "distant"
	case m.Confidence < 0.35:
		return "unsure"
	case m.Warmth > 0.75 && m.Energy > 0.6:
		return "bright"
	case m.Warmth > 0.75:
		return "warm"
	default:
		return "calm"
	}
}

// Mood returns her current mood, decayed to now.
func (b *Brain) Mood() Mood {
	m := baseline
	if !b.S.GetJSON("mood", &m) {
		m.UpdatedAt = b.Now()
	}
	return decayed(m, b.Now())
}

func (b *Brain) feel(a Appraisal) Mood {
	m := applied(b.Mood(), a, b.Now())
	b.S.SetJSON("mood", m)
	b.Bus.Publish("mood", m)
	return m
}
