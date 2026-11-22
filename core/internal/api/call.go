package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"cosmos/core/internal/brain"
	"cosmos/core/internal/store"
	"cosmos/core/internal/voice"
)

// A call is her ears and voice over one connection, for as long as the
// microphone is open. The app sends the sound from the microphone and
// plays what comes back; everything in between happens here: when you
// started and stopped speaking, whose voice it was, whether it was for
// her, cutting her off, and her voice itself.
//
// Up, from the app:
//   - binary: the microphone, 16 kHz mono 16-bit samples;
//   - {"type":"mode","by_name":true}: whether she waits for her name;
//   - {"type":"her","speaking":true}: her voice started or stopped playing;
//   - {"type":"idle"}: her reply is over, said and shown;
//   - {"type":"speak","id":3,"text":"…","voice":""}: say this;
//   - {"type":"played","id":3}: that sound started playing.
//
// Down, to the app:
//   - {"type":"ready","detector":"silero"}: listening;
//   - {"type":"start"}, {"type":"dropped"}: something began, and was not speech;
//   - {"type":"cut"}: someone is speaking over her: she stops;
//   - {"type":"writing"}: what was said is being written out;
//   - {"type":"say","text":"…","heard":7,"cut":true}: send this to her as said aloud;
//   - {"type":"overheard","text":"…"}, {"type":"ignored"}: not for her;
//   - {"type":"window","open":true}: she takes what is said as for her;
//   - {"type":"problem","text":"…"};
//   - binary: speech, a 4-byte header length, a JSON header
//     {"id":3,"marks":[…]} (or {"id":3,"error":"…"}), then a WAV file.

type upMsg struct {
	Type     string `json:"type"`
	ByName   bool   `json:"by_name"`
	Speaking bool   `json:"speaking"`
	ID       int64  `json:"id"`
	Text     string `json:"text"`
	Voice    string `json:"voice"`
}

// frameCodec keeps whether a frame was text or binary.
var frameCodec = websocket.Codec{
	Marshal: func(v any) ([]byte, byte, error) {
		if b, ok := v.([]byte); ok {
			return b, websocket.BinaryFrame, nil
		}
		raw, err := json.Marshal(v)
		return raw, websocket.TextFrame, err
	},
	Unmarshal: func(data []byte, kind byte, v any) error {
		f := v.(*wsFrame)
		f.binary, f.data = kind == websocket.BinaryFrame, data
		return nil
	},
}

type wsFrame struct {
	binary bool
	data   []byte
}

// timing is how quickly one thing said was answered, from the moment
// the speaker stopped.
type timing struct {
	stopped    time.Time // they stopped speaking
	heard      []store.Step
	run        int64
	firstSpeak time.Time
	firstAudio time.Time
	written    bool
}

type call struct {
	s   *Server
	ws  *websocket.Conn
	out chan any
	ctx context.Context

	mu          sync.Mutex
	byName      bool
	herSpeaking bool
	// The window: while open, what is said is taken as for her without
	// her name. Held open while she works on a reply, then open until.
	holding bool
	until   time.Time
	closing bool
	shut    *time.Timer
	// Her own words, lately, so that what comes back of them through the
	// microphone is known to be hers.
	hers   map[string]bool
	hersAt time.Time
	// What was said, by its number, until her reply to it is timed.
	nextID  int64
	timings map[int64]*timing
	turn    *timing
}

// call is the voice call: GET /api/call, upgraded to a WebSocket.
func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	srv := websocket.Server{
		// The app's own pages are file:// or a local address; the token
		// has already been checked.
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler:   func(ws *websocket.Conn) { s.runCall(ws) },
	}
	srv.ServeHTTP(w, r)
}

