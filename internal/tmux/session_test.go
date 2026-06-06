package tmux

import "testing"

func TestSessionCmd(t *testing.T) {
	cases := []struct {
		name string
		ref  SessionRef
		want string
	}{
		{"default app is Claude", SessionRef{ID: "abc123"}, "exec claude --resume abc123"},
		{"explicit Claude", SessionRef{ID: "abc123", App: "claude"}, "exec claude --resume abc123"},
		{"Codex", SessionRef{ID: "abc123", App: "codex"}, "exec codex resume abc123"},
	}
	for _, c := range cases {
		if got := sessionCmd(c.ref); got != c.want {
			t.Errorf("%s: sessionCmd=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestNewSessionCmd(t *testing.T) {
	cases := []struct {
		name string
		app  string
		want string
	}{
		{"default app is Claude", "", "exec claude"},
		{"explicit Claude", "claude", "exec claude"},
		{"Codex", "codex", "exec codex"},
	}
	for _, c := range cases {
		if got := newSessionCmd(c.app); got != c.want {
			t.Errorf("%s: newSessionCmd=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestAgentCommandOverrides(t *testing.T) {
	t.Setenv("CLAUDE_MGR_CLAUDE_CMD", "claude-test {id}")
	t.Setenv("CLAUDE_MGR_CODEX_CMD", "codex-test {id}")

	if got := sessionCmd(SessionRef{ID: "abc123"}); got != "claude-test abc123" {
		t.Fatalf("Claude resume override=%q", got)
	}
	if got := sessionCmd(SessionRef{ID: "abc123", App: "codex"}); got != "codex-test abc123" {
		t.Fatalf("Codex resume override=%q", got)
	}
	if got := newSessionCmd(""); got != "claude-test new" {
		t.Fatalf("Claude new override=%q", got)
	}
	if got := newSessionCmd("codex"); got != "codex-test new" {
		t.Fatalf("Codex new override=%q", got)
	}
}
