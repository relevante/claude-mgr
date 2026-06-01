// Package sound plays a short completion chime. The chime is a custom, subtle
// tone embedded in the binary so it travels with the app and needs no external
// asset; CLAUDE_MGR_SOUND overrides it with any afplay-readable file. Playback
// is fire-and-forget via macOS afplay — failures are silently ignored, and rapid
// calls are debounced so a burst of completions can't stack overlapping sounds.
package sound

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

//go:embed chime.wav
var chime []byte

var (
	once     sync.Once
	file     string
	mu       sync.Mutex
	lastPlay time.Time
)

// soundFile returns the path to play: a user override, else the embedded chime
// written to a temp file once. "" if it can't be made available.
func soundFile() string {
	if p := os.Getenv("CLAUDE_MGR_SOUND"); p != "" {
		return p
	}
	once.Do(func() {
		p := filepath.Join(os.TempDir(), "claude-mgr-chime.wav")
		if err := os.WriteFile(p, chime, 0o644); err == nil {
			file = p
		}
	})
	return file
}

// Play sounds the chime asynchronously. No-op if unavailable; debounced so calls
// within 250ms collapse to one.
func Play() {
	p := soundFile()
	if p == "" {
		return
	}
	mu.Lock()
	if time.Since(lastPlay) < 250*time.Millisecond {
		mu.Unlock()
		return
	}
	lastPlay = time.Now()
	mu.Unlock()
	go func() { _ = exec.Command("afplay", p).Run() }()
}
