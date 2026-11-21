package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cosmos/core/internal/store"
)

// Weather is one look at a place: now, today, and the next hours.
type Weather struct {
	Place         string  `json:"place"`
	TempC         float64 `json:"temp_c"`
	WindKph       float64 `json:"wind_kph"`
	RainMm        float64 `json:"rain_mm"`
	Code          int     `json:"-"`
	Sky           string  `json:"sky"`
	TodayMinC     float64 `json:"today_min_c"`
	TodayMaxC     float64 `json:"today_max_c"`
	RainChancePct int     `json:"rain_chance_pct"`
	Hourly        []Hour  `json:"-"`
	Tomorrow      *Day    `json:"tomorrow,omitempty"`
}

// Day is the forecast for one whole day.
type Day struct {
	MinC          float64 `json:"min_c"`
	MaxC          float64 `json:"max_c"`
	Sky           string  `json:"sky"`
	RainChancePct int     `json:"rain_chance_pct"`
	WindMaxKph    float64 `json:"wind_max_kph"`
}

type Hour struct {
	Time          string
	RainChancePct int
	RainMm        float64
	Code          int
}

type WeatherSource interface {
	Fetch(ctx context.Context, place string) (Weather, error)
}

// WMO weather codes, as Open-Meteo returns them.
var wmo = map[int]string{
	0: "clear sky", 1: "mainly clear", 2: "partly cloudy", 3: "overcast",
	45: "fog", 48: "rime fog",
	51: "light drizzle", 53: "drizzle", 55: "heavy drizzle",
	56: "freezing drizzle", 57: "heavy freezing drizzle",
	61: "light rain", 63: "rain", 65: "heavy rain",
	66: "freezing rain", 67: "heavy freezing rain",
	71: "light snow", 73: "snow", 75: "heavy snow", 77: "snow grains",
	80: "light rain showers", 81: "rain showers", 82: "violent rain showers",
	85: "snow showers", 86: "heavy snow showers",
	95: "thunderstorm", 96: "thunderstorm with hail", 99: "severe thunderstorm",
}

var (
	dangerousCodes = map[int]bool{65: true, 66: true, 67: true, 75: true, 82: true, 86: true, 95: true, 96: true, 99: true}
	rainyCodes     = map[int]bool{51: true, 53: true, 55: true, 61: true, 63: true, 80: true, 81: true}
)

func describeCode(code int) string {
	if s, ok := wmo[code]; ok {
		return s
	}
	return "unknown"
}

func isDangerous(code int) bool { return dangerousCodes[code] }
func isRainy(code int) bool     { return rainyCodes[code] || dangerousCodes[code] }

// OpenMeteo fetches weather from open-meteo.com: free, no key, no account.
type OpenMeteo struct{ Client *http.Client }

