package ui

import (
	"path/filepath"
	"testing"
	"time"

	"claude-mgr/internal/index"
	"claude-mgr/internal/overlay"
)

func TestChimeForTransition(t *testing.T) {
	W, I, P, S := index.StatusWorking, index.StatusIdle, index.StatusPermission, index.StatusShell
	cases := []struct {
		name                   string
		prev, next             index.Status
		isShown, focused, want bool
	}{
		{"background working→idle chimes", W, I, false, true, true},
		{"background working→permission chimes", W, P, false, true, true},
		{"viewed + focused stays silent", W, I, true, true, false},
		{"viewed but window unfocused chimes", W, I, true, false, true},
		{"still working: no chime", W, W, false, true, false},
		{"was idle (no transition): no chime", I, I, false, true, false},
		{"idle→working (started): no chime", I, W, false, true, false},
		{"handoff to background shell: no chime", W, S, false, true, false},
		{"background shell finishes → idle chimes", S, I, false, true, true},
		{"shell wakes claude (shell→working): no chime", S, W, false, true, false},
	}
	for _, c := range cases {
		if got := chimeForTransition(c.prev, c.next, c.isShown, c.focused); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

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
		got, ok := findPendingNew(all, dir+"/", since, index.AppClaude) // tab-completion left a trailing slash
		if !ok || got.SessionID != "real" {
			t.Fatalf("want real, got %q ok=%v", got.SessionID, ok)
		}
	})

	t.Run("exact match", func(t *testing.T) {
		all := []index.SessionMeta{sess("real", dir, after, after)}
		if got, ok := findPendingNew(all, dir, since, index.AppClaude); !ok || got.SessionID != "real" {
			t.Fatalf("want real, got %q ok=%v", got.SessionID, ok)
		}
	})

	t.Run("different directory does not match", func(t *testing.T) {
		all := []index.SessionMeta{sess("other", "/somewhere/else", after, after)}
		if _, ok := findPendingNew(all, dir, since, index.AppClaude); ok {
			t.Fatal("should not match a different cwd")
		}
	})

	t.Run("transcript older than launch is ignored", func(t *testing.T) {
		all := []index.SessionMeta{sess("stale", dir, before, before)}
		if _, ok := findPendingNew(all, dir, since, index.AppClaude); ok {
			t.Fatal("should not match a transcript written before the launch")
		}
	})

	t.Run("latest activity wins among matches", func(t *testing.T) {
		all := []index.SessionMeta{
			sess("older", dir, after, after),
			sess("newer", dir, after, after.Add(time.Minute)),
		}
		if got, ok := findPendingNew(all, dir+"/", since, index.AppClaude); !ok || got.SessionID != "newer" {
			t.Fatalf("want newer, got %q ok=%v", got.SessionID, ok)
		}
	})

	t.Run("matching app wins when cwd is shared", func(t *testing.T) {
		all := []index.SessionMeta{
			{SessionID: "claude", Cwd: dir, FileMtime: after, LastActive: after, App: index.AppClaude},
			{SessionID: "codex", Cwd: dir, FileMtime: after, LastActive: after.Add(time.Minute), App: index.AppCodex},
		}
		if got, ok := findPendingNew(all, dir, since, index.AppCodex); !ok || got.SessionID != "codex" {
			t.Fatalf("want codex, got %q ok=%v", got.SessionID, ok)
		}
		if got, ok := findPendingNew(all, dir, since, index.AppClaude); !ok || got.SessionID != "claude" {
			t.Fatalf("want claude, got %q ok=%v", got.SessionID, ok)
		}
	})
}

func TestVisibleHidesCodexArchived(t *testing.T) {
	m := &Model{ov: overlay.Load(filepath.Join(t.TempDir(), "overlay.json"))}
	s := index.SessionMeta{
		SessionID: "codex-archived",
		App:       index.AppCodex,
		Archived:  true,
	}
	if m.visible(s) {
		t.Fatal("visible=true for archived Codex session, want false")
	}
	m.showArchived = true
	if !m.visible(s) {
		t.Fatal("visible=false with showArchived=true, want true")
	}
}

func TestNewPromptAppToggle(t *testing.T) {
	cases := []struct {
		name       string
		start      string
		wantNext   string
		wantPrompt string
	}{
		{"default to codex", "", index.AppCodex, "new ✳ in: "},
		{"claude to codex", index.AppClaude, index.AppCodex, "new ✳ in: "},
		{"codex to claude", index.AppCodex, index.AppClaude, "new ⬡ in: "},
	}
	for _, c := range cases {
		if got := toggleApp(c.start); got != c.wantNext {
			t.Errorf("%s: toggleApp=%q, want %q", c.name, got, c.wantNext)
		}
		if got := newPrompt(c.start); got != c.wantPrompt {
			t.Errorf("%s: newPrompt=%q, want %q", c.name, got, c.wantPrompt)
		}
	}
}
