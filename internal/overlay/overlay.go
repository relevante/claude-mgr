// Package overlay stores the dashboard's own per-session metadata — custom
// names, pins, and archive flags — keyed by stable sessionId. Claude does not
// persist renames in a recoverable way, so the dashboard owns names: a custom
// name wins over Claude's auto-title, and renaming works for every session,
// even ones Claude never titled.
package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const version = 1

type data struct {
	Version  int               `json:"version"`
	Names    map[string]string `json:"names"`
	Pinned   map[string]bool   `json:"pinned"`
	Archived map[string]bool   `json:"archived"`
}

// Overlay is safe for concurrent use.
type Overlay struct {
	mu   sync.RWMutex
	d    data
	path string
}

// DefaultPath returns ~/.config/claude-mgr/overlay.json, honoring
// CLAUDE_MGR_CONFIG and XDG_CONFIG_HOME.
func DefaultPath() string {
	if p := os.Getenv("CLAUDE_MGR_OVERLAY"); p != "" {
		return p
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "claude-mgr", "overlay.json")
}

// Load reads the overlay file, returning an empty overlay if absent/corrupt.
func Load(path string) *Overlay {
	o := &Overlay{path: path, d: data{Version: version, Names: map[string]string{}, Pinned: map[string]bool{}, Archived: map[string]bool{}}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return o
	}
	var loaded data
	if json.Unmarshal(raw, &loaded) == nil && loaded.Version == version {
		if loaded.Names != nil {
			o.d.Names = loaded.Names
		}
		if loaded.Pinned != nil {
			o.d.Pinned = loaded.Pinned
		}
		if loaded.Archived != nil {
			o.d.Archived = loaded.Archived
		}
	}
	return o
}

func (o *Overlay) Name(id string) (string, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	n, ok := o.d.Names[id]
	return n, ok && n != ""
}

func (o *Overlay) IsPinned(id string) bool { o.mu.RLock(); defer o.mu.RUnlock(); return o.d.Pinned[id] }
func (o *Overlay) IsArchived(id string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.d.Archived[id]
}

// SetName sets (or clears, when name=="") a custom name and persists.
func (o *Overlay) SetName(id, name string) error {
	o.mu.Lock()
	if name == "" {
		delete(o.d.Names, id)
	} else {
		o.d.Names[id] = name
	}
	o.mu.Unlock()
	return o.save()
}

func (o *Overlay) TogglePinned(id string) error   { return o.toggle(o.d.Pinned, id) }
func (o *Overlay) ToggleArchived(id string) error { return o.toggle(o.d.Archived, id) }

func (o *Overlay) toggle(m map[string]bool, id string) error {
	o.mu.Lock()
	if m[id] {
		delete(m, id)
	} else {
		m[id] = true
	}
	o.mu.Unlock()
	return o.save()
}

// save writes the overlay atomically (temp file + rename).
func (o *Overlay) save() error {
	o.mu.RLock()
	o.d.Version = version
	raw, err := json.MarshalIndent(o.d, "", "  ")
	o.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o755); err != nil {
		return err
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, o.path)
}