func (o *OpenMeteo) get(ctx context.Context, u string, into any) error {
	c := o.Client
	if c == nil {
		c = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("weather service returned %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(into)
}

func (o *OpenMeteo) Fetch(ctx context.Context, place string) (Weather, error) {
	place = strings.TrimSpace(place)
	if place == "" {
		return Weather{}, fmt.Errorf("no place given")
	}
	var geo struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Name      string  `json:"name"`
			Country   string  `json:"country"`
		} `json:"results"`
	}
	q := url.Values{"name": {place}, "count": {"1"}, "language": {"en"}, "format": {"json"}}
	if err := o.get(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode(), &geo); err != nil {
		return Weather{}, fmt.Errorf("looking up %q failed: %w", place, err)
	}
	if len(geo.Results) == 0 {
		return Weather{}, fmt.Errorf("could not find a place called %q", place)
	}
	hit := geo.Results[0]
	name := hit.Name
	if hit.Country != "" {
		name += ", " + hit.Country
	}

	var f struct {
		Current struct {
			Temp   float64 `json:"temperature_2m"`
			Code   int     `json:"weather_code"`
			Precip float64 `json:"precipitation"`
			Wind   float64 `json:"wind_speed_10m"`
		} `json:"current"`
		Daily struct {
			Max  []float64  `json:"temperature_2m_max"`
			Min  []float64  `json:"temperature_2m_min"`
			Prob []*float64 `json:"precipitation_probability_max"`
			Code []int      `json:"weather_code"`
			Wind []float64  `json:"wind_speed_10m_max"`
		} `json:"daily"`
		Hourly struct {
			Time   []string   `json:"time"`
			Precip []float64  `json:"precipitation"`
			Prob   []*float64 `json:"precipitation_probability"`
			Code   []int      `json:"weather_code"`
		} `json:"hourly"`
	}
	fq := url.Values{
		"latitude":       {fmt.Sprint(hit.Latitude)},
		"longitude":      {fmt.Sprint(hit.Longitude)},
		"current":        {"temperature_2m,weather_code,precipitation,wind_speed_10m"},
		"daily":          {"temperature_2m_max,temperature_2m_min,precipitation_probability_max,weather_code,wind_speed_10m_max"},
		"hourly":         {"precipitation,precipitation_probability,weather_code"},
		"forecast_hours": {"12"},
		"forecast_days":  {"2"},
		"timezone":       {"auto"},
	}
	if err := o.get(ctx, "https://api.open-meteo.com/v1/forecast?"+fq.Encode(), &f); err != nil {
		return Weather{}, fmt.Errorf("forecast for %s failed: %w", name, err)
	}
	w := Weather{
		Place: name, TempC: f.Current.Temp, WindKph: f.Current.Wind, RainMm: f.Current.Precip,
		Code: f.Current.Code, Sky: describeCode(f.Current.Code),
	}
	if len(f.Daily.Max) > 0 && len(f.Daily.Min) > 0 {
		w.TodayMaxC, w.TodayMinC = f.Daily.Max[0], f.Daily.Min[0]
	}
	if len(f.Daily.Prob) > 0 && f.Daily.Prob[0] != nil {
		w.RainChancePct = int(*f.Daily.Prob[0])
	}
	if len(f.Daily.Max) > 1 && len(f.Daily.Min) > 1 {
		d := Day{MaxC: f.Daily.Max[1], MinC: f.Daily.Min[1]}
		if len(f.Daily.Code) > 1 {
			d.Sky = describeCode(f.Daily.Code[1])
		}
		if len(f.Daily.Prob) > 1 && f.Daily.Prob[1] != nil {
			d.RainChancePct = int(*f.Daily.Prob[1])
		}
		if len(f.Daily.Wind) > 1 {
			d.WindMaxKph = f.Daily.Wind[1]
		}
		w.Tomorrow = &d
	}
	for i, t := range f.Hourly.Time {
		h := Hour{Time: t}
		if i < len(f.Hourly.Precip) {
			h.RainMm = f.Hourly.Precip[i]
		}
		if i < len(f.Hourly.Prob) && f.Hourly.Prob[i] != nil {
			h.RainChancePct = int(*f.Hourly.Prob[i])
		}
		if i < len(f.Hourly.Code) {
			h.Code = f.Hourly.Code[i]
		}
		w.Hourly = append(w.Hourly, h)
	}
	return w, nil
}

// ---------- comparing today with her own log ----------

type Flag struct {
	What   string `json:"what"`
	Level  string `json:"level"` // note, warn
	Detail string `json:"detail"`
}

// Comparison says how today differs from what she has seen recently. The
// arithmetic happens here so no model has to do sums.
type Comparison struct {
	DaysOfHistory int     `json:"days_of_history"`
	UsualTempC    float64 `json:"usual_temp_c"`
	UsualWindKph  float64 `json:"usual_wind_kph"`
	TempDeltaC    float64 `json:"temp_delta_c"`
	Flags         []Flag  `json:"flags"`
}

func round(v float64) float64 { return math.Round(v) }

// compare needs readings from at least two earlier days to call anything
// usual. It returns nil when there is not enough history yet.
func compare(w Weather, history []store.Reading) *Comparison {
	days := map[string]bool{}
	var temp, wind float64
	for _, r := range history {
		days[dayKey(r.At)] = true
		temp += r.TempC
		wind += r.WindKph
	}
	if len(days) < 2 || len(history) < 3 {
		return nil
	}
	n := float64(len(history))
	c := &Comparison{
		DaysOfHistory: len(days),
		UsualTempC:    round(temp / n),
		UsualWindKph:  round(wind / n),
		Flags:         []Flag{},
	}
	c.TempDeltaC = round(w.TempC) - c.UsualTempC
	if math.Abs(c.TempDeltaC) >= 4 {
		dir := "warmer"
		if c.TempDeltaC < 0 {
			dir = "colder"
		}
		c.Flags = append(c.Flags, Flag{"temperature", "note",
			fmt.Sprintf("%.0f degrees %s than the usual %.0f", math.Abs(c.TempDeltaC), dir, c.UsualTempC)})
	}
	if w.WindKph >= 25 && c.UsualWindKph > 0 && w.WindKph >= 2*c.UsualWindKph {
		c.Flags = append(c.Flags, Flag{"wind", "warn",
			fmt.Sprintf("wind %.0f km/h against a usual %.0f", round(w.WindKph), c.UsualWindKph)})
	}
	if isDangerous(w.Code) {
		c.Flags = append(c.Flags, Flag{"conditions", "warn", w.Sky})
	}
	return c
}

