package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitSignal reports whether the watcher fired within the timeout.
func waitSignal(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// drain removes any already-pending signal so the next wait is fresh.
func drain(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// TestRegistryInPlaceRewrite is the load-bearing check: Claude rewrites
// <pid>.json IN PLACE (same inode), and on macOS a kqueue directory watch does
// NOT report that. This verifies the per-file watch DOES — i.e. status flips are
// actually observable as events, not just create/delete.
func TestRegistryInPlaceRewrite(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	defer r.Close()

	pid := filepath.Join(dir, "1234.json")

	// Create the pid file (the dir watch should see this and arm a file watch).
	if err := os.WriteFile(pid, []byte(`{"status":"idle"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(r.Events(), 3*time.Second) {
		t.Fatal("no signal on create")
	}
	drain(r.Events())

	// Rewrite it in place (same path → same inode, exactly Claude's idle→busy).
	// This is the event a directory-only kqueue watch would miss.
	for i, body := range []string{`{"status":"busy"}`, `{"status":"waiting"}`, `{"status":"idle"}`} {
		if err := os.WriteFile(pid, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if !waitSignal(r.Events(), 3*time.Second) {
			t.Fatalf("no signal on in-place rewrite #%d (%s)", i, body)
		}
		drain(r.Events())
	}

	// Removal (session ended) should also signal.
	if err := os.Remove(pid); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(r.Events(), 3*time.Second) {
		t.Fatal("no signal on remove")
	}
}

// TestRegistryCoalesces confirms a burst collapses to a single pending wake-up
// (buffered(1)), so the consumer isn't flooded.
func TestRegistryCoalesces(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	defer r.Close()

	// Fire several signals with no consumer; the channel should hold exactly one.
	for i := 0; i < 5; i++ {
		r.signal()
	}
	if !waitSignal(r.Events(), time.Second) {
		t.Fatal("expected one pending signal")
	}
	if waitSignal(r.Events(), 100*time.Millisecond) {
		t.Fatal("expected the burst to coalesce to a single signal")
	}
}
