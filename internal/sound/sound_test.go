package sound

import (
	"os"
	"testing"
)

// TestEmbeddedChimeIsValidWav verifies the embedded chime materializes to a temp
// file as a real WAV — i.e. the embed→file path afplay will read actually works.
func TestEmbeddedChimeIsValidWav(t *testing.T) {
	p := embeddedFile()
	if p == "" {
		t.Fatal("embeddedFile() produced no path")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading temp chime: %v", err)
	}
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("temp chime is not a valid WAV (len=%d)", len(b))
	}
}

// TestNextCycles checks the 'b' cycle: off → each choice → off.
func TestNextCycles(t *testing.T) {
	cur := "" // off
	seen := []string{}
	for i := 0; i < len(Choices); i++ {
		cur = Next(cur)
		if cur == "" {
			t.Fatalf("hit off early at step %d", i)
		}
		seen = append(seen, cur)
	}
	if len(seen) != len(Choices) {
		t.Fatalf("cycled %d sounds, want %d", len(seen), len(Choices))
	}
	if got := Next(cur); got != "" {
		t.Fatalf("after the last choice, want off, got %q", got)
	}
	if got := Next(""); got != Choices[0].Name {
		t.Fatalf("off should advance to first choice %q, got %q", Choices[0].Name, got)
	}
	if got := Next("NotARealSound"); got != "" {
		t.Fatalf("unknown should reset to off, got %q", got)
	}
}
