package server

import (
	"encoding/json"
	"net/http"
	"time"

	"claude-mgr/internal/index"
	"claude-mgr/internal/live"
	"claude-mgr/internal/status"
)

// sessionJSON is the wire shape for one session. It deliberately mirrors what
// the rail shows rather than reusing the lipgloss row layer.
type sessionJSON struct {
	ID            string `json:"id"`
	App           string `json:"app"`
	Name          string `json:"name"`
	Project       string `json:"project"`
	Status        string `json:"status"` // idle|working|waiting|permission|shell
	Live          bool   `json:"live"`
	Pinned        bool   `json:"pinned"`
	Archived      bool   `json:"archived"`
	ContextTokens int    `json:"contextTokens"`
	ContextLimit  int    `json:"contextLimit"`
	LastActive    int64  `json:"lastActive"` // unix seconds
	Cwd           string `json:"cwd"`
}

type groupJSON struct {
	Label    string        `json:"label"`
	Cwd      string        `json:"cwd"`
	Sessions []sessionJSON `json:"sessions"`
}

func statusString(s index.Status) string {
	switch s {
	case index.StatusWorking:
		return "working"
	case index.StatusWaiting:
		return "waiting"
	case index.StatusPermission:
		return "permission"
	case index.StatusShell:
		return "shell"
	default:
		return "idle"
	}
}

// buildSessions scans, merges live status (Claude + Codex) and overlay metadata,
// and groups by project — the same composition the rail performs. Empty stub
// sessions are dropped (the rail hides them by default); archived sessions are
// kept but flagged so the client can choose to show them.
func (s *Server) buildSessions() ([]groupJSON, error) {
	sessions, err := s.store.Scan()
	if err != nil {
		return nil, err
	}

	statuses := live.Statuses(s.store.ProjectsDir)
	for id, st := range live.CodexStatuses() {
		statuses[id] = st
	}

	groups := index.GroupByProject(sessions)
	out := make([]groupJSON, 0, len(groups))
	for _, g := range groups {
		gj := groupJSON{Label: g.Label, Cwd: g.Cwd}
		for i := range g.Sessions {
			m := g.Sessions[i]
			if m.IsEmpty() {
				continue
			}
			reg, isLive := statuses[m.SessionID]
			st := index.StatusIdle
			if isLive {
				st = status.FromRegistry(reg)
			}
			name := m.AutoTitle
			if n, ok := s.ov.Name(m.SessionID); ok {
				name = n
			}
			gj.Sessions = append(gj.Sessions, sessionJSON{
				ID:            m.SessionID,
				App:           m.AppName(),
				Name:          name,
				Project:       g.Label,
				Status:        statusString(st),
				Live:          isLive,
				Pinned:        s.ov.IsPinned(m.SessionID),
				Archived:      s.ov.IsArchived(m.SessionID) || m.Archived,
				ContextTokens: m.ContextTokens,
				ContextLimit:  m.ContextLimit,
				LastActive:    m.LastActive.Unix(),
				Cwd:           m.Cwd,
			})
		}
		if len(gj.Sessions) > 0 {
			out = append(out, gj)
		}
	}
	return out, nil
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	groups, err := s.buildSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, groups)
}

// handleStream pushes the full session list on every registry change (and on a
// slow safety-net ticker), as Server-Sent Events. Full snapshots, not deltas:
// the payload is a few KB and the client just re-renders.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() {
		groups, err := s.buildSessions()
		if err != nil {
			return
		}
		raw, err := json.Marshal(groups)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}

	send() // initial snapshot

	// Prefer the registry watcher the TUI uses; fall back to pure polling.
	var changed <-chan struct{}
	if reg, err := s.watchRegistry(); err == nil {
		defer reg.Close()
		changed = reg.Events()
	}
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-changed:
			send()
		case <-ticker.C:
			send()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
