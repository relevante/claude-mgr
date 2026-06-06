package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadApps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	apps := map[string]string{"claude-id": "claude", "codex-id": "codex"}

	if err := Save(path, []string{"claude-id", "codex-id"}, apps, "codex-id", "bell"); err != nil {
		t.Fatal(err)
	}
	got := Load(path)
	if got.Shown != "codex-id" || got.Sound != "bell" {
		t.Fatalf("Shown/Sound=%q/%q, want codex-id/bell", got.Shown, got.Sound)
	}
	if got.App("codex-id") != "codex" {
		t.Fatalf("codex app=%q, want codex", got.App("codex-id"))
	}
	if got.App("claude-id") != "claude" {
		t.Fatalf("claude app=%q, want claude", got.App("claude-id"))
	}
}

func TestLoadDefaultsMissingAppToClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	raw := []byte(`{"version":1,"open":["old-id"],"shown":"old-id","sound":""}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load(path)
	if got.App("old-id") != "claude" {
		t.Fatalf("old app=%q, want claude", got.App("old-id"))
	}
}
