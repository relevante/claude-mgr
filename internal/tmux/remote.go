package tmux

// Remote-viewing support. A grouped session (created with `new-session -t
// <Session>`) shares the dashboard's window LIST — so it can see every parked
// s_<key> window — but keeps its OWN selected window and size. That lets a phone
// attach and navigate without yanking the desktop's current window or forcing it
// to the phone's dimensions.

// RemoteName is the grouped session a remote client attaches to.
func RemoteName() string { return Session + "-remote" }

// EnsureRemote creates the grouped remote session if absent. Idempotent. The
// window-size option is set to "latest" so size follows the most-recent client
// rather than locking every shared window to the smallest one.
func EnsureRemote() error {
	if run("has-session", "-t", RemoteName()) == nil {
		return nil
	}
	if err := run("new-session", "-d", "-t", Session, "-s", RemoteName()); err != nil {
		return err
	}
	// Best-effort isolation tweaks; an odd tmux build must not block attach.
	_ = run("set-option", "-t", RemoteName(), "window-size", "latest")
	_ = run("set-option", "-t", RemoteName(), "destroy-unattached", "off")
	return nil
}

// SelectRemoteWindow points the remote session at a parked session's window so
// an attached client shows it. The caller must have ensured the s_<key> window
// exists (see RestoreParked).
func SelectRemoteWindow(key string) error {
	return run("select-window", "-t", RemoteName()+":s_"+key)
}

// ParkedExists reports whether a session's parked window is present.
func ParkedExists(key string) bool { return windowExists("s_" + key) }

// AttachCommand returns the argv for a PTY that attaches a fresh client to the
// remote session. The pane the PTY drives is whatever window the remote session
// currently has selected.
func AttachCommand() (name string, args []string) {
	return "tmux", []string{"-L", Socket, "attach", "-t", RemoteName()}
}
