package sound

import (
	"os"
	"testing"
)

// TestEmbeddedChimeIsValidWav verifies the embedded chime materializes to a temp
// file as a real WAV — i.e. the embed→file path afplay will read actually works.
// It does not play audio (tests stay quiet).
func TestEmbeddedChimeIsValidWav(t *testing.T) {
	p := soundFile()
	if p == "" {
		t.Fatal("soundFile() produced no path")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading temp chime: %v", err)
	}
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("temp chime is not a valid WAV (len=%d)", len(b))
	}
}
