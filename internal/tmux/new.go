package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// NewParked launches a brand-new agent (no resume) in cwd as a detached window,
// returning the new window id and its pane id. The window keeps its default name
// until the caller adopts it (AdoptNew) once the real session id is known — so
// it stays invisible to the controller's s_<key> session scan in the meantime.
func NewParked(cwd, app string) (winID, paneID string, err error) {
	out, err := output("new-window", "-d", "-P", "-c", cwd,
		"-F", "#{window_id}\t#{pane_id}", newSessionCmd(app))
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected new-window output %q", out)
	}
	return parts[0], parts[1], nil
}

// PanePID returns the pid running in a pane (the agent itself, since sessions
// launch via `exec`).
func PanePID(paneID string) (int, bool) {
	out, err := output("display-message", "-p", "-t", paneID, "#{pane_pid}")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, false
	}
	return n, true
}

// AdoptNew renames a freshly-created window (by id) to a session's parked name,
// so it integrates like any other parked session once its real id is known.
func AdoptNew(winID, key string) error {
	return run("rename-window", "-t", winID, "s_"+key)
}

// SelectRemoteWindowID points the remote session at a window by id — used to show
// a brand-new session before it has been adopted under an s_<key> name.
func SelectRemoteWindowID(winID string) error {
	return run("select-window", "-t", RemoteName()+":"+winID)
}
