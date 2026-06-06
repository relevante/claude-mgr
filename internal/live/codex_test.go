package live

import "testing"

func TestCodexSessionIDFromLsof(t *testing.T) {
	const id = "019e9a6a-bc79-7990-bfa1-41096d988ffc"
	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "field output",
			out:  "p123\nn/Users/j/.codex/sessions/2026/06/05/rollout-2026-06-05T20-52-26-" + id + ".jsonl\n",
			want: id,
		},
		{
			name: "classic output",
			out:  "codex 123 j 25w REG 1,4 200 /Users/j/.codex/sessions/rollout-2026-06-05T20-52-26-" + id + ".jsonl",
			want: id,
		},
		{
			name: "no rollout",
			out:  "n/Users/j/.codex/state_5.sqlite",
			want: "",
		},
	}
	for _, c := range cases {
		if got := codexSessionIDFromLsof(c.out); got != c.want {
			t.Errorf("%s: id=%q, want %q", c.name, got, c.want)
		}
	}
}
