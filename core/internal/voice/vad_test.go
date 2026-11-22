package voice

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The real detector, on this computer's own voice. Skipped where Silero
// or the voice environment is not set up.
func TestSileroHearsSpeechAndNotQuiet(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "Application Support", "Cosmos", "voice")
	say, err := exec.LookPath("say")
	if VADModel() == "" || !exists(filepath.Join(dir, ".venv", "bin", "python")) || err != nil {
		t.Skip("silero, its environment or the say command is not here")
	}
	wav := filepath.Join(t.TempDir(), "said.wav")
	if out, err := exec.Command(say, "-o", wav, "--data-format=LEI16@16000", "Clarke, what is the weather like tomorrow morning?").CombinedOutput(); err != nil {
		t.Skipf("say: %v %s", err, out)
	}
	raw, _ := os.ReadFile(wav)
	speech, err := PCM(raw)
	if err != nil {
		t.Fatal(err)
	}
	d := OpenDetector(dir)
	defer d.Close()
	if d.Kind() != "silero" {
		t.Fatalf("opened %s, want silero", d.Kind())
	}
	quiet := make([]int16, Rate)
	all := append(append(append([]int16{}, quiet...), speech...), quiet...)
	all = append(all, quiet...)
	s := &Segmenter{}
	var got []Heard
	for i := 0; i+Frame <= len(all); i += Frame {
		p, err := d.Speech(all[i : i+Frame])
		if err != nil {
			t.Fatal(err)
		}
		if h, _ := s.Push(all[i:i+Frame], p, false); h != Nothing {
			got = append(got, h)
		}
	}
	if len(got) < 3 || got[0] != Started || got[len(got)-1] != Finished {
		t.Fatalf("heard %v, want it started and finished", got)
	}
}
