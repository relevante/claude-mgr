package status

import (
	"testing"

	"claude-mgr/internal/index"
)

// Snippets captured from claude 2.1.158 during the Phase 0 spike.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		text string
		want index.Status
	}{
		{
			name: "working",
			text: "⏺ pong\n✻ Worked for 1s\n──────────\n❯\n──────────\n  esc to interrupt",
			want: index.StatusWorking,
		},
		{
			name: "plain prompt is idle, not waiting",
			text: "──────────\n❯ Try \"create a util\"\n──────────\n  ? for shortcuts · ← for agents",
			want: index.StatusIdle,
		},
		{
			name: "auto-mode prompt is idle",
			text: "⏺ Ready — send the path.\n❯\n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents",
			want: index.StatusIdle,
		},
		{
			name: "permission",
			text: " ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel",
			want: index.StatusPermission,
		},
		{
			name: "numbered list in prose is NOT permission",
			text: "Here are options:\n  1. flat rail\n  2. beveled rail\n❯\n  ? for shortcuts · ← for agents",
			want: index.StatusIdle,
		},
		{
			name: "idle",
			text: "some old transcript output with no hint line visible",
			want: index.StatusIdle,
		},
		{
			name: "working beats permission when both present",
			text: "Enter to confirm\n esc to interrupt",
			want: index.StatusWorking,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.text); got != c.want {
				t.Fatalf("Classify(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// Verified against claude 2.1.159 (busy/waiting/idle) and 2.1.162 (shell):
// the pid registry self-reports these.
func TestFromRegistry(t *testing.T) {
	cases := []struct {
		in   string
		want index.Status
	}{
		{"busy", index.StatusWorking},
		{"waiting", index.StatusWaiting},
		{"shell", index.StatusShell},
		{"idle", index.StatusIdle},
		{"", index.StatusIdle},
		{"something-unknown", index.StatusIdle},
	}
	for _, c := range cases {
		if got := FromRegistry(c.in); got != c.want {
			t.Fatalf("FromRegistry(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

const permDialog = " ❯ 1. Yes\n   2. No\n Do you want to proceed?"

// Resolve prefers the registry flag and refines waiting→permission via the pane.
func TestResolve(t *testing.T) {
	cases := []struct {
		name     string
		reg      index.Status
		regKnown bool
		pane     string
		want     index.Status
	}{
		{"registry busy wins over silent pane", index.StatusWorking, true, "idle prose", index.StatusWorking},
		{"registry idle wins over noisy pane", index.StatusIdle, true, "esc to interrupt", index.StatusIdle},
		{"registry waiting, plain pane stays waiting", index.StatusWaiting, true, "❯ Try something", index.StatusWaiting},
		{"registry waiting + permission dialog upgrades to ⚠", index.StatusWaiting, true, permDialog, index.StatusPermission},
		{"no registry falls back to pane: working", index.StatusIdle, false, "esc to interrupt", index.StatusWorking},
		{"no registry falls back to pane: permission", index.StatusIdle, false, permDialog, index.StatusPermission},
		{"no registry falls back to pane: idle", index.StatusIdle, false, "❯ Try something", index.StatusIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Resolve(c.reg, c.regKnown, c.pane); got != c.want {
				t.Fatalf("Resolve(%v,%v,pane) = %v, want %v", c.reg, c.regKnown, got, c.want)
			}
		})
	}
}
