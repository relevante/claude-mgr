// Package tmux is a thin wrapper over the tmux CLI. The dashboard runs on its
// own tmux server (a dedicated socket) so it never disturbs the user's own tmux
// setup. The controller lives in the left pane of one window; the focused
// Claude session is shown in the right pane, while other live sessions are
// parked in detached windows (their processes keep running).
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	MainWindow = "main" // window holding the controller+session split
	RailWidth  = 40     // fixed columns for the controller rail
)

// Socket and Session are overridable (CLAUDE_MGR_SOCKET / CLAUDE_MGR_SESSION)
// so test instances stay fully isolated from a live dashboard.
var (
	Socket  = envOr("CLAUDE_MGR_SOCKET", "claude-mgr")
	Session = envOr("CLAUDE_MGR_SESSION", "cmgr")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// mainTarget addresses the primary window.
func mainTarget() string { return Session + ":" + MainWindow }

// SessionKey returns the tmux/window key for a session. Claude keeps the
// historical 8-char key; Codex UUIDv7 ids share long time prefixes, so they
// need a collision-safe key.
func SessionKey(id, app string) string {
	if app == "codex" {
		return strings.ReplaceAll(id, "-", "")
	}
	return short(id)
}

// parkedName is the window name a session is parked under when not visible.
func parkedName(ref SessionRef) string { return "s_" + SessionKey(ref.ID, ref.App) }

func short(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// cmd builds a tmux command bound to our socket.
func cmd(args ...string) *exec.Cmd {
	return exec.Command("tmux", append([]string{"-L", Socket}, args...)...)
}

// run executes a tmux command, discarding stdout.
func run(args ...string) error {
	c := cmd(args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// output executes a tmux command and returns trimmed stdout.
func output(args ...string) (string, error) {
	c := cmd(args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ServerUp reports whether our tmux server is running with the main session.
func ServerUp() bool {
	return run("has-session", "-t", Session) == nil
}

// Pane is a snapshot of one pane in a window.
type Pane struct {
	ID    string // e.g. "%3"
	Left  int    // column offset; the leftmost pane is the controller
	Width int
	Cmd   string // foreground command
	PID   int
}

// Panes lists the panes of the main window, left to right.
func Panes() ([]Pane, error) {
	out, err := output("list-panes", "-t", mainTarget(), "-F",
		"#{pane_id}\t#{pane_left}\t#{pane_width}\t#{pane_current_command}\t#{pane_pid}")
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		left, _ := strconv.Atoi(f[1])
		width, _ := strconv.Atoi(f[2])
		pid, _ := strconv.Atoi(f[4])
		panes = append(panes, Pane{ID: f[0], Left: left, Width: width, Cmd: f[3], PID: pid})
	}
	return panes, nil
}

// Parked is a session running in a detached window (not currently shown).
type Parked struct {
	ID8    string // session key from the window name
	PaneID string
}

// ParkedPanes lists sessions parked off-screen in s_<sessionKey> windows.
func ParkedPanes() ([]Parked, error) {
	out, err := output("list-panes", "-s", "-t", Session, "-F", "#{window_name}\t#{pane_id}")
	if err != nil {
		return nil, err
	}
	var parked []Parked
	for _, line := range splitOnNewline(out) {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 || !strings.HasPrefix(f[0], "s_") {
			continue
		}
		parked = append(parked, Parked{ID8: strings.TrimPrefix(f[0], "s_"), PaneID: f[1]})
	}
	return parked, nil
}

// Short returns the legacy 8-char id used for Claude parked window names.
func Short(id string) string { return short(id) }

// layout returns the controller pane and the session pane (if one is shown).
// The controller pane is identified by $TMUX_PANE (set by tmux for the process
// running in it) so detection is robust even while a pane is zoomed — when
// geometry-based heuristics break. Falls back to the leftmost pane.
func layout() (ctrl Pane, sess Pane, hasSess bool, err error) {
	panes, err := Panes()
	if err != nil {
		return Pane{}, Pane{}, false, err
	}
	if len(panes) == 0 {
		return Pane{}, Pane{}, false, errors.New("no panes in main window")
	}
	ctrlID := os.Getenv("TMUX_PANE")
	ctrlIdx := -1
	for i, p := range panes {
		if p.ID == ctrlID {
			ctrlIdx = i
			break
		}
	}
	if ctrlIdx < 0 {
		ctrlIdx = 0 // fallback: leftmost (Panes is left-to-right)
	}
	ctrl = panes[ctrlIdx]
	for i, p := range panes {
		if i != ctrlIdx {
			return ctrl, p, true, nil
		}
	}
	return ctrl, Pane{}, false, nil
}
