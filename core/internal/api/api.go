// Package api is the local HTTP interface the Electron app talks to.
package api

import (
	"context"
	"cosmos/core/internal/mcp"
	"cosmos/core/internal/voice"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cosmos/core/internal/brain"
	"cosmos/core/internal/config"
	"cosmos/core/internal/launcher"
	"cosmos/core/internal/store"
)

type Server struct {
	B     *brain.Brain
	Token string
	// Launch and ModelDirs back the Models screen. Launch may be nil.
	Launch    *launcher.Launcher
	ModelDirs []string
	// Voice is her speaking voice. Nil, or not set up, falls back to the
	// computer's built-in one.
	Voice *voice.Manager
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Handler returns the routed, authenticated API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/state", s.auth(s.state))
	mux.HandleFunc("GET /api/today", s.auth(s.today))
	mux.HandleFunc("POST /api/message", s.auth(s.message))
	mux.HandleFunc("GET /api/sessions", s.auth(s.sessions))
	mux.HandleFunc("POST /api/sessions", s.auth(s.newSession))
	mux.HandleFunc("GET /api/sessions/{id}", s.auth(s.session))
	mux.HandleFunc("POST /api/sessions/{id}/open", s.auth(s.openSession))
	mux.HandleFunc("GET /api/runs/{id}", s.auth(s.run))
	mux.HandleFunc("GET /api/mind", s.auth(s.mind))
	mux.HandleFunc("GET /api/sleep", s.auth(s.sleepView))
	mux.HandleFunc("POST /api/sleep", s.auth(s.sleepNow))
	mux.HandleFunc("POST /api/proposals/{id}", s.auth(s.decide))
	mux.HandleFunc("POST /api/tidy/{id}/undo", s.auth(s.undoTidy))
	mux.HandleFunc("GET /api/memories", s.auth(s.memories))
	mux.HandleFunc("GET /api/people", s.auth(s.people))
	mux.HandleFunc("GET /api/people/{id}", s.auth(s.person))
	mux.HandleFunc("GET /api/calendar", s.auth(s.calendar))
	mux.HandleFunc("GET /api/podcasts", s.auth(s.podcasts))
	mux.HandleFunc("POST /api/podcasts/feeds", s.auth(s.addFeed))
	mux.HandleFunc("DELETE /api/podcasts/feeds/{id}", s.auth(s.removeFeed))
	mux.HandleFunc("GET /api/podcasts/feeds/{id}/art", s.auth(s.feedArt))
	mux.HandleFunc("GET /api/podcasts/feeds/{id}/episodes", s.auth(s.feedEpisodes))
	mux.HandleFunc("GET /api/podcasts/episodes/{id}", s.auth(s.episode))
	mux.HandleFunc("GET /api/podcasts/episodes/{id}/transcript", s.auth(s.episodeTranscript))
	mux.HandleFunc("POST /api/podcasts/episodes/{id}/listen", s.auth(s.listenToEpisode))
	mux.HandleFunc("POST /api/memories/{id}/forget", s.auth(s.forgetMemory))
	mux.HandleFunc("POST /api/speech", s.auth(s.speech))
	mux.HandleFunc("POST /api/hear", s.auth(s.hear))
	mux.HandleFunc("GET /api/voices", s.auth(s.voices))
	mux.HandleFunc("PUT /api/voices", s.auth(s.setVoice))
	mux.HandleFunc("POST /api/messages/{id}/feedback", s.auth(s.feedback))
	mux.HandleFunc("GET /api/connections", s.auth(s.connections))
	mux.HandleFunc("PUT /api/connections", s.auth(s.putConnections))
	mux.HandleFunc("POST /api/connections/import", s.auth(s.importConnections))
	mux.HandleFunc("GET /api/lists", s.auth(s.lists))
	mux.HandleFunc("POST /api/lists", s.auth(s.addToList))
	mux.HandleFunc("PUT /api/lists/items/{id}", s.auth(s.tickListItem))
	mux.HandleFunc("DELETE /api/lists/items/{id}", s.auth(s.deleteListItem))
	mux.HandleFunc("GET /api/voice", s.auth(s.getVoice))
	mux.HandleFunc("PUT /api/voice", s.auth(s.putVoice))
	mux.HandleFunc("GET /api/config", s.auth(s.getConfig))
	mux.HandleFunc("PUT /api/config", s.auth(s.putConfig))
	mux.HandleFunc("GET /api/models", s.auth(s.models))
	mux.HandleFunc("POST /api/models/{tier}/{action}", s.auth(s.modelAction))
	mux.HandleFunc("GET /api/events", s.auth(s.events))
	return cors(mux)
}

