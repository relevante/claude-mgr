package tmux

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ErrNoSession is returned when an operation needs the right-hand session pane
// but none is shown (e.g. it was just exited).
var ErrNoSession = errors.New("no session pane shown")

// SessionRef identifies a Claude session to show on the right.
type SessionRef struct {
	ID  string // claude sessionId
	Cwd string // working directory to launch/resume in
}

// EnsureSession starts our tmux server + main window (controller in the only
// pane) if not already running. controllerCmd is the shell command that runs
// the rail UI (e.g. "/path/to/claude-mgr __controller").
func EnsureSession(controllerCmd string) error {
	if ServerUp() {
		return nil
	}
	if err := run("new-session", "-d", "-s", Session, "-n", MainWindow, controllerCmd); err != nil {
		return err
	}
	configure()
	return nil
}

// configure applies global options and pane-navigation bindings. Best-effort:
// individual failures are ignored so an odd tmux build can't block startup.
func configure() {
	opts := [][]string{
		{"set-option", "-g", "mouse", "on"},
		{"set-option", "-g", "status", "off"},
		{"set-option", "-g", "escape-time", "10"},
		{"set-option", "-g", "history-limit", "50000"},
		{"set-option", "-g", "base-index", "0"},
		// Labeled pane headers. The active indicator lives ONLY in the green
		// header text — both border lines (incl. the vertical divider) stay a
		// constant gray, so the divider doesn't flip color as focus moves.
		{"set-option", "-g", "pane-border-status", "top"},
		{"set-option", "-g", "pane-border-format", "#{?pane_active,#[fg=colour84]#[bold]▸ #{pane_title},#[fg=colour245]  #{pane_title}}"},
		{"set-option", "-g", "pane-active-border-style", "fg=colour240"},
		{"set-option", "-g", "pane-border-style", "fg=colour240"},
		// Toggle focus between the rail and the Claude pane. Option+Tab is the
		// primary; Option+l kept as a backup (Apple Terminal can be finicky with
		// special keys, but reliable with Meta+letter).
		{"bind-key", "-n", "M-Tab", "select-pane", "-t", ":.+"},
		{"bind-key", "-n", "M-l", "select-pane", "-t", ":.+"},
		{"bind-key", "-n", "M-z", "resize-pane", "-Z"},
	}
	for _, o := range opts {
		_ = run(o...)
	}
}

// windowExists reports whether a window with the given name exists.
func windowExists(name string) bool {
	out, err := output("list-windows", "-t", Session, "-F", "#{window_name}")
	if err != nil {
		return false
	}
	for _, w := range splitLines(out) {
		if w == name {
			return true
		}
	}
	return false
}

// ShowSession makes target the visible right-hand pane. current is the id of
// the session presently shown ("" if none). Returns whether the target was
// freshly launched (so the caller can send "/tui fullscreen" after it boots).
func ShowSession(target SessionRef, current string) (created bool, err error) {
	if target.ID == current && current != "" {
		return false, nil // already showing it
	}
	ctrl, sess, hasSess, err := layout()
	if err != nil {
		return false, err
	}
	// Park whatever is currently shown, preserving its process + scrollback.
	if hasSess && current != "" {
		if err := run("break-pane", "-d", "-s", sess.ID, "-n", parkedName(current)); err != nil {
			return false, err
		}
	} else if hasSess {
		// Unknown occupant (shouldn't happen): kill it to reclaim the slot.
		_ = run("kill-pane", "-t", sess.ID)
	}

	parked := parkedName(target.ID)
	if !windowExists(parked) {
		if err := run("new-window", "-d", "-n", parked, "-c", target.Cwd, claudeCmd(target.ID)); err != nil {
			return false, err
		}
		created = true
	}
	if err := run("join-pane", "-h", "-s", Session+":"+parked+".0", "-t", ctrl.ID); err != nil {
		return created, err
	}
	pinRail()
	return created, nil
}