func (s *Server) runCall(ws *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &call{s: s, ws: ws, out: make(chan any, 64), ctx: ctx, byName: true, hers: map[string]bool{}, timings: map[int64]*timing{}}
	s.callMu.Lock()
	if s.current != nil {
		s.current.ws.Close() // one call at a time; the newer wins
	}
	s.current = c
	s.callMu.Unlock()
	defer func() {
		s.callMu.Lock()
		if s.current == c {
			s.current = nil
		}
		s.callMu.Unlock()
		c.mu.Lock()
		if c.shut != nil {
			c.shut.Stop()
		}
		c.mu.Unlock()
		// What she overheard goes with her ears.
		s.B.ForgetRoom()
	}()

	// One writer, so frames never interleave.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case v := <-c.out:
				if err := frameCodec.Send(ws, v); err != nil {
					ws.Close()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-done; ws.Close() }()

	// Her ears and voice load now, not on the first thing said.
	s.warm()
	det := voice.OpenDetector(s.voiceDir())
	defer det.Close()
	if det.Kind() != "silero" {
		s.B.Log.Printf("call: judging speech by loudness; put silero_vad.onnx in ~/models/vad for better ears")
	}
	cfg := s.B.Cfg.Get()
	seg := &voice.Segmenter{Pause: time.Duration(cfg.HearPause * float64(time.Second))}
	c.send(map[string]any{"type": "ready", "detector": det.Kind()})

	// What was said is written out one thing at a time, in order.
	said := make(chan []int16, 8)
	go func() {
		for {
			select {
			case pcm := <-said:
				c.hear(pcm, seg.Pause)
			case <-ctx.Done():
				return
			}
		}
	}()

	var pending []int16
	for {
		var f wsFrame
		if err := frameCodec.Receive(ws, &f); err != nil {
			return
		}
		if !f.binary {
			c.control(f.data)
			continue
		}
		for i := 0; i+1 < len(f.data); i += 2 {
			pending = append(pending, int16(binary.LittleEndian.Uint16(f.data[i:])))
		}
		for len(pending) >= voice.Frame {
			frame := pending[:voice.Frame:voice.Frame]
			pending = pending[voice.Frame:]
			p, err := det.Speech(frame)
			if err != nil {
				// The detector stopped; carry on by loudness.
				s.B.Log.Printf("call: %s stopped: %v", det.Kind(), err)
				det.Close()
				det = &voice.Loudness{}
				p, _ = det.Speech(frame)
			}
			c.mu.Lock()
			her := c.herSpeaking
			c.mu.Unlock()
			switch h, pcm := seg.Push(frame, p, her); h {
			case voice.Started:
				c.send(map[string]string{"type": "start"})
			case voice.Dropped:
				c.send(map[string]string{"type": "dropped"})
			case voice.Confirmed:
				c.confirmed(append([]int16(nil), seg.SoFar()...))
			case voice.Finished:
				c.send(map[string]string{"type": "writing"})
				select {
				case said <- pcm:
				default:
					c.send(map[string]string{"type": "ignored"})
				}
			}
		}
	}
}

func (c *call) send(v any) {
	select {
	case c.out <- v:
	case <-c.ctx.Done():
	}
}

func (c *call) control(raw []byte) {
	var m upMsg
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	switch m.Type {
	case "mode":
		c.mu.Lock()
		c.byName = m.ByName
		c.mu.Unlock()
	case "her":
		c.mu.Lock()
		c.herSpeaking = m.Speaking
		c.mu.Unlock()
	case "idle":
		c.letRun()
	case "speak":
		c.speak(m)
	case "played":
		c.mu.Lock()
		if t := c.turn; t != nil && !t.firstSpeak.IsZero() && t.firstAudio.IsZero() {
			t.firstAudio = time.Now()
			c.writeTiming(t)
		}
		c.mu.Unlock()
	}
}

// confirmed is someone clearly speaking. If she is talking, she stops,
// as a person would, unless it is her own voice coming back, or (while
// she waits for her name) someone she is not talking with.
func (c *call) confirmed(sofar []int16) {
	c.mu.Lock()
	open := !c.byName || c.inWindow()
	her := c.herSpeaking
	c.mu.Unlock()
	if !open || !her {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
		defer cancel()
		if isHer, _ := c.s.B.IsHer(ctx, voice.WAV(sofar)); isHer {
			return
		}
		c.send(map[string]string{"type": "cut"})
	}()
}