// cors lets the app's window call the API. The token, not the origin, is
// what protects it.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			// EventSource cannot send headers, so the stream passes it here.
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			fail(w, http.StatusUnauthorized, "missing or wrong token")
			return
		}
		next(w, r)
	}
}

func dayBounds(now time.Time) (time.Time, time.Time) {
	y, m, d := now.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	return start, start.AddDate(0, 0, 1)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.B.State())
}

// today is the Nova screen: the current session and its thread.
func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"state": s.B.State(), "messages": []store.Event{}}
	open, _ := s.B.S.OpenSessions()
	if cur, ok := s.B.CurrentSession(); ok {
		msgs, err := s.B.S.SessionEvents(cur.ID, 0, "user_msg", "nova_msg", "initiative")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out["session"], out["messages"] = cur, msgs
		others := []store.Session{}
		for _, o := range open {
			if o.ID != cur.ID && o.Messages > 0 {
				others = append(others, o)
			}
		}
		out["earlier"] = others
	}
	intentions, _ := s.B.S.Intentions("open", "sent")
	out["intentions"] = intentions
	if run, ok := s.B.S.LastRun(); ok {
		out["last_run"] = run
	}
	writeJSON(w, 200, out)
}

// sessions lists what she has not slept on yet, for the list under Nova.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	open, err := s.B.S.OpenSessions()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	shown := []store.Session{}
	var current int64
	if cur, ok := s.B.CurrentSession(); ok {
		current = cur.ID
	}
	for _, o := range open {
		if o.Messages > 0 || o.ID == current {
			shown = append(shown, o)
		}
	}
	writeJSON(w, 200, map[string]any{"current": current, "open": shown})
}

func (s *Server) newSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.B.NewSession())
}

func (s *Server) openSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	sess, ok := s.B.OpenSession(id)
	if !ok {
		fail(w, 409, "she has slept on that session; it can be read but not continued")
		return
	}
	writeJSON(w, 200, sess)
}

// session returns one session with its whole thread, for reading back.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	sess, found := s.B.S.GetSession(id)
	if !ok || !found {
		fail(w, 404, "no such session")
		return
	}
	msgs, _ := s.B.S.SessionEvents(id, 0, "user_msg", "nova_msg", "initiative")
	writeJSON(w, 200, map[string]any{"session": sess, "messages": msgs})
}

// run returns how one reply was produced: every step, and for a step
// that failed, why.
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	run, found := s.B.S.GetRun(id)
	if !ok || !found {
		fail(w, 404, "no record of how that reply was made")
		return
	}
	writeJSON(w, 200, run)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		fail(w, 400, "send {\"text\": \"...\"}")
		return
	}
	// The turn finishes even if the window closes mid-reply, so her
	// stream never holds a question without its answer.
	reply, err := s.B.HandleMessage(context.WithoutCancel(r.Context()), in.Text)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "run": reply.Run})
		return
	}
	writeJSON(w, 200, reply)
}

