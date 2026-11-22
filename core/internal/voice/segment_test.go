package voice

import (
	"math"
	"testing"
	"time"
)

// feed pushes seconds of frames all at probability p and returns what
// happened, in order, leaving out the frames where nothing did.
func feed(s *Segmenter, seconds, p float64, her bool) (got []Heard, said []int16) {
	frame := make([]int16, Frame)
	for i := 0; i < int(seconds*Rate/Frame); i++ {
		h, pcm := s.Push(frame, p, her)
		if h != Nothing {
			got = append(got, h)
		}
		if h == Finished {
			said = pcm
		}
	}
	return got, said
}

func TestASentenceIsHeardOnceItEnds(t *testing.T) {
	s := &Segmenter{}
	got, _ := feed(s, 0.5, 0.1, false)
	if len(got) != 0 {
		t.Fatalf("a quiet room was heard as %v", got)
	}
	got, _ = feed(s, 2, 0.9, false)
	if len(got) != 2 || got[0] != Started || got[1] != Confirmed {
		t.Fatalf("two seconds of speech gave %v, want started then confirmed", got)
	}
	got, said := feed(s, 1.2, 0.05, false)
	if len(got) != 1 || got[0] != Finished {
		t.Fatalf("the pause after it gave %v, want finished", got)
	}
	seconds := float64(len(said)) / Rate
	if seconds < 2 || seconds > 2.8 {
		t.Fatalf("kept %.2fs, want the two seconds said and a little either side", seconds)
	}
}

func TestAShortPauseDoesNotEndIt(t *testing.T) {
	s := &Segmenter{Pause: time.Second}
	feed(s, 1, 0.9, false)
	if got, _ := feed(s, 0.6, 0.1, false); len(got) != 0 {
		t.Fatalf("a breath ended it: %v", got)
	}
	feed(s, 1, 0.9, false)
	got, said := feed(s, 1.2, 0.1, false)
	if len(got) != 1 || got[0] != Finished || float64(len(said))/Rate < 2.4 {
		t.Fatalf("got %v with %.2fs, want one sentence holding both halves", got, float64(len(said))/Rate)
	}
}

func TestAKnockIsDropped(t *testing.T) {
	s := &Segmenter{}
	feed(s, 0.2, 0.95, false)
	got, _ := feed(s, 1.2, 0.0, false)
	if len(got) != 1 || got[0] != Dropped {
		t.Fatalf("a knock gave %v, want dropped", got)
	}
}

func TestOverHerOwnVoiceMoreIsAsked(t *testing.T) {
	s := &Segmenter{}
	// Her voice from the speakers, half sure to be speech.
	if got, _ := feed(s, 2, 0.7, true); len(got) != 0 {
		t.Fatalf("her echo was heard as %v", got)
	}
	// Someone plainly speaking over her still is.
	if got, _ := feed(s, 1, 0.95, true); len(got) == 0 || got[0] != Started {
		t.Fatalf("speaking over her gave %v", got)
	}
}

func TestWAVRoundTrip(t *testing.T) {
	pcm := make([]int16, Rate/2)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(float64(i)/10))
	}
	back, err := PCM(WAV(pcm))
	if err != nil || len(back) != len(pcm) || back[100] != pcm[100] {
		t.Fatalf("round trip lost the sound: %v, %d samples", err, len(back))
	}
}
