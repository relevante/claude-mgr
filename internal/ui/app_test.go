package ui

import (
	"path/filepath"
	"testing"
	"time"

	"claude-mgr/internal/index"
	"claude-mgr/internal/tmux"
	"claude-mgr/internal/workspace"
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

// planRestore must TRACK every on-disk saved session (so the persisted
// workspace never shrinks), while the cap and live-elsewhere checks only gate
// relaunching. Sessions alive in our own tmux (respawn survivors) are adopted,
// not relaunched, and never mistaken for another terminal.
func TestPlanRestore(t *testing.T) {
	meta := func(id string) index.SessionMeta {
		return index.SessionMeta{SessionID: id, Cwd: "/w/" + id, App: index.AppClaude}
	}
	saved := workspace.State{Open: []string{"aaaa", "bbbb", "cccc", "dddd", "gone"}}
	byID := map[string]index.SessionMeta{
		"aaaa": meta("aaaa"), "bbbb": meta("bbbb"),
		"cccc": meta("cccc"), "dddd": meta("dddd"),
		// "gone" intentionally absent from disk
	}
	ours := map[string]bool{tmux.SessionKey("aaaa", index.AppClaude): true}   // respawn survivor
	liveElsewhere := map[string]bool{"bbbb": true}                            // another terminal
	p := planRestore(saved, byID, ours, liveElsewhere, map[string]bool{}, 40)

	// Every on-disk saved session is tracked; "gone" is dropped.
	gotTrack := map[string]bool{}
	for _, id := range p.track {
		gotTrack[id] = true
	}
	for _, id := range []string{"aaaa", "bbbb", "cccc", "dddd"} {
		if !gotTrack[id] {
			t.Errorf("track missing %s", id)
		}
	}
	if gotTrack["gone"] {
		t.Error("tracked a session that's gone from disk")
	}
	// aaaa (ours) is marked seen, not relaunched.
	if len(p.seen) != 1 || p.seen[0] != "aaaa" {
		t.Errorf("seen=%v, want [aaaa]", p.seen)
	}
	// Relaunch = cccc and dddd only (aaaa is ours, bbbb is elsewhere).
	got := map[string]bool{}
	for _, r := range p.relaunch {
		got[r.ID] = true
	}
	if got["aaaa"] || got["bbbb"] || !got["cccc"] || !got["dddd"] {
		t.Errorf("relaunch=%v, want {cccc,dddd}", got)
	}
}

// The cap bounds relaunches but never tracking: with more saved sessions than
// the cap, all are still tracked (persisted), only the cap-count relaunch.
func TestPlanRestoreCapBoundsRelaunchNotTracking(t *testing.T) {
	saved := workspace.State{}
	byID := map[string]index.SessionMeta{}
	for i := 0; i < 5; i++ {
		id := string(rune('a'+i)) + "xxx"
		saved.Open = append(saved.Open, id)
		byID[id] = index.SessionMeta{SessionID: id, Cwd: "/w/" + id, App: index.AppClaude}
	}
	p := planRestore(saved, byID, map[string]bool{}, map[string]bool{}, map[string]bool{}, 2)
	if len(p.track) != 5 {
		t.Fatalf("track=%d, want all 5 saved sessions", len(p.track))
	}
	if len(p.relaunch) != 2 {
		t.Fatalf("relaunch=%d, want cap of 2", len(p.relaunch))
	}
}

func TestPollStillCurrent(t *testing.T) {
	if !pollStillCurrent("X", "X") {
		t.Error("same shown should be current")
	}
	if pollStillCurrent("X", "Y") {
		t.Error("a poll from before a switch (X) must be stale once shown is Y")
	}
	if !pollStillCurrent("", "") {
		t.Error("empty/empty (orphan recovery) should be current")
	}
}