func (s *Server) mind(w http.ResponseWriter, r *http.Request) {
	from, to := dayBounds(s.B.Now())
	stream, err := s.B.S.EventsBetween(from, to)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	vals, _ := s.B.S.Values()
	pending, _ := s.B.S.Proposals("pending", 50)
	// What she committed to memory today, so each moment can show it.
	learned := []store.Memory{}
	all, _ := s.B.S.Memories()
	for _, m := range all {
		if m.Kind == "fact" && !m.CreatedAt.Before(from) && m.CreatedAt.Before(to) {
			learned = append(learned, m)
		}
	}
	intentions, _ := s.B.S.Intentions("open", "sent")
	writeJSON(w, 200, map[string]any{
		"state": s.B.State(), "stream": stream, "values": vals, "pending_proposals": len(pending),
		"learned": learned, "intentions": intentions,
	})
}

func (s *Server) sleepView(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"state": s.B.State()}
	since := time.Time{}
	if raw := s.B.S.Get("last_sleep_at"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
			out["last_sleep_at"] = t
		}
	}
	// An earlier night can be looked at by its time.
	latest := since
	if raw := r.URL.Query().Get("at"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}
	out["viewing"] = since
	out["nights"], _ = s.B.S.SleepNights(60)
	older := !since.Equal(latest)
	reflections := []store.Memory{}
	tidied := []store.Tidy{}
	if !since.IsZero() {
		if journal, _ := s.B.S.EventsBetween(since.Add(-time.Second), since.Add(time.Minute), "journal"); len(journal) > 0 {
			out["journal"] = journal[len(journal)-1].Text
		}
		mems, _ := s.B.S.Memories()
		for _, m := range mems {
			// On an earlier night, only what that night produced.
			if m.Kind == "reflection" && !m.CreatedAt.Before(since.Add(-time.Second)) && (!older || m.CreatedAt.Before(since.Add(10*time.Minute))) {
				reflections = append(reflections, m)
			}
		}
		if !older {
			tidied, _ = s.B.S.TidySince(since.Add(-time.Second))
		}
	}
	out["reflections"] = reflections
	out["tidied"] = tidied
	// The sessions this sleep closed, each with what she kept from it.
	type kept struct {
		store.Session
		Remembered []store.Memory `json:"remembered"`
		// Mistakes are what she got wrong in that conversation, as she
		// wrote them down at the time.
		Mistakes []string `json:"mistakes"`
	}
	closed := []kept{}
	if !since.IsZero() {
		slept, _ := s.B.S.SessionsSleptAt(since)
		for _, x := range slept {
			if x.Messages == 0 {
				continue
			}
			mems, _ := s.B.S.MemoriesFromSession(x.ID)
			wrong := []string{}
			evs, _ := s.B.S.SessionEvents(x.ID, 20, "mistake")
			for _, e := range evs {
				wrong = append(wrong, e.Text)
			}
			closed = append(closed, kept{Session: x, Remembered: mems, Mistakes: wrong})
		}
	}
	out["sessions"] = closed
	// What she is waiting to ask, gathered while asleep.
	questions := []store.Intention{}
	waiting, _ := s.B.S.Intentions("open", "sent")
	for _, i := range waiting {
		if i.Kind == "curiosity" {
			questions = append(questions, i)
		}
	}
	out["questions"] = questions
	out["pending"], _ = s.B.S.Proposals("pending", 50)
	decided := []store.Proposal{}
	all, _ := s.B.S.Proposals("", 20)
	for _, p := range all {
		if p.Status != "pending" {
			decided = append(decided, p)
		}
	}
	out["decided"] = decided
	writeJSON(w, 200, out)
}

func (s *Server) sleepNow(w http.ResponseWriter, r *http.Request) {
	// Sleep takes a while on a large model. Start it and let the app
	// follow along through the event stream.
	go func() {
		report, err := s.B.Sleep(context.Background())
		if err != nil {
			s.B.Bus.Publish("sleep", map[string]any{"error": err.Error()})
			return
		}
		s.B.Bus.Publish("sleep", report)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	var in struct {
		Approve bool `json:"approve"`
	}
	if !ok || json.NewDecoder(r.Body).Decode(&in) != nil {
		fail(w, 400, "send {\"approve\": true|false}")
		return
	}
	p, err := s.B.DecideProposal(id, in.Approve)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) undoTidy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	if err := s.B.UndoTidy(id); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"undone": true})
}

