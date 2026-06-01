// Package workspace persists which sessions are open in the dashboard so they
// can be rebuilt after a reboot (or a quit). Because Claude sessions are
// disk-resumable, "restoring a workspace" just means relaunching `claude
// --resume <id>` for each remembered thread — no external tmux plugins needed.
package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const version = 1

type State struct {
	Version int      `json:"version"`
	Open    []string `json:"open"`    // session ids open in the dashboard
	Shown   string   `json:"shown"`   // the one displayed on the right
	SoundOn bool     `json:"soundOn"` // completion chime enabled (global toggle)
}

// DefaultPath returns ~/.config/claude-mgr/workspace.json (honoring overrides).
func DefaultPath() string {
	if p := os.Getenv("CLAUDE_MGR_WORKSPACE"); p != "" {
		return p
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "claude-mgr", "workspace.json")
}

// Load reads the saved workspace (empty if absent/corrupt).
func Load(path string) State {
	s := State{Version: version}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var loaded State
	if json.Unmarshal(raw, &loaded) == nil && loaded.Version == version {
		return loaded
	}
	return s
}

// Save writes the workspace atomically.
func Save(path string, open []string, shown string, soundOn bool) error {
	s := State{Version: version, Open: open, Shown: shown, SoundOn: soundOn}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
