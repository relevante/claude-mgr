package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"claude-mgr/internal/index"
	"claude-mgr/internal/live"
	"claude-mgr/internal/watch"
)

// handleAction applies a control action to one session. Overlay actions
// (pin/archive/rename) touch only the shared, mutex-guarded overlay; open/kill
// touch tmux (see terminal.go).
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	var err error
	switch action {
	case "pin":
		err = s.ov.TogglePinned(id)
	case "archive":
		err = s.ov.ToggleArchived(id)
	case "rename":
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		err = s.ov.SetName(id, strings.TrimSpace(body.Name))
	case "open":
		err = s.openSession(id)
	case "kill":
		err = s.killSession(id)
	default:
		http.Error(w, "unknown action: "+action, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleNew launches a brand-new session in a chosen project directory.
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cwd string `json:"cwd"`
		App string `json:"app"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Cwd) == "" {
		http.Error(w, "cwd required", http.StatusBadRequest)
		return
	}
	id, err := s.newSession(strings.TrimSpace(body.Cwd), body.App)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

// findMeta resolves a session id to its current metadata (for app/cwd), via a
// cheap cached Scan. Returns false if the id is unknown.
func (s *Server) findMeta(id string) (index.SessionMeta, bool) {
	sessions, err := s.store.Scan()
	if err != nil {
		return index.SessionMeta{}, false
	}
	for _, m := range sessions {
		if m.SessionID == id {
			return m, true
		}
	}
	return index.SessionMeta{}, false
}

func (s *Server) watchRegistry() (*watch.Registry, error) {
	return watch.NewRegistry(live.SessionsDir(s.store.ProjectsDir))
}