func (s *Server) memories(w http.ResponseWriter, r *http.Request) {
	mems, err := s.B.S.Memories()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	replaced, _ := s.B.S.Replaced()
	type view struct {
		store.Memory
		Replaced string `json:"replaced,omitempty"` // the wording this one took over from
	}
	out := make([]view, len(mems))
	for i, m := range mems {
		out[i] = view{Memory: m, Replaced: replaced[m.ID]}
	}
	writeJSON(w, 200, out)
}

// getVoice returns who she is and how she talks, as the user can edit it.
func (s *Server) getVoice(w http.ResponseWriter, r *http.Request) {
	persona, examples := s.B.Voice()
	writeJSON(w, 200, map[string]string{
		"persona": persona, "examples": examples,
		"default_persona": brain.DefaultPersona, "default_examples": brain.DefaultExamples,
	})
}

// putVoice saves the persona and examples. An empty field goes back to
// the built-in default.
func (s *Server) putVoice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Persona  string `json:"persona"`
		Examples string `json:"examples"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	now := s.B.Now()
	s.B.S.SetPrompt("persona", strings.TrimSpace(in.Persona), now)
	s.B.S.SetPrompt("examples", strings.TrimSpace(in.Examples), now)
	s.getVoice(w, r)
}

func (s *Server) people(w http.ResponseWriter, r *http.Request) {
	people, err := s.B.S.People()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, people)
}

// person gathers everything she holds about one person: the record
// itself, the memories that mention them and the recent moments where
// they came up.
func (s *Server) person(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	people, err := s.B.S.People()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var found *store.Person
	for i := range people {
		if people[i].ID == id {
			found = &people[i]
		}
	}
	if found == nil {
		fail(w, 404, "no such person")
		return
	}
	names := []string{strings.ToLower(found.Name)}
	for _, a := range found.Aliases {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			names = append(names, a)
		}
	}
	// A full name also matches on its first word ("Bethany Bonnici").
	if first, _, ok := strings.Cut(names[0], " "); ok && len(first) >= 3 {
		names = append(names, first)
	}
	mentions := func(text string) bool {
		t := strings.ToLower(text)
		for _, n := range names {
			if strings.Contains(t, n) {
				return true
			}
		}
		return false
	}
	memories := []store.Memory{}
	all, _ := s.B.S.Memories()
	for _, m := range all {
		if mentions(m.Content) {
			memories = append(memories, m)
		}
	}
	moments := []store.Event{}
	recent, _ := s.B.S.RecentEvents(400, "user_msg", "nova_msg", "initiative")
	for i := len(recent) - 1; i >= 0 && len(moments) < 12; i-- {
		if mentions(recent[i].Text) {
			moments = append(moments, recent[i])
		}
	}
	writeJSON(w, 200, map[string]any{"person": found, "memories": memories, "moments": moments, "user": s.B.Cfg.Get().UserName})
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	from, _ := dayBounds(s.B.Now())
	// A year ahead, so a birthday added today is visible wherever it falls.
	items, err := s.B.S.CalendarBetween(from, from.AddDate(1, 0, 0))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) podcasts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.B.Podcasts())
}

func (s *Server) addFeed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	feed, err := s.B.AddFeed(r.Context(), in.URL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, feed)
}

func (s *Server) removeFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	if err := s.B.RemoveFeed(id); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) feedArt(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	feed, ok := s.B.S.GetFeed(id)
	if !ok || feed.ArtPath == "" {
		fail(w, 404, "no artwork")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, feed.ArtPath)
}

func (s *Server) feedEpisodes(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	eps, err := s.B.S.Episodes(id, 60)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, eps)
}

func (s *Server) episode(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	e, ok := s.B.S.GetEpisode(id)
	if !ok {
		fail(w, 404, "no such episode")
		return
	}
	feed, _ := s.B.S.GetFeed(e.FeedID)
	writeJSON(w, 200, map[string]any{"episode": e, "show": feed.Title, "kept": s.B.EpisodeMemories(e)})
}

func (s *Server) episodeTranscript(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	e, ok := s.B.S.GetEpisode(id)
	if !ok || e.TranscriptPath == "" {
		fail(w, 404, "no transcript")
		return
	}
	raw, err := os.ReadFile(e.TranscriptPath)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": string(raw)})
}

func (s *Server) listenToEpisode(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.B.QueueEpisode(id); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// voices lists the voices she can speak with and which one she uses.
func (s *Server) voices(w http.ResponseWriter, r *http.Request) {
	missing := "her voice is not set up"
	if s.Voice != nil {
		missing = s.Voice.Missing()
	}
	current := s.B.Cfg.Get().Voice
	if current == "" {
		current = voice.Default
	}
	writeJSON(w, 200, map[string]any{"voices": voice.Voices, "current": current, "missing": missing})
}

func (s *Server) setVoice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Voice string `json:"voice"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	known := false
	for _, v := range voice.Voices {
		known = known || v.ID == in.Voice
	}
	if !known {
		fail(w, 400, "no such voice")
		return
	}
	cfg := s.B.Cfg.Get()
	cfg.Voice = in.Voice
	if err := s.B.Cfg.Set(cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.voices(w, r)
}