// hear writes out one thing said and decides what it was: her own
// voice, talk not for her, or something to answer.
func (c *call) hear(pcm []int16, pause time.Duration) {
	finished := time.Now()
	stopped := finished.Add(-pause)
	cfg := c.s.B.Cfg.Get()
	ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
	defer cancel()
	wav := voice.WAV(pcm)
	spoke := make(chan brain.Speaker, 1)
	go func() { spoke <- c.s.B.WhoSpoke(ctx, wav) }()
	c.mu.Lock()
	needName := c.byName && !c.inWindow()
	c.mu.Unlock()
	text, addressed, err := c.s.listen(ctx, cfg, wav, needName)
	written := time.Now()
	who := <-spoke
	if err != nil {
		c.send(map[string]string{"type": "problem", "text": "She could not make that out: " + err.Error()})
		c.send(map[string]string{"type": "ignored"})
		return
	}
	words := strings.TrimSpace(text)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isHers(words) {
		who.Who = "her"
	}
	gate := voice.Gate{ByName: c.byName, Open: c.inWindow(), AnswerOthers: cfg.AnswerOthers}
	switch gate.Decide(voice.Said{Words: words, Addressed: addressed, Who: who.Who}) {
	case voice.Hers, voice.Noise:
		c.send(map[string]string{"type": "ignored"})
		return
	case voice.Overheard:
		line := words
		if who.Who == "person" && who.Name != "" {
			line = who.Name + ": " + words
		}
		c.s.B.Overhear(line)
		c.send(map[string]string{"type": "overheard", "text": line})
		return
	}
	c.nextID++
	id := c.nextID
	note := fmt.Sprintf("%.1f s of quiet, then written out in %d ms", pause.Seconds(), written.Sub(finished).Milliseconds())
	if who.Who != "" {
		note += " · " + who.Who
	}
	t := &timing{stopped: stopped, heard: []store.Step{{Name: "heard", Tier: "voice", OK: true, Millis: written.Sub(stopped).Milliseconds(), Note: note}}}
	c.timings[id] = t
	c.turn = t
	for k := range c.timings {
		if k < id-8 {
			delete(c.timings, k)
		}
	}
	c.holdOpen()
	c.closing = voice.Ending(words)
	// Said while she was still working on a reply: that reply is dropped
	// and she answers both together.
	cut := c.s.B.Interrupt()
	c.send(map[string]any{"type": "say", "text": words, "heard": id, "cut": cut})
}

// heardSteps are the steps of hearing something said, for its reply's run.
func (s *Server) heardSteps(id int64) []store.Step {
	s.callMu.Lock()
	c := s.current
	s.callMu.Unlock()
	if c == nil || id == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.timings[id]; t != nil {
		return t.heard
	}
	return nil
}

// answered ties what was said to the run of her reply, so how soon her
// voice was heard can be added to it.
func (s *Server) answered(id, run int64) {
	s.callMu.Lock()
	c := s.current
	s.callMu.Unlock()
	if c == nil || id == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.timings[id]; t != nil {
		t.run = run
		c.writeTiming(t)
	}
}

// writeTiming adds to the run how soon after the user stopped speaking
// her first sentence was ready to say and first heard. Called with mu held.
func (c *call) writeTiming(t *timing) {
	if t.written || t.run == 0 || t.firstAudio.IsZero() {
		return
	}
	t.written = true
	b := c.s.B
	b.AddRunStep(t.run, store.Step{Name: "first sentence", Tier: "voice", OK: true, Millis: t.firstSpeak.Sub(t.stopped).Milliseconds(), Note: "since you stopped speaking"})
	b.AddRunStep(t.run, store.Step{Name: "first audio", Tier: "voice", OK: true, Millis: t.firstAudio.Sub(t.stopped).Milliseconds(), Note: "since you stopped speaking"})
	b.Log.Printf("voice: first audio %d ms after you stopped (run %d)", t.firstAudio.Sub(t.stopped).Milliseconds(), t.run)
}

