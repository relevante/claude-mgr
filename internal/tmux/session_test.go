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

func TestSessionKey(t *testing.T) {
	claudeID := "216220b1-c0c6-4db8-aab7-445f242307f1"
	if got := SessionKey(claudeID, "claude"); got != "216220b1" {
		t.Fatalf("Claude key=%q, want 216220b1", got)
	}

	first := "019e9a7e-7b19-7f11-8d23-56038b8a7283"
	second := "019e9a7e-b70c-7622-ae88-1f6b5d05b030"
	if got := SessionKey(first, "codex"); got != "019e9a7e7b197f118d2356038b8a7283" {
		t.Fatalf("Codex key=%q", got)
	}
	if SessionKey(first, "codex") == SessionKey(second, "codex") {
		t.Fatal("Codex keys collided for ids sharing the first 8 chars")
	}
}

func TestConfigureSetsCopyCommand(t *testing.T) {
	for _, cmd := range configureCommands() {
		if len(cmd) == 4 && cmd[0] == "set-option" && cmd[1] == "-s" &&
			cmd[2] == "copy-command" && cmd[3] == "pbcopy" {
			return
		}
	}
	t.Fatal("configureCommands missing server copy-command pbcopy")
}