// hear takes a short recording from the microphone and returns the
// words in it. Nothing leaves this computer.
func (s *Server) hear(w http.ResponseWriter, r *http.Request) {
	tmp, err := os.CreateTemp("", "cosmos-heard-*.wav")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	// Two minutes of speech at most.
	n, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, 8<<20))
	tmp.Close()
	if err != nil || n < 1000 {
		fail(w, 400, "the recording was empty or too long")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	text, err := (&brain.WhisperCLI{}).Hear(ctx, tmp.Name())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

// speech turns text into sound with the computer's own voice and sends
// the sound back. The app plays it, and moves her mouth to it.
func (s *Server) speech(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
		// Voice, when given, is tried out without being saved.
		Voice string `json:"voice"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	// Marks that are for the eye, not the ear.
	text := strings.NewReplacer("*", "", "_", " ", "`", "", "#", "", "\n", " ").Replace(in.Text)
	text = strings.TrimSpace(text)
	if text == "" {
		fail(w, 400, "nothing to say")
		return
	}
	if r := []rune(text); len(r) > 1500 {
		text = string(r[:1500])
	}
	// Her own voice when it is set up; the computer's plain one if not.
	if s.Voice != nil && s.Voice.Missing() == "" {
		which := in.Voice
		if which == "" {
			which = s.B.Cfg.Get().Voice
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		spoken, err := s.Voice.Speak(ctx, text, which, 1)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, spoken)
		return
	}
	bin, err := exec.LookPath("say")
	if err != nil {
		fail(w, 501, "this computer has no built-in voice")
		return
	}
	out, err := os.CreateTemp("", "cosmos-speech-*.wav")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out.Close()
	defer os.Remove(out.Name())
	args := []string{"-o", out.Name(), "--data-format=LEI16@22050"}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// The words go in on standard input, so nothing in them is ever
	// read as an option.
	cmd.Stdin = strings.NewReader(text)
	if msg, err := cmd.CombinedOutput(); err != nil {
		fail(w, 500, "the voice did not work: "+strings.TrimSpace(string(msg)+" "+err.Error()))
		return
	}
	// The built-in voice gives no timing; the app falls back to moving
	// her mouth by the sound alone.
	wav, err := os.ReadFile(out.Name())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, voice.Spoken{Audio: base64.StdEncoding.EncodeToString(wav), Marks: []json.RawMessage{}})
}