// claudeCmd is the shell command that resumes a session in a pane. It can be
// overridden via CLAUDE_MGR_CLAUDE_CMD (a template with {id}) for testing
// without spawning a real claude.
// claudeCmd resumes a session. `exec` so the pane's pid IS the claude process —
// lets us read the pane's current session id from Claude's process registry
// (which matters when /clear changes the id under us).
func claudeCmd(sessionID string) string {
	if tmpl := os.Getenv("CLAUDE_MGR_CLAUDE_CMD"); tmpl != "" {
		return strings.NewReplacer("{id}", sessionID).Replace(tmpl)
	}
	return "exec claude --resume " + sessionID
}

// newClaudeCmd starts a brand-new session (no resume).
func newClaudeCmd() string {
	if tmpl := os.Getenv("CLAUDE_MGR_CLAUDE_CMD"); tmpl != "" {
		return strings.NewReplacer("{id}", "new").Replace(tmpl)
	}
	return "exec claude"
}

// SessionPanePID returns the pid of the process in the shown session pane (the
// claude process, thanks to exec).
func SessionPanePID() (int, bool) {
	_, sess, has, err := layout()
	if err != nil || !has {
		return 0, false
	}
	out, err := output("display-message", "-p", "-t", sess.ID, "#{pane_pid}")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, false
	}
	return n, true
}

// LaunchNew opens a brand-new claude in cwd on the right, parking whatever is
// currently shown. tmpID is a placeholder id; its parked window name is used so
// the session can later be adopted (renamed) once its real id is known.
func LaunchNew(cwd, tmpID, current string) error {
	ctrl, sess, hasSess, err := layout()
	if err != nil {
		return err
	}
	if hasSess && current != "" {
		if err := run("break-pane", "-d", "-s", sess.ID, "-n", parkedName(current)); err != nil {
			return err
		}
	} else if hasSess {
		_ = run("kill-pane", "-t", sess.ID)
	}
	// Split a new pane directly into the main window, right of the controller —
	// atomic, with no detached-window/join-pane race that can drop a
	// fast-exiting session. tmpID is used by the caller for tracking/parking.
	_ = tmpID
	if err := run("split-window", "-h", "-t", ctrl.ID, "-c", cwd, newClaudeCmd()); err != nil {
		return err
	}
	if _, _, has, _ := layout(); !has {
		return errors.New("session exited immediately — is 'claude' on PATH and the directory valid?")
	}
	pinRail()
	return nil
}

// RestoreParked recreates parked windows for a set of sessions (resuming each)
// without showing them. Used on startup to rebuild a saved workspace. Existing
// windows are left alone.
func RestoreParked(refs []SessionRef) {
	for _, r := range refs {
		win := parkedName(r.ID)
		if windowExists(win) {
			continue
		}
		_ = run("new-window", "-d", "-n", win, "-c", r.Cwd, claudeCmd(r.ID))
	}
}

// Detach detaches all clients from our session, leaving the dashboard (and all
// its sessions) running in the background to be re-attached later.
func Detach() error {
	return run("detach-client", "-s", Session)
}

// KillServer tears down the whole dashboard: the controller, every session
// pane, and the tmux server. Sessions remain resumable from disk.
func KillServer() error {
	return run("kill-server")
}

// AdoptParked renames a parked placeholder window to the real session's name,
// so future park/join and status polling address it correctly. No-op if the
// placeholder window doesn't exist (the session is currently shown instead).
func AdoptParked(tmpID, realID string) {
	from, to := parkedName(tmpID), parkedName(realID)
	if windowExists(from) {
		_ = run("rename-window", "-t", Session+":"+from, to)
	}
}

// pinRail fixes the controller pane to RailWidth columns, clamped so it never
// exceeds half the window (keeps it usable when a large font leaves few cols).
// A no-op when no session is shown (the rail is full-width then).
func pinRail() {
	ctrl, _, has, err := layout()
	if err != nil || !has {
		return
	}
	if isZoomed() {
		return // don't fight an active zoom (rail or session fullscreen)
	}
	w := RailWidth
	if ww := windowWidth(); ww > 0 && w > ww/2 {
		w = ww / 2
	}
	_ = run("resize-pane", "-t", ctrl.ID, "-x", strconv.Itoa(w))
}