// speak makes the sound for a sentence and sends it down as one binary
// frame. Sentences are made side by side; the app plays them in order.
func (c *call) speak(m upMsg) {
	text := sayable(m.Text)
	c.mu.Lock()
	c.noteHers(text)
	if t := c.turn; t != nil && t.firstSpeak.IsZero() {
		t.firstSpeak = time.Now()
	}
	c.mu.Unlock()
	go func() {
		header := map[string]any{"id": m.ID}
		var wav []byte
		if text == "" {
			header["error"] = "nothing to say"
		} else {
			ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
			defer cancel()
			var marks []json.RawMessage
			var err error
			wav, marks, err = c.s.spoken(ctx, text, m.Voice)
			if err != nil {
				header["error"] = err.Error()
			} else {
				header["marks"] = marks
			}
		}
		head, _ := json.Marshal(header)
		frame := make([]byte, 4, 4+len(head)+len(wav))
		binary.LittleEndian.PutUint32(frame, uint32(len(head)))
		frame = append(append(frame, head...), wav...)
		c.send(frame)
	}()
}

// The window. Called with mu held.
func (c *call) inWindow() bool { return c.holding || time.Now().Before(c.until) }

func (c *call) holdOpen() {
	c.holding = true
	if c.shut != nil {
		c.shut.Stop()
	}
	c.send(map[string]any{"type": "window", "open": true})
}

// letRun: her reply is over, so the window runs from now, or shuts if
// the user said goodbye.
func (c *call) letRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.holding {
		return
	}
	c.holding = false
	if c.closing {
		c.closing = false
		c.until = time.Time{}
		c.send(map[string]any{"type": "window", "open": false})
		return
	}
	window := time.Duration(c.s.B.Cfg.Get().ListenWindow) * time.Second
	c.until = time.Now().Add(window)
	c.shut = time.AfterFunc(window, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.inWindow() {
			c.send(map[string]any{"type": "window", "open": false})
		}
	})
}

func wordsOf(line string) []string {
	return strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '\'')
	})
}

// noteHers and isHers: called with mu held.
func (c *call) noteHers(line string) {
	if time.Since(c.hersAt) > 30*time.Second {
		c.hers = map[string]bool{}
	}
	for _, w := range wordsOf(line) {
		if len(w) > 1 {
			c.hers[w] = true
		}
	}
	c.hersAt = time.Now()
}

func (c *call) isHers(heard string) bool {
	if time.Since(c.hersAt) > 30*time.Second {
		return false
	}
	n, shared := 0, 0
	for _, w := range wordsOf(heard) {
		if len(w) < 2 {
			continue
		}
		n++
		if c.hers[w] {
			shared++
		}
	}
	// Nearly all of it is something she just said.
	return n > 0 && float64(shared)/float64(n) >= 0.7
}

// warm gets her ears and voice ready, in the background.
func (s *Server) warm() {
	cfg := s.B.Cfg.Get()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if s.Hearing != nil {
			if err := s.Hearing.Ready(ctx, cfg.HearModel, cfg.HearLanguage); err != nil {
				s.B.Log.Printf("hear ready: %v", err)
			}
		}
		if s.Voice != nil && s.Voice.Missing() == "" {
			if err := s.Voice.Ready(ctx); err != nil {
				s.B.Log.Printf("voice ready: %v", err)
			}
		}
	}()
}

// voiceDir holds the Python environment her ears and voice share.
func (s *Server) voiceDir() string {
	if s.Voice != nil {
		return s.Voice.Dir
	}
	if s.Senses != nil {
		return s.Senses.Dir
	}
	return ""
}
