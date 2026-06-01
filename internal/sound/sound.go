// Package sound plays the completion chime. The chime is selectable: a few
// subtle macOS system sounds plus a custom tone embedded in the binary. Playback
// is fire-and-forget via afplay — failures are silently ignored, and rapid calls
// are debounced so a burst of completions can't stack overlapping sounds.
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

// Choice is a selectable completion chime.
type Choice struct {
	Name string
	path string // afplay file path; "" = the embedded custom tone
}

// Choices is the cycle order the 'b' key steps through (the UI adds "off").
var Choices = []Choice{
	{"Tink", "/System/Library/Sounds/Tink.aiff"},
	{"Pop", "/System/Library/Sounds/Pop.aiff"},
	{"Submarine", "/System/Library/Sounds/Submarine.aiff"},
	{"Hero", "/System/Library/Sounds/Hero.aiff"},
	{"Glass", "/System/Library/Sounds/Glass.aiff"},
	{"Chime", ""}, // the embedded custom tone
}

// Next returns the next chime in the cycle: "" (off) → first → … → last → ""
// again. An unknown name resets to off.
func Next(current string) string {
	if current == "" {
		return Choices[0].Name
	}
	for i, c := range Choices {
		if c.Name == current {
			if i+1 < len(Choices) {
				return Choices[i+1].Name
			}
			break
		}
	}
	return ""
}

var (
	once     sync.Once
	embedded string
	mu       sync.Mutex
	lastPlay time.Time
)

// embeddedFile writes the embedded chime to a temp file once and returns it.
func embeddedFile() string {
	once.Do(func() {
		p := filepath.Join(os.TempDir(), "claude-mgr-chime.wav")
		if os.WriteFile(p, chime, 0o644) == nil {
			embedded = p
		}
	})
	return embedded
}

// pathFor resolves a choice name to an afplay file path; "" if unknown.
func pathFor(name string) string {
	for _, c := range Choices {
		if c.Name == name {
			if c.path == "" {
				return embeddedFile()
			}
			return c.path
		}
	}
	return ""
}

// Play sounds the named chime asynchronously. No-op for ""/unknown; debounced so
// calls within 250ms collapse to one.
func Play(name string) {
	p := pathFor(name)
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
