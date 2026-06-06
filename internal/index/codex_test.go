package index

import (
	"testing"
	"time"
)

func TestCodexRowsToMetaFiltersInteractiveCLIThreads(t *testing.T) {
	rows := []codexThreadRow{
		{
			ID:               "cli-user",
			RolloutPath:      "/tmp/cli-user.jsonl",
			Cwd:              "/Users/j/project",
			Title:            "Helpful title",
			FirstUserMessage: "first prompt",
			Preview:          "latest preview",
			UpdatedAtMS:      1_700_000_123_000,
			GitBranch:        "codex-branch",
			Archived:         1,
			Source:           "cli",
			ThreadSource:     "user",
		},
		{ID: "cli-empty-thread-source", Cwd: "/Users/j/other", Source: "cli"},
		{ID: "exec-session", Source: "exec", ThreadSource: "user"},
		{ID: "subagent-session", Source: "cli", ThreadSource: "subagent"},
		{ID: "guardian-session", Source: `{"type":"guardian"}`, ThreadSource: "user"},
		{ID: "", Source: "cli", ThreadSource: "user"},
	}

	got := codexRowsToMeta(rows)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	first := got[0]
	if first.SessionID != "cli-user" {
		t.Fatalf("SessionID=%q, want cli-user", first.SessionID)
	}
	if first.App != AppCodex || first.AppName() != AppCodex {
		t.Fatalf("App=%q AppName=%q, want codex", first.App, first.AppName())
	}
	if !first.Archived {
		t.Fatal("Archived=false, want true")
	}
	if first.AutoTitle != "Helpful title" {
		t.Fatalf("AutoTitle=%q, want Helpful title", first.AutoTitle)
	}
	if first.ProjectDir != "project" {
		t.Fatalf("ProjectDir=%q, want project", first.ProjectDir)
	}
	wantTime := time.UnixMilli(1_700_000_123_000)
	if !first.LastActive.Equal(wantTime) {
		t.Fatalf("LastActive=%v, want %v", first.LastActive, wantTime)
	}
	if got[1].SessionID != "cli-empty-thread-source" {
		t.Fatalf("second SessionID=%q, want cli-empty-thread-source", got[1].SessionID)
	}
}

func TestCodexRowsToMetaTitleFallbackAndCreatedTime(t *testing.T) {
	cases := []struct {
		name string
		row  codexThreadRow
		want string
	}{
		{
			name: "preview fallback",
			row:  codexThreadRow{ID: "a", Preview: "latest preview", Source: "cli"},
			want: "latest preview",
		},
		{
			name: "first user fallback",
			row:  codexThreadRow{ID: "b", FirstUserMessage: "first prompt", Source: "cli"},
			want: "first prompt",
		},
		{
			name: "short id fallback",
			row:  codexThreadRow{ID: "1234567890", Source: "cli"},
			want: "(12345678)",
		},
	}

	for _, c := range cases {
		c.row.CreatedAtMS = 1_700_000_000_000
		got := codexRowsToMeta([]codexThreadRow{c.row})
		if len(got) != 1 {
			t.Fatalf("%s: len=%d, want 1", c.name, len(got))
		}
		if got[0].AutoTitle != c.want {
			t.Errorf("%s: AutoTitle=%q, want %q", c.name, got[0].AutoTitle, c.want)
		}
		if !got[0].LastActive.Equal(time.UnixMilli(c.row.CreatedAtMS)) {
			t.Errorf("%s: LastActive=%v, want created time", c.name, got[0].LastActive)
		}
	}
}

func TestSessionMetaAppNameDefaultsClaude(t *testing.T) {
	if got := (SessionMeta{}).AppName(); got != AppClaude {
		t.Fatalf("AppName()=%q, want %q", got, AppClaude)
	}
}
