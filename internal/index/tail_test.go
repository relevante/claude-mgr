package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCwdDrifted(t *testing.T) {
	cases := []struct {
		name, cwd, projectDir string
		want                  bool
	}{
		{"matching encode", "/Users/j/Work/CousinsSears/sensorpush",
			"-Users-j-Work-CousinsSears-sensorpush", false},
		{"dashes in path segments still match", "/Users/j/House/130-south-road/shop/mill",
			"-Users-j-House-130-south-road-shop-mill", false},
		{"drifted to a subdirectory", "/Users/j/Work/CousinsSears/sensorpush/beacon-thermometer-android",
			"-Users-j-Work-CousinsSears-sensorpush", true},
		{"empty cwd never drifts", "", "-Users-j-x", false},
		{"empty projectDir never drifts", "/Users/j/x", "", false},
	}
	for _, c := range cases {
		if got := cwdDrifted(c.cwd, c.projectDir); got != c.want {
			t.Errorf("%s: cwdDrifted(%q,%q)=%v, want %v", c.name, c.cwd, c.projectDir, got, c.want)
		}
	}
}

func TestResumeCwd(t *testing.T) {
	drifted := SessionMeta{Cwd: "/work/android", OriginCwd: "/work/root"}
	if got := drifted.ResumeCwd(); got != "/work/root" {
		t.Fatalf("drifted ResumeCwd=%q, want origin /work/root", got)
	}
	plain := SessionMeta{Cwd: "/work/root"}
	if got := plain.ResumeCwd(); got != "/work/root" {
		t.Fatalf("plain ResumeCwd=%q, want /work/root", got)
	}
}

// A session whose cwd drifted mid-session (records show a different dir than
// the one the transcript's project dir encodes) must surface its ORIGIN cwd,
// because `claude --resume` only finds the session from the origin project.
func TestExtractMetaRecoversOriginCwdOnDrift(t *testing.T) {
	dir := t.TempDir()
	origin := "/Users/j/Work/CousinsSears/sensorpush"
	drifted := origin + "/beacon-thermometer-android"
	path := filepath.Join(dir, "sess.jsonl")
	transcript := `{"type":"user","timestamp":"2026-06-06T10:00:00Z","cwd":"` + origin + `","message":{"role":"user","content":"start here"}}
{"type":"last-prompt","timestamp":"2026-06-06T10:05:00Z","cwd":"` + drifted + `","lastPrompt":"keep going"}
`
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	m := SessionMeta{
		SessionID:  "sess",
		Path:       path,
		ProjectDir: encodeCwd(origin), // claude derives the project dir from the ORIGIN cwd
		FileSize:   fi.Size(),
		FileMtime:  fi.ModTime(),
	}
	if err := extractMeta(&m); err != nil {
		t.Fatal(err)
	}
	if m.Cwd != drifted {
		t.Fatalf("Cwd=%q, want the drifted dir %q (display/grouping follows the work)", m.Cwd, drifted)
	}
	if m.OriginCwd != origin {
		t.Fatalf("OriginCwd=%q, want %q", m.OriginCwd, origin)
	}
	if m.ResumeCwd() != origin {
		t.Fatalf("ResumeCwd=%q, want origin %q", m.ResumeCwd(), origin)
	}

	// No drift → OriginCwd stays empty (cache stays lean, ResumeCwd = Cwd).
	noDrift := SessionMeta{
		SessionID: "sess", Path: path,
		ProjectDir: encodeCwd(drifted),
		FileSize:   fi.Size(), FileMtime: fi.ModTime(),
	}
	// Rewrite transcript with a single consistent cwd.
	consistent := `{"type":"last-prompt","timestamp":"2026-06-06T10:05:00Z","cwd":"` + drifted + `","lastPrompt":"hi"}
`
	if err := os.WriteFile(path, []byte(consistent), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(path)
	noDrift.FileSize, noDrift.FileMtime = fi.Size(), fi.ModTime()
	if err := extractMeta(&noDrift); err != nil {
		t.Fatal(err)
	}
	if noDrift.OriginCwd != "" {
		t.Fatalf("no-drift OriginCwd=%q, want empty", noDrift.OriginCwd)
	}
}