func isZoomed() bool {
	z, err := output("display-message", "-p", "-t", mainTarget(), "#{window_zoomed_flag}")
	return err == nil && z == "1"
}

// RePin re-applies the rail width; called by the controller on every resize so
// font-size changes don't let tmux shrink the rail proportionally.
func RePin() { pinRail() }

func windowWidth() int {
	out, err := output("display-message", "-p", "-t", mainTarget(), "#{window_width}")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// Unzoom clears the window's zoom if set, returning to the split (rail visible).
func Unzoom() error {
	z, err := output("display-message", "-p", "-t", mainTarget(), "#{window_zoomed_flag}")
	if err == nil && z == "1" {
		return run("resize-pane", "-Z", "-t", mainTarget())
	}
	return nil
}

// Zoom toggles fullscreen on the session pane (hides the rail).
func Zoom() error {
	_, sess, has, err := layout()
	if err != nil || !has {
		return err
	}
	return run("resize-pane", "-Z", "-t", sess.ID)
}

// FocusSession / FocusController move keyboard focus between the panes.
func FocusSession() error {
	_, sess, has, err := layout()
	if err != nil || !has {
		return err
	}
	return run("select-pane", "-t", sess.ID)
}

func FocusController() error {
	ctrl, _, _, err := layout()
	if err != nil {
		return err
	}
	return run("select-pane", "-t", ctrl.ID)
}

// BindLoadKeys installs global (prefix-less) keys that move the rail selection
// and load the session — working even while the Claude pane is focused, by
// sending the key through to the controller pane. Must be called from the
// controller process (reads its own $TMUX_PANE). Idempotent.
func BindLoadKeys() {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	for _, k := range []string{"M-Down", "M-Up", "M-t", "M-S-Up", "M-S-Down"} {
		_ = run("bind-key", "-n", k, "send-keys", "-t", pane, k)
	}
}

// SetControllerTitle labels the rail pane (shown in its header).
func SetControllerTitle(title string) error {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return nil
	}
	return run("select-pane", "-t", pane, "-T", title)
}

// SetSessionTitle labels the currently-shown session pane (shown in its header).
func SetSessionTitle(title string) error {
	_, sess, has, err := layout()
	if err != nil || !has {
		return err
	}
	return run("select-pane", "-t", sess.ID, "-T", title)
}

// SendSession types a command line into the session pane, followed by Enter.
func SendSession(line string) error {
	_, sess, has, err := layout()
	if err != nil || !has {
		return err
	}
	return run("send-keys", "-t", sess.ID, line, "Enter")
}

// SessionPaneID returns the pane id of the currently-shown session, if any.
func SessionPaneID() (string, bool) {
	_, sess, has, err := layout()
	if err != nil || !has {
		return "", false
	}
	return sess.ID, true
}

// SendPaneKeys sends key(s) to a specific pane (tmux key names, e.g. "2",
// "Enter", "Down").
func SendPaneKeys(paneID string, keys ...string) error {
	return run(append([]string{"send-keys", "-t", paneID}, keys...)...)
}

// CaptureSession returns the last n lines visible in the session pane, or
// ErrNoSession if no session is shown (so callers don't mistake "no pane" for
// "captured empty content").
func CaptureSession(n int) (string, error) {
	_, sess, has, err := layout()
	if err != nil {
		return "", err
	}
	if !has {
		return "", ErrNoSession
	}
	return output("capture-pane", "-p", "-t", sess.ID, "-S", strconv.Itoa(-n))
}

// CapturePane returns the last n lines of a specific pane id.
func CapturePane(paneID string, n int) (string, error) {
	return output("capture-pane", "-p", "-t", paneID, "-S", strconv.Itoa(-n))
}

// Attach replaces the current process with a tmux client attached to our
// session. Used by the launcher after EnsureSession.
func Attach() error {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	argv := []string{"tmux", "-L", Socket, "attach", "-t", Session}
	return syscall.Exec(path, argv, os.Environ())
}

func splitLines(s string) []string {
	var out []string
	for _, l := range splitOnNewline(s) {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitOnNewline(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
