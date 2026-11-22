package voice

import "time"

// The segmenter decides, frame by frame, when someone has started
// speaking and when they have finished, from how sure the detector is
// that each frame holds speech. It has no clock of its own: time is
// counted in frames, so it runs the same on a recording as live.

// Heard is what one frame did to what is being heard.
type Heard int

const (
	Nothing Heard = iota
	// Started: something like speech began. Maybe a voice, maybe a cough.
	Started
	// Confirmed: it has gone on long enough to be someone speaking.
	Confirmed
	// Dropped: it ended too short to be speech, and is let go.
	Dropped
	// Finished: they stopped. The samples are what was said.
	Finished
)

// Segmenter cuts the sound from the microphone into things said.
type Segmenter struct {
	// Pause is how long a quiet stretch has to be to end what was being
	// said. Shorter pauses, for a breath or a thought, are kept inside it.
	Pause time.Duration

	speaking bool
	run      int // frames in a row over the bar, before speech starts
	quiet    int // frames in a row under the bar, while speaking
	voiced   int // frames over the bar in what is being kept
	sure     bool
	kept     []int16
	before   [][]int16
	// Frames left in which her own voice may still be ringing in the
	// room: speech then has to be surer, and last longer, to count.
	echo int
}

const (
	// A frame counts as speech at this, and stays counted down to the
	// lower figure once speech has begun, so a soft syllable does not
	// break a sentence.
	speechOn  = 0.5
	speechOff = 0.35
	// Over her voice coming back from the speakers, more is asked.
	echoOn = 0.8
	// About a third of a second kept from before speech was noticed, so
	// the first syllable is not lost.
	beforeFrames = 10
	// Half a second of voice is someone speaking, not a knock.
	sureFrames = 16
	// Under this much voice it was not a sentence.
	leastFrames = 12
	// Half a minute is the most taken in one go.
	mostFrames = 30 * Rate / Frame
	// Her voice can ring on in a room for a moment after she stops.
	echoFrames = 19
)

// DefaultPause is the quiet that ends what was said when none is set:
// longer than a breath, so a sentence is not cut at a comma.
const DefaultPause = 900 * time.Millisecond

func (s *Segmenter) pauseFrames() int {
	p := s.Pause
	if p <= 0 {
		p = DefaultPause
	}
	return int(p / FrameTime)
}

// Speaking says whether something is being heard right now.
func (s *Segmenter) Speaking() bool { return s.speaking }

// SoFar is what has been kept of what is being said.
func (s *Segmenter) SoFar() []int16 { return s.kept }

// Reset lets go of whatever was being heard.
func (s *Segmenter) Reset() {
	s.speaking, s.sure = false, false
	s.run, s.quiet, s.voiced = 0, 0, 0
	s.kept = nil
	s.before = s.before[:0]
}

// Push takes the next frame and how sure the detector is that it holds
// speech. herSpeaking is whether she is talking at that moment. When it
// returns Finished, the samples are what was said.
func (s *Segmenter) Push(frame []int16, p float64, herSpeaking bool) (Heard, []int16) {
	if herSpeaking {
		s.echo = echoFrames
	} else if s.echo > 0 {
		s.echo--
	}
	on := speechOn
	start := 3
	if s.echo > 0 {
		on, start = echoOn, 5
	}
	if !s.speaking {
		s.before = append(s.before, frame)
		if len(s.before) > beforeFrames {
			s.before = s.before[1:]
		}
		if p >= on {
			s.run++
		} else {
			s.run = 0
		}
		if s.run < start {
			return Nothing, nil
		}
		s.speaking = true
		s.kept = nil
		for _, f := range s.before {
			s.kept = append(s.kept, f...)
		}
		s.before = s.before[:0]
		s.voiced, s.quiet, s.run = start, 0, 0
		return Started, nil
	}
	s.kept = append(s.kept, frame...)
	off := speechOff
	if s.echo > 0 {
		off = on - 0.15
	}
	if p >= off {
		s.voiced++
		s.quiet = 0
	} else {
		s.quiet++
	}
	if s.quiet >= s.pauseFrames() || len(s.kept) >= mostFrames*Frame {
		voiced, quiet := s.voiced, s.quiet
		said := s.kept
		s.Reset()
		if voiced < leastFrames {
			return Dropped, nil
		}
		// Most of the quiet at the end is not kept; a little is, so the
		// last word is not clipped.
		tail := (quiet - 6) * Frame
		if tail > 0 && tail < len(said) {
			said = said[:len(said)-tail]
		}
		return Finished, said
	}
	if !s.sure && s.voiced >= sureFrames {
		s.sure = true
		return Confirmed, nil
	}
	return Nothing, nil
}
