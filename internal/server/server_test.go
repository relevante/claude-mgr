package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-mgr/internal/index"
	"claude-mgr/internal/overlay"
)

// newTestServer builds a Server over a temp projects dir holding one real,
// non-empty Claude session, plus an isolated overlay. Returns the running
// httptest server and the session id.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects")
	// Project dir name must equal encodeCwd(cwd) so no drift head-read is needed.
	projDir := filepath.Join(projects, "-Users-test-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "abcd1234-0000-0000-0000-000000000001"
	line := `{"type":"last-prompt","timestamp":"2026-06-13T10:00:00Z","sessionId":"` + id +
		`","cwd":"/Users/test/proj","gitBranch":"main","lastPrompt":"Fix the parser bug"}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_MGR_PROJECTS", projects)
	t.Setenv("CLAUDE_MGR_CACHE", filepath.Join(dir, "index.json"))
	t.Setenv("CLAUDE_MGR_CODEX_STATE", filepath.Join(dir, "none.sqlite"))

	store, err := index.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	ov := overlay.Load(filepath.Join(dir, "overlay.json"))
	s := newServer(store, ov, "secret", io.Discard)
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return ts, id
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func post(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path+"?token=secret", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAuthRequired(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

func TestSessionsList(t *testing.T) {
	ts, id := newTestServer(t)
	resp, body := get(t, ts, "/api/sessions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var groups []groupJSON
	if err := json.Unmarshal(body, &groups); err != nil {
		t.Fatalf("bad json: %v\n%s", err, body)
	}
	if len(groups) != 1 || len(groups[0].Sessions) != 1 {
		t.Fatalf("want 1 group/1 session, got %+v", groups)
	}
	s := groups[0].Sessions[0]
	if s.ID != id {
		t.Errorf("id = %q, want %q", s.ID, id)
	}
	if s.Name != "Fix the parser bug" {
		t.Errorf("name = %q, want auto-title from last prompt", s.Name)
	}
	if s.App != "claude" {
		t.Errorf("app = %q, want claude", s.App)
	}
	if s.Status != "idle" || s.Live {
		t.Errorf("dormant session should be idle/not-live, got status=%q live=%v", s.Status, s.Live)
	}
	if s.Project != "test/proj" {
		t.Errorf("project = %q, want test/proj", s.Project)
	}
}

func TestPinAndRenamePersist(t *testing.T) {
	ts, id := newTestServer(t)

	if resp := post(t, ts, "/api/sessions/"+id+"/pin"); resp.StatusCode != http.StatusOK {
		t.Fatalf("pin status %d", resp.StatusCode)
	}
	// Rename via JSON body.
	req, _ := http.NewRequest("POST", ts.URL+"/api/sessions/"+id+"/rename?token=secret",
		strings.NewReader(`{"name":"My Session"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename status %d", resp.StatusCode)
	}

	_, body := get(t, ts, "/api/sessions")
	var groups []groupJSON
	if err := json.Unmarshal(body, &groups); err != nil {
		t.Fatal(err)
	}
	s := groups[0].Sessions[0]
	if !s.Pinned {
		t.Error("session should be pinned after POST pin")
	}
	if s.Name != "My Session" {
		t.Errorf("name = %q, want overlay name to win", s.Name)
	}
}

func TestStaticServedWithoutToken(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		resp, err := http.Get(ts.URL + path) // no token on purpose
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 (static must not be gated)", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("%s: empty body", path)
		}
	}
}

func TestUnknownAction(t *testing.T) {
	ts, id := newTestServer(t)
	if resp := post(t, ts, "/api/sessions/"+id+"/frobnicate"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown action, got %d", resp.StatusCode)
	}
}
