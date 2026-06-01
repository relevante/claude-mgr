package ui

import (
	"testing"
	"time"

	"claude-mgr/internal/index"
)

func TestShouldAdoptShown(t *testing.T) {
	cases := []struct {
		name          string
		actual, shown string
		pendingNew    bool
		want          bool
	}{
		{"orphaned pane (shown lost) adopts", "cd7af4d1", "", false, true},
		{"clear-style id change adopts", "newid", "oldid", false, true},
		{"failed-adoption placeholder adopts", "cd7af4d1", "new123", false, true},
		{"no session in pane does nothing", "", "oldid", false, false},
		{"already tracking it does nothing", "cd7af4d1", "cd7af4d1", false, false},
		{"mid new-session launch is skipped", "cd7af4d1", "", true, false},
	}
	for _, c := range cases {
		if got := shouldAdoptShown(c.actual, c.shown, c.pendingNew); got != c.want {
			t.Errorf("%s: shouldAdoptShown(%q,%q,%v)=%v, want %v", c.name, c.actual, c.shown, c.pendingNew, got, c.want)
		}
	}
}

func TestFindPendingNew(t *testing.T) {
	since := time.Unix(1_000_000, 0)
	after := since.Add(time.Second)
	before := since.Add(-time.Second)

	sess := func(id, cwd string, mtime, active time.Time) index.SessionMeta {
		return index.SessionMeta{SessionID: id, Cwd: cwd, FileMtime: mtime, LastActive: active}
	}
	const dir = "/Users/j/Dropbox/Travel Documents/2026-summer"

	t.Run("trailing slash on launch cwd still matches clean transcript cwd", func(t *testing.T) {
		all := []index.SessionMeta{sess("real", dir, after, after)}
		got, ok := findPendingNew(all, dir+"/", since) // tab-completion left a trailing slash
		if !ok || got.SessionID != "real" {
			t.Fatalf("want real, got %q ok=%v", got.SessionID, ok)
		}
	})

	t.Run("exact match", func(t *testing.T) {
		all := []index.SessionMeta{sess("real", dir, after, after)}
		if got, ok := findPendingNew(all, dir, since); !ok || got.SessionID != "real" {
			t.Fatalf("want real, got %q ok=%v", got.SessionID, ok)
		}
	})

	t.Run("different directory does not match", func(t *testing.T) {
		all := []index.SessionMeta{sess("other", "/somewhere/else", after, after)}
		if _, ok := findPendingNew(all, dir, since); ok {
			t.Fatal("should not match a different cwd")
		}
	})

	t.Run("transcript older than launch is ignored", func(t *testing.T) {
		all := []index.SessionMeta{sess("stale", dir, before, before)}
		if _, ok := findPendingNew(all, dir, since); ok {
			t.Fatal("should not match a transcript written before the launch")
		}
	})

	t.Run("latest activity wins among matches", func(t *testing.T) {
		all := []index.SessionMeta{
			sess("older", dir, after, after),
			sess("newer", dir, after, after.Add(time.Minute)),
		}
		if got, ok := findPendingNew(all, dir+"/", since); !ok || got.SessionID != "newer" {
			t.Fatalf("want newer, got %q ok=%v", got.SessionID, ok)
		}
	})
}
