package server

import (
	"bytes"
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"claude-mgr/internal/tmux"
)

// TestTerminalBridge drives the full WebSocket→PTY→tmux path against a real tmux
// on an isolated socket. The "session" being resumed is overridden to `cat`, so
// it simply echoes input — letting us assert a round trip without claude.
func TestTerminalBridge(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	// Isolate tmux globals (evaluated at package init, so env won't retarget
	// them in-process — set them directly and restore after).
	origSock, origSess := tmux.Socket, tmux.Session
	tmux.Socket = "cmgr-srvtest-" + strings.ReplaceAll(t.Name(), "/", "-")
	tmux.Session = "cmgrsrvtest"
	t.Cleanup(func() {
		_ = tmux.KillServer()
		tmux.Socket, tmux.Session = origSock, origSess
	})
	// Resumes run `cat` (echoes input) instead of a real agent.
	t.Setenv("CLAUDE_MGR_CLAUDE_CMD", "cat")

	if err := tmux.EnsureSession("sleep 120"); err != nil {
		t.Fatalf("ensure base session: %v", err)
	}

	ts, id := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/terminal?token=secret&session=" + id

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	// Give the PTY/tmux attach a moment, then send a line cat will echo.
	time.Sleep(300 * time.Millisecond)
	marker := "echo-roundtrip-9271"
	if err := c.Write(ctx, websocket.MessageText,
		[]byte(`{"type":"input","data":"`+marker+`\n"}`)); err != nil {
		t.Fatalf("write input: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var acc bytes.Buffer
	for time.Now().Before(deadline) {
		readCtx, rc := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := c.Read(readCtx)
		rc()
		if err != nil {
			continue
		}
		acc.Write(data)
		if strings.Contains(acc.String(), marker) {
			return // success: input reached cat and echoed back through the bridge
		}
	}
	t.Fatalf("marker %q never echoed back; got %dB:\n%s", marker, acc.Len(), acc.String())
}
