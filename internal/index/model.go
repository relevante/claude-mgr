// Package index discovers Claude Code sessions on disk and maintains a cheap,
// incrementally-refreshed in-memory index of them.
//
// Ground truth lives at ~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl, one
// JSON object per line. Files can be large (tens of MB), so metadata is
// extracted by reading only the file tail (and, rarely, a bounded head), never
// the whole file. See spike/FINDINGS.md and the plan for the verified facts
// this relies on.
package index

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Status is a live session's current activity, derived elsewhere (Phase 3).
// Dormant sessions are always StatusIdle.
type Status int

const (
	StatusIdle Status = iota // open but quiet, or not live
	StatusWorking
	StatusWaiting // waiting for user input
	StatusPermission
)

func (s Status) Dot() string {
	switch s {
	case StatusWorking:
		return "●"
	case StatusWaiting:
		return "◐"
	case StatusPermission:
		return "⚠"
	default:
		return "○"
	}
}

// SessionMeta is everything the dashboard needs to display and re-enter one
// session. Cheap fields come from a tail read; AutoTitle/Cwd may require a
// one-time head read when absent from the tail.
type SessionMeta struct {
	SessionID  string    // stable across resumes; the overlay/rename key
	Path       string    // absolute path to the .jsonl
	ProjectDir string    // encoded dir name under ~/.claude/projects (index key)
	Cwd        string    // authoritative working dir, read from the transcript
	GitBranch  string    // last-seen git branch (informational)
	LastActive time.Time // max(timestamp) seen in the tail window

	// Title components, in priority order. AutoTitle is the resolved default
	// (overlay names, applied later, take precedence over AutoTitle).
	AiTitle      string
	LastPrompt   string
	FirstUserMsg string
	AutoTitle    string

	// File identity, used for (path,mtime,size) cache invalidation.
	FileSize  int64
	FileMtime time.Time

	// Runtime-only (not cached): set by the live/status layers.
	Live   bool
	Status Status
}

// resolveAutoTitle picks the best non-custom display name: aiTitle, else last
// prompt, else first user message, else a short id.
func (m *SessionMeta) resolveAutoTitle() {
	switch {
	case strings.TrimSpace(m.AiTitle) != "":
		m.AutoTitle = oneLine(m.AiTitle, 80)
	case strings.TrimSpace(m.LastPrompt) != "":
		m.AutoTitle = oneLine(m.LastPrompt, 80)
	case strings.TrimSpace(m.FirstUserMsg) != "":
		m.AutoTitle = oneLine(m.FirstUserMsg, 80)
	default:
		m.AutoTitle = "(" + shortID(m.SessionID) + ")"
	}
}

// IsEmpty reports a contentless stub session — no auto-title, last prompt, or
// real first user message (typically an artifact of /clear or an abandoned
// launch). The dashboard hides these by default.
func (m *SessionMeta) IsEmpty() bool {
	return strings.TrimSpace(m.AiTitle) == "" &&
		strings.TrimSpace(m.LastPrompt) == "" &&
		strings.TrimSpace(m.FirstUserMsg) == ""
}

// ProjectLabel is a short, human-friendly grouping label for the session's
// project, derived from the authoritative cwd when available.
func (m *SessionMeta) ProjectLabel() string {
	cwd := m.Cwd
	if cwd == "" {
		cwd = decodeProjectDir(m.ProjectDir)
	}
	return shortPath(cwd)
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// oneLine collapses whitespace/newlines and truncates to n runes with an ellipsis.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// shortPath returns the last two path segments, e.g. "sensorpush/sensor-esp".
func shortPath(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "?"
	}
	base := filepath.Base(p)
	parent := filepath.Base(filepath.Dir(p))
	if parent == "" || parent == "." || parent == "/" {
		return base
	}
	return parent + "/" + base
}

// decodeProjectDir reverses the lossy "/"->"-" encoding for use only as a
// fallback label (never for resume — read cwd from the transcript for that).
func decodeProjectDir(dir string) string {
	return strings.ReplaceAll(dir, "-", "/")
}

// RelTime renders a compact "time ago" string relative to now.
func RelTime(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
