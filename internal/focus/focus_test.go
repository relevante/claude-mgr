package focus

import "testing"

func TestParsing(t *testing.T) {
	if got := lastInt(`"pid"=710`); got != 710 {
		t.Errorf("lastInt(`\"pid\"=710`) = %d, want 710", got)
	}
	if got := lastInt(`"LSDisplayName"="Terminal"`); got != 0 {
		t.Errorf("lastInt with no digits = %d, want 0", got)
	}
	if got := firstInt("  11129\n12000\n"); got != 11129 {
		t.Errorf("firstInt = %d, want 11129", got)
	}
}

// TestFocusedSmoke runs the real chain and logs the values for inspection — it
// doesn't assert, since the result depends on what's frontmost during the run.
func TestFocusedSmoke(t *testing.T) {
	t.Logf("frontmostPID=%d", frontmostPID())
	t.Logf("terminalPID(claude-mgr)=%d", terminalPID("claude-mgr"))
	t.Logf("Focused(claude-mgr)=%v", Focused("claude-mgr"))
}