func (s *Server) feedback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	var in struct {
		Good bool `json:"good"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := s.B.Feedback(id, in.Good); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// connections lists the user's own tool servers with how each is doing.
func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	type view struct {
		mcp.Server
		Status mcp.Status `json:"status"`
	}
	out := []view{}
	for _, sv := range s.B.Cfg.Get().MCPServers {
		v := view{Server: sv, Status: mcp.Status{Tools: []mcp.Tool{}}}
		// What a server was started with can hold keys. The app is shown
		// which settings exist, not their values.
		hidden := map[string]string{}
		for k := range sv.Env {
			hidden[k] = ""
		}
		v.Env = hidden
		if s.B.MCP != nil {
			v.Status = s.B.MCP.Status(sv.Name)
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

// putConnections switches servers on and off, or removes them. Only the
// switches are taken from the app; commands and settings stay as saved.
func (s *Server) putConnections(w http.ResponseWriter, r *http.Request) {
	var in []struct {
		Name         string `json:"name"`
		Enabled      bool   `json:"enabled"`
		AllowChanges bool   `json:"allow_changes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	cfg := s.B.Cfg.Get()
	kept := []mcp.Server{}
	for _, want := range in {
		for _, have := range cfg.MCPServers {
			if have.Name == want.Name {
				have.Enabled, have.AllowChanges = want.Enabled, want.AllowChanges
				kept = append(kept, have)
			}
		}
	}
	cfg.MCPServers = kept
	if err := s.B.Cfg.Set(cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if s.B.MCP != nil {
		s.B.MCP.Sync(kept)
	}
	s.connections(w, r)
}

// importConnections reads the tool servers set up for Claude Code on
// this computer and adds the ones not here yet, switched off.
func (s *Server) importConnections(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		fail(w, 404, "no Claude Code settings were found on this computer")
		return
	}
	var file struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		fail(w, 400, "the Claude Code settings could not be read")
		return
	}
	cfg := s.B.Cfg.Get()
	have := map[string]bool{}
	for _, sv := range cfg.MCPServers {
		have[sv.Name] = true
	}
	names := []string{}
	for name := range file.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sv := file.Servers[name]
		// Only programs run on this computer; servers reached over the
		// network are left out.
		if have[name] || sv.Command == "" || (sv.Type != "" && sv.Type != "stdio") {
			continue
		}
		cfg.MCPServers = append(cfg.MCPServers, mcp.Server{Name: name, Command: sv.Command, Args: sv.Args, Env: sv.Env})
	}
	if err := s.B.Cfg.Set(cfg); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.connections(w, r)
}

// forgetMemory puts a memory away. It is archived, not deleted, so a
// slip can be put right in the database.
func (s *Server) forgetMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	if err := s.B.S.SetMemoryArchived(id, true); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.B.Bus.Publish("podcasts", nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) lists(w http.ResponseWriter, r *http.Request) {
	items, err := s.B.ListsView()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) addToList(w http.ResponseWriter, r *http.Request) {
	var in struct {
		List string `json:"list"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, 400, "unreadable item: "+err.Error())
		return
	}
	in.List, in.Text = strings.ToLower(strings.TrimSpace(in.List)), strings.TrimSpace(in.Text)
	if in.List == "" || in.Text == "" {
		fail(w, 400, "a list and an item are both needed")
		return
	}
	item, err := s.B.S.AddListItem(in.List, in.Text, s.B.Now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.B.Bus.Publish("lists", nil)
	writeJSON(w, 200, item)
}

func (s *Server) tickListItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "bad id")
		return
	}
	var in struct {
		Done bool `json:"done"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, 400, "unreadable: "+err.Error())
		return
	}
	if err := s.B.S.SetListItemDone(id, in.Done, s.B.Now()); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.B.Bus.Publish("lists", nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteListItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "bad id")
		return
	}
	if err := s.B.S.DeleteListItem(id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.B.Bus.Publish("lists", nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.B.Cfg.Get())
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	c := s.B.Cfg.Get()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&c); err != nil {
		fail(w, 400, "unreadable config: "+err.Error())
		return
	}
	if strings.TrimSpace(c.CompanionName) == "" || strings.TrimSpace(c.UserName) == "" {
		fail(w, 400, "both names are needed")
		return
	}
	if c.Planning != "auto" && c.Planning != "once" && c.Planning != "steps" {
		fail(w, 400, "planning must be auto, once or steps")
		return
	}
	if err := s.B.Cfg.Set(c); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

