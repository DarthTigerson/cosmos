package voice

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

//go:embed vad_server.py
var vadServer []byte

// Rate and Frame are the sound her ears work in: 16 kHz mono, judged
// 512 samples (32 ms) at a time.
const (
	Rate  = 16000
	Frame = 512
)

// FrameTime is how long one frame lasts.
const FrameTime = time.Duration(Frame) * time.Second / Rate

// Detector says how sure it is, from 0 to 1, that a frame holds speech.
type Detector interface {
	Speech(frame []int16) (float64, error)
	// Kind names it, for the settings screen and the logs.
	Kind() string
	Close()
}

// VADModel is Silero's model in ~/models/vad, or "" when it is not there.
func VADModel() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, "models", "vad", "silero_vad.onnx")
	if exists(p) {
		return p
	}
	return ""
}

// OpenDetector starts Silero for one call, in the Python environment in
// dir. Without the model or the environment it falls back to judging by
// loudness, which is what she had before.
func OpenDetector(dir string) Detector {
	model := VADModel()
	python := filepath.Join(dir, ".venv", "bin", "python")
	if model == "" || !exists(python) {
		return &Loudness{}
	}
	d, err := openSilero(dir, python, model)
	if err != nil {
		return &Loudness{}
	}
	return d
}

// silero is the helper process: frames go in on its standard input and
// a float per frame comes back on its standard output.
type silero struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	buf []byte
}

func openSilero(dir, python, model string) (*silero, error) {
	script := filepath.Join(dir, "vad_server.py")
	if err := os.WriteFile(script, vadServer, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command(python, "-I", script, model)
	var problems bytes.Buffer
	cmd.Stderr = &problems
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &silero{cmd: cmd, in: in, out: bufio.NewReader(out), buf: make([]byte, Frame*2)}
	// It says when the model is loaded.
	ready := make(chan error, 1)
	go func() {
		p, err := s.read()
		if err == nil && p >= 0 {
			err = errors.New("it said something before it was ready")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("silero: %v %s", err, lastLine(problems.String()))
		}
	case <-time.After(20 * time.Second):
		s.Close()
		return nil, errors.New("silero did not become ready")
	}
	return s, nil
}

func (s *silero) read() (float64, error) {
	var b [4]byte
	if _, err := io.ReadFull(s.out, b[:]); err != nil {
		return 0, err
	}
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b[:]))), nil
}

func (s *silero) Speech(frame []int16) (float64, error) {
	if len(frame) != Frame {
		return 0, fmt.Errorf("a frame is %d samples, not %d", Frame, len(frame))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range frame {
		binary.LittleEndian.PutUint16(s.buf[i*2:], uint16(v))
	}
	if _, err := s.in.Write(s.buf); err != nil {
		return 0, err
	}
	return s.read()
}

func (s *silero) Kind() string { return "silero" }

func (s *silero) Close() {
	s.in.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	go s.cmd.Wait()
}

// Loudness judges speech by how far a frame stands above the room. It
// cannot tell a voice from a door, so the segmenter's length rules do
// more of the work.
type Loudness struct {
	floor float64
}

func (l *Loudness) Speech(frame []int16) (float64, error) {
	if l.floor == 0 {
		l.floor = 0.004
	}
	rms := RMS(frame)
	bar := math.Max(0.012, l.floor*3.2)
	if rms <= bar {
		l.floor += (rms - l.floor) * 0.05
	}
	// Twice the bar is as sure as it gets.
	return math.Max(0, math.Min(1, (rms-bar*0.5)/(bar*1.5))), nil
}

func (l *Loudness) Kind() string { return "loudness" }
func (l *Loudness) Close()       {}

// RMS is how loud a stretch of sound is, from 0 to 1.
func RMS(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, v := range pcm {
		f := float64(v) / 32768
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// WAV wraps 16 kHz mono samples as a WAV file.
func WAV(pcm []int16) []byte {
	var b bytes.Buffer
	size := len(pcm) * 2
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+size))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(Rate), uint32(Rate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(size))
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

// PCM reads the samples out of a 16-bit WAV file, mono or the first
// channel, resampled to 16 kHz if it was recorded at another rate.
func PCM(wav []byte) ([]int16, error) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var channels, bits uint16
	var rate uint32
	at := 12
	for at+8 <= len(wav) {
		id := string(wav[at : at+4])
		size := int(binary.LittleEndian.Uint32(wav[at+4:]))
		body := wav[at+8 : min(len(wav), at+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.New("a WAV file with a short format")
			}
			channels = binary.LittleEndian.Uint16(body[2:])
			rate = binary.LittleEndian.Uint32(body[4:])
			bits = binary.LittleEndian.Uint16(body[14:])
		case "data":
			if bits != 16 || channels == 0 {
				return nil, errors.New("only 16-bit WAV files are read")
			}
			step := int(channels) * 2
			pcm := make([]int16, len(body)/step)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(body[i*step:]))
			}
			return Resample(pcm, int(rate)), nil
		}
		at += 8 + size + size%2
	}
	return nil, errors.New("a WAV file with no sound in it")
}

// Resample brings sound at another rate to 16 kHz, linearly. Only used
// for test clips and voices; the microphone arrives at 16 kHz already.
func Resample(pcm []int16, rate int) []int16 {
	if rate == Rate || rate <= 0 {
		return pcm
	}
	n := int(int64(len(pcm)) * Rate / int64(rate))
	out := make([]int16, n)
	ratio := float64(rate) / Rate
	for i := range out {
		x := float64(i) * ratio
		j := int(x)
		if j+1 >= len(pcm) {
			out[i] = pcm[len(pcm)-1]
			continue
		}
		f := x - float64(j)
		out[i] = int16(float64(pcm[j])*(1-f) + float64(pcm[j+1])*f)
	}
	return out
}
