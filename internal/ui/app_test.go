package ui

import (
	"path/filepath"
	"testing"
	"time"

	"claude-mgr/internal/index"
)

// Reaping is time-based: a session missing from the pane snapshot is only
// dropped after it's been gone for reapAfter, so fs-event poll bursts (the
// Codex WAL can fire polls milliseconds apart) can't reap a healthy session
// that was merely mid window-switch.
func TestReconcileLiveReapsByTimeNotPollCount(t *testing.T) {
	// A session already observed alive this run (the precondition for reaping).
	newModel := func() *Model {
		return &Model{
			wsPath:      filepath.Join(t.TempDir(), "ws.json"),
			openIDs:     map[string]bool{"aaaaaaaa-1111": true},
			appByID:     map[string]string{},
			seenLive:    map[string]bool{"aaaaaaaa-1111": true},
			statusByID8: map[string]index.Status{}, // now missing from panes
		}
	}

	// Burst: first miss records the time; an immediate second poll must NOT reap.
	m := newModel()
	if m.reconcileLive() {
		t.Fatal("first miss must not reap")
	}
	if m.reconcileLive() {
		t.Fatal("second miss within the burst window must not reap")
	}
	if !m.openIDs["aaaaaaaa-1111"] {
		t.Fatal("session dropped from openIDs during a poll burst")
	}

	// Sustained absence after being seen: a miss older than reapAfter reaps.
	m = newModel()
	m.liveMiss = map[string]time.Time{"aaaaaaaa-1111": time.Now().Add(-reapAfter - time.Second)}
	if !m.reconcileLive() {
		t.Fatal("sustained absence of a seen session must reap")
	}
	if m.openIDs["aaaaaaaa-1111"] {
		t.Fatal("session still open after sustained absence")
	}

	// Reappearing clears the miss clock.
	m = newModel()
	m.liveMiss = map[string]time.Time{"aaaaaaaa-1111": time.Now().Add(-time.Hour)}
	m.statusByID8 = map[string]index.Status{"aaaaaaaa": index.StatusIdle} // claude key = id8
	if m.reconcileLive() {
		t.Fatal("present session must not reap")
	}
	if _, stale := m.liveMiss["aaaaaaaa-1111"]; stale {
		t.Fatal("miss clock not cleared when the session reappeared")
	}
}

// The core data-loss guard: a session that was NEVER observed alive (a
// key mismatch, scan gap, or a poll that raced window creation) must never be
// reaped, no matter how long it's been "absent" — reaping it would silently
// drop a healthy session from the persisted workspace.
func TestReconcileLiveNeverReapsUnseenSession(t *testing.T) {
	m := &Model{
		wsPath:      filepath.Join(t.TempDir(), "ws.json"),
		openIDs:     map[string]bool{"bbbbbbbb-2222": true},
		appByID:     map[string]string{},
		seenLive:    map[string]bool{}, // never seen
		statusByID8: map[string]index.Status{},
		// Even with an ancient miss timestamp, the unseen guard wins.
		liveMiss: map[string]time.Time{"bbbbbbbb-2222": time.Now().Add(-time.Hour)},
	}
	if m.reconcileLive() {
		t.Fatal("reaped a session that was never seen alive")
	}
	if !m.openIDs["bbbbbbbb-2222"] {
		t.Fatal("unseen session dropped from openIDs — workspace would shrink")
	}
}

// appForID must be sticky: a scan that transiently misses a session (locked
// Codex DB) cannot flip its app to claude — that would derail its tmux session
// key and get it reaped while its pane is alive.
func TestAppForIDSticksAcrossScanGaps(t *testing.T) {
	m := &Model{appByID: map[string]string{"019e-codex": index.AppCodex}}
	m.all = nil // the scan lost it
	if got := m.appForID("019e-codex"); got != index.AppCodex {
		t.Fatalf("appForID=%q after scan gap, want codex", got)
	}
	// Unknown ids still default to claude.
	if got := m.appForID("unknown"); got != index.AppClaude {
		t.Fatalf("appForID(unknown)=%q, want claude", got)
	}
}