// lookAtWeather fetches the weather, records the reading in her log (at
// most one an hour per place) and compares it with the previous week.
func (b *Brain) lookAtWeather(ctx context.Context, place string) (Weather, *Comparison, error) {
	if strings.TrimSpace(place) == "" {
		place = b.Cfg.Get().Location
	}
	if strings.TrimSpace(place) == "" {
		return Weather{}, nil, fmt.Errorf("no place given and no home location set")
	}
	w, err := b.Weather.Fetch(ctx, place)
	if err != nil {
		return Weather{}, nil, err
	}
	now := b.Now()
	history, _ := b.S.ReadingsBetween(w.Place, dayStart(now).AddDate(0, 0, -7), dayStart(now))
	recent, _ := b.S.ReadingsBetween(w.Place, now.Add(-time.Hour), now.Add(time.Second))
	if len(recent) == 0 {
		b.S.AddReading(store.Reading{Place: w.Place, At: now, TempC: w.TempC, WindKph: w.WindKph, RainMm: w.RainMm, Code: w.Code})
	}
	return w, compare(w, history), nil
}

// weatherFacts is what a model is shown: whole numbers only, so a reply
// can be checked against them.
func weatherFacts(w Weather, c *Comparison) map[string]any {
	facts := map[string]any{
		"place": w.Place, "sky": w.Sky,
		"temp_c": round(w.TempC), "wind_kph": round(w.WindKph), "rain_mm": w.RainMm,
		"today_min_c": round(w.TodayMinC), "today_max_c": round(w.TodayMaxC),
		"rain_chance_pct": w.RainChancePct,
	}
	// Labelled as tomorrow's, so a question about tomorrow is not
	// answered with today's numbers.
	if d := w.Tomorrow; d != nil {
		facts["tomorrow"] = map[string]any{"sky": d.Sky, "min_c": round(d.MinC), "max_c": round(d.MaxC),
			"rain_chance_pct": d.RainChancePct, "wind_max_kph": round(d.WindMaxKph)}
	}
	// With no history yet there is nothing to compare, and saying so only
	// tempts the writer to read it out.
	if c != nil {
		facts["compared_to_usual"] = c
	}
	return facts
}

// ---------- watching the weather on her own ----------

const (
	sevCalm   = "calm"
	sevWatch  = "watch"
	sevActive = "active"
)

var sevRank = map[string]int{sevCalm: 0, sevWatch: 1, sevActive: 2}

var watchCadence = map[string]time.Duration{
	sevCalm: 3 * time.Hour, sevWatch: time.Hour, sevActive: 20 * time.Minute,
}

func classifySeverity(w Weather) string {
	rainingNow := isRainy(w.Code) || w.RainMm > 0.1
	dangerSoon, rainSoon := false, false
	for i, h := range w.Hourly {
		if i < 2 && isDangerous(h.Code) {
			dangerSoon = true
		}
		if i < 6 && (isRainy(h.Code) || h.RainChancePct >= 50) {
			rainSoon = true
		}
	}
	switch {
	case isDangerous(w.Code) || dangerSoon || rainingNow:
		return sevActive
	case rainSoon:
		return sevWatch
	}
	return sevCalm
}

type watchState struct {
	Severity     string    `json:"severity"`
	LastReportAt time.Time `json:"last_report_at"`
	NextCheckAt  time.Time `json:"next_check_at"`
}

// decideReport says whether a change in the weather is worth telling the
// user about, and why. Empty means stay quiet.
func decideReport(prev watchState, severity string, now time.Time) string {
	if prev.Severity == "" {
		if severity != sevCalm {
			return "The weather is not calm right now."
		}
		return ""
	}
	switch {
	case sevRank[severity] > sevRank[prev.Severity] && severity == sevActive:
		return "The weather is turning rough."
	case sevRank[severity] > sevRank[prev.Severity]:
		return "Rain is becoming likely soon."
	case severity == sevCalm && prev.Severity != sevCalm:
		return "The weather has settled back down."
	case severity == sevActive && now.Sub(prev.LastReportAt) >= time.Hour:
		return "The rough weather is still going."
	}
	return ""
}
