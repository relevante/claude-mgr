package ui

import (
	"testing"
	"time"

	"claude-mgr/internal/index"
)

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
