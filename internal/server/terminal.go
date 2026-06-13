package server

import (
	"fmt"
	"net/http"

	"claude-mgr/internal/tmux"
)

// openSession makes a session available for remote viewing by ensuring it has a
// parked tmux window (s_<key>). It uses RestoreParked, NOT ShowSession, so it
// never hijacks the desktop's right-hand pane. Idempotent. The launch dir is
// ResumeCwd() to survive cwd drift.
func (s *Server) openSession(id string) error {
	m, ok := s.findMeta(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	tmux.RestoreParked([]tmux.SessionRef{{
		ID:  m.SessionID,
		Cwd: m.ResumeCwd(),
		App: m.AppName(),
	}})
	return nil
}

// killSession terminates a parked session's process. See tmux.KillParked for the
// deliberate no-op when the session is currently shown on the desktop.
func (s *Server) killSession(id string) error {
	m, ok := s.findMeta(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	return tmux.KillParked(tmux.SessionKey(m.SessionID, m.AppName()))
}

// handleTerminal is the interactive PTY bridge — implemented in the WebSocket
// task. Placeholder until then.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "terminal bridge not yet implemented", http.StatusNotImplemented)
}
