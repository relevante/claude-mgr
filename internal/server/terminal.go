package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"claude-mgr/internal/index"
	"claude-mgr/internal/live"
	"claude-mgr/internal/tmux"
)

// openSession makes a session viewable remotely by ensuring it has a parked tmux
// window (s_<key>), using RestoreParked — NOT ShowSession, which would hijack the
// desktop's right pane. It refuses to resume a session that is already live but
// unparked (i.e. shown on the desktop or running in an external terminal), since
// a second `--resume` of the same id would spawn a duplicate process.
func (s *Server) openSession(id string) error {
	m, ok := s.findMeta(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	key := tmux.SessionKey(m.SessionID, m.AppName())
	if tmux.ParkedExists(key) {
		return nil // already viewable
	}
	if s.isLiveElsewhere(m) {
		return fmt.Errorf("session is active on the desktop; park it there before opening remotely")
	}
	tmux.RestoreParked([]tmux.SessionRef{{
		ID:  m.SessionID,
		Cwd: m.ResumeCwd(),
		App: m.AppName(),
	}})
	return nil
}

func (s *Server) isLiveElsewhere(m index.SessionMeta) bool {
	if m.AppName() == index.AppCodex {
		return live.CodexSessions()[m.SessionID]
	}
	return live.Sessions(s.store.ProjectsDir)[m.SessionID]
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

// selectForRemote parks the session if needed and points the remote session's
// current window at it, so the attached PTY shows it.
func (s *Server) selectForRemote(id string) error {
	if err := s.openSession(id); err != nil {
		return err
	}
	m, ok := s.findMeta(id)
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	return tmux.SelectRemoteWindow(tmux.SessionKey(m.SessionID, m.AppName()))
}

// ctrlMsg is a client→server control frame (sent as a JSON text message). Raw
// keystrokes may also arrive as binary frames; both feed the PTY.
type ctrlMsg struct {
	Type    string `json:"type"` // input | resize | select
	Data    string `json:"data"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Session string `json:"session"`
}

// handleTerminal bridges a browser terminal (xterm.js) to a tmux client over a
// PTY. The PTY attaches to the grouped remote session, so it views whatever
// window the phone has selected — independent of the desktop. One WebSocket is a
// single, persistent viewport; switching sessions is just a {type:"select"} that
// re-points the remote session's current window.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logf("ws accept: %v", err)
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)

	if err := tmux.EnsureRemote(); err != nil {
		s.logf("ensure remote: %v", err)
		c.Close(websocket.StatusInternalError, "remote session")
		return
	}
	if id := r.URL.Query().Get("session"); id != "" {
		if err := s.selectForRemote(id); err != nil {
			s.logf("select %s: %v", id, err)
		}
	}

	name, args := tmux.AttachCommand()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		s.logf("pty start: %v", err)
		c.Close(websocket.StatusInternalError, "pty")
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx := r.Context()

	// PTY → WS (terminal output as binary frames).
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				c.Close(websocket.StatusNormalClosure, "terminal exited")
				return
			}
		}
	}()

	// WS → PTY (input / resize / select).
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			_, _ = ptmx.Write(data)
			continue
		}
		var msg ctrlMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "input":
			_, _ = ptmx.Write([]byte(msg.Data))
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols})
			}
		case "select":
			if err := s.selectForRemote(msg.Session); err != nil {
				s.logf("select %s: %v", msg.Session, err)
			}
		}
	}
}
