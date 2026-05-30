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
