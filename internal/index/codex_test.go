package index

import (
	"os"
	"path/filepath"
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

func TestExtractCodexUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":1000},"model_context_window":200000}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":2500},"model_context_window":258400}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	m := SessionMeta{Path: path, FileSize: fi.Size()}
	extractCodexUsage(&m)
	if m.ContextTokens != 2500 {
		t.Fatalf("ContextTokens=%d, want 2500", m.ContextTokens)
	}
	if m.ContextLimit != 258400 {
		t.Fatalf("ContextLimit=%d, want 258400", m.ContextLimit)
	}
}

// A locked Codex state DB (sqlite exits non-zero) must not wipe Codex rows
// from the scan — Scan reuses the last good snapshot instead.
func TestScanReusesLastCodexOnQueryFailure(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(state, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fake sqlite3: succeeds with one thread row until the fail-flag appears.
	failFlag := filepath.Join(dir, "fail")
	script := filepath.Join(dir, "sqlite3")
	rows := `[{"id":"codex-1","rollout_path":"","cwd":"/w/p","title":"T","first_user_message":"f","preview":"p","tokens_used":0,"created_at_ms":1,"updated_at_ms":2,"git_branch":"","archived":0,"source":"cli","thread_source":"user"}]`
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ -e "+failFlag+" ] && exit 5\necho '"+rows+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{
		ProjectsDir:    filepath.Join(dir, "projects"), // empty: no claude sessions
		CachePath:      filepath.Join(dir, "cache.json"),
		CodexStatePath: state,
		SQLitePath:     script,
	}

	got, err := s.Scan()
	if err != nil || len(got) != 1 || got[0].SessionID != "codex-1" {
		t.Fatalf("healthy scan: got %d rows err=%v, want the codex row", len(got), err)
	}

	if err := os.WriteFile(failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = s.Scan()
	if err != nil {
		t.Fatalf("degraded scan errored: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "codex-1" {
		t.Fatalf("degraded scan: got %d rows, want last good codex row reused", len(got))
	}
}