// models describes the three model servers for the Models screen: what
// each tier points at, whether it is answering, what its server last
// printed, and which model files are on disk to choose from.
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	cfg := s.B.Cfg.Get()
	type tierView struct {
		Tier       string          `json:"tier"`
		Endpoint   config.Endpoint `json:"endpoint"` // as configured, before any fallback
		SharedWith string          `json:"shared_with,omitempty"`
		Status     launcher.Status `json:"status"`
	}
	own := map[string]config.Endpoint{"main": cfg.Main, "worker": cfg.Worker, "sleeper": cfg.Sleeper, "embed": cfg.Embed}
	tiers := []tierView{}
	for _, tier := range []string{"main", "worker", "sleeper", "embed"} {
		v := tierView{Tier: tier, Endpoint: own[tier]}
		if tier != "main" && tier != "embed" && own[tier].BaseURL == "" {
			v.SharedWith = "main"
		}
		if s.Launch != nil {
			v.Status = s.Launch.Status(r.Context(), tier)
		} else if launcher.Healthy(r.Context(), cfg.Tier(tier).BaseURL) {
			v.Status = launcher.Status{State: "external"}
		} else {
			v.Status = launcher.Status{State: "stopped"}
		}
		tiers = append(tiers, v)
	}
	writeJSON(w, 200, map[string]any{
		"tiers":   tiers,
		"binary":  launcher.FindBinary(""),
		"files":   launcher.ScanModels(s.ModelDirs),
		"folders": s.ModelDirs,
	})
}

// modelAction starts, stops or restarts the server for a tier. Loading a
// model takes a while, so start returns at once and the screen follows
// progress through GET /api/models.
// restingWhy explains a model that is stopped because of her state.
func restingWhy(tier string) string {
	if tier == "sleeper" {
		return "The sleep model only loads while she sleeps, so it never shares memory with the main and worker models."
	}
	return "She is asleep, so this model is stopped to leave room for the sleep model. It loads again when she wakes."
}

func (s *Server) modelAction(w http.ResponseWriter, r *http.Request) {
	tier, action := r.PathValue("tier"), r.PathValue("action")
	if s.Launch == nil {
		fail(w, 400, "this core does not manage model servers")
		return
	}
	if tier != "main" && tier != "worker" && tier != "sleeper" && tier != "embed" {
		fail(w, 404, "unknown model "+tier)
		return
	}
	ep := s.B.Cfg.Get().Tier(tier)
	start := func() {
		go func() {
			if err := s.Launch.Ensure(context.Background(), tier); err != nil {
				s.B.Log.Printf("models: %s: %v", tier, err)
			}
		}()
	}
	switch action {
	case "start":
		if ep.Launch == nil || ep.Launch.ModelPath == "" {
			fail(w, 400, "choose a model file first")
			return
		}
		if s.Launch.Resting(tier) {
			fail(w, 409, restingWhy(tier))
			return
		}
		start()
	case "stop":
		if err := s.Launch.Stop(tier); err != nil {
			fail(w, 409, err.Error())
			return
		}
	case "restart":
		if s.Launch.Resting(tier) {
			fail(w, 409, restingWhy(tier))
			return
		}
		s.Launch.Stop(tier)
		time.Sleep(400 * time.Millisecond)
		start()
	default:
		fail(w, 404, "unknown action "+action)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"ok": action})
}

// events streams the brain's signals to the app as server-sent events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ch, cancel := s.B.Bus.Subscribe()
	defer cancel()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case sig := <-ch:
			raw, err := json.Marshal(sig)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
	}
}
