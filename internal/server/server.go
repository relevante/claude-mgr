// Package server exposes claude-mgr over HTTP+WebSocket so a phone (or any
// browser) can drive the same sessions the tmux dashboard manages. It runs
// IN-PROCESS with the Bubble Tea controller (see runController), sharing the one
// *overlay.Overlay instance so pins/names/archives can't race a second writer.
//
// Because it lives inside the alt-screen TUI process, it must NEVER write to
// stdout/stderr (that corrupts the display). All diagnostics go to a log file
// under the config dir; see logf.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-mgr/internal/index"
	"claude-mgr/internal/overlay"
)

// Server holds the shared dependencies the handlers need. store and ov are the
// SAME instances the controller UI uses.
type Server struct {
	store *index.Store
	ov    *overlay.Overlay
	token string
	log   io.Writer
}

// Run starts the HTTP server on addr (e.g. "127.0.0.1:8787") and blocks. It is
// meant to be called in a goroutine from the controller. Errors are written to
// the log file, never to stderr.
func Run(addr string, store *index.Store, ov *overlay.Overlay) error {
	s := newServer(store, ov, resolveToken(), openLog())
	s.logf("serving on http://%s/  (token in %s)", addr, tokenPath())

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	err := srv.ListenAndServe()
	if err != nil {
		s.logf("listen: %v", err)
	}
	return err
}

func newServer(store *index.Store, ov *overlay.Overlay, token string, log io.Writer) *Server {
	return &Server{store: store, ov: ov, token: token, log: log}
}

// handler builds the routed, auth-gated handler. Exposed for tests.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("GET /api/sessions/stream", s.auth(s.handleStream))
	mux.HandleFunc("POST /api/new", s.auth(s.handleNew))
	mux.HandleFunc("POST /api/sessions/{id}/{action}", s.auth(s.handleAction))
	mux.HandleFunc("GET /api/terminal", s.auth(s.handleTerminal))
	mux.Handle("/", s.staticHandler())
}

// auth gates a handler behind the bearer token. The token may arrive as an
// Authorization: Bearer header (API clients) or a ?token= query param (browser
// page loads and the WebSocket upgrade, which can't set custom headers easily).
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	if got == "" {
		got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// --- token + config-dir plumbing -------------------------------------------

// configDir mirrors the convention in index/overlay: $XDG_CONFIG_HOME or
// ~/.config, under claude-mgr.
func configDir() string {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "claude-mgr")
}

func tokenPath() string { return filepath.Join(configDir(), "serve-token") }

// resolveToken uses CLAUDE_MGR_SERVE_TOKEN when set, else a persisted random
// token (generated once, 0600). It is never printed to the terminal.
func resolveToken() string {
	if t := os.Getenv("CLAUDE_MGR_SERVE_TOKEN"); t != "" {
		return t
	}
	if raw, err := os.ReadFile(tokenPath()); err == nil {
		if t := strings.TrimSpace(string(raw)); t != "" {
			return t
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	_ = os.MkdirAll(configDir(), 0o755)
	_ = os.WriteFile(tokenPath(), []byte(t+"\n"), 0o600)
	return t
}

func openLog() io.Writer {
	_ = os.MkdirAll(configDir(), 0o755)
	f, err := os.OpenFile(filepath.Join(configDir(), "serve.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}

func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "%s "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
}
