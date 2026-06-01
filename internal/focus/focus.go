// Package focus reports whether the terminal app hosting our tmux client is the
// frontmost macOS app — a reliable, pane-independent "is the user looking at us"
// signal.
//
// Terminal focus-reporting escape sequences (DECSET 1004) are unreliable under
// tmux: tmux delivers them only to the active pane, which is usually the Claude
// pane rather than the rail, so the controller's focus state gets stuck. Instead
// we ask the OS via lsappinfo (no permission prompt, ~10ms) and compare the
// frontmost app's pid to our terminal app's pid (found by walking up from the
// tmux client). Throttled + cached; on any uncertainty it reports focused, so a
// detection failure can't cause spurious chimes.
package focus

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mu     sync.Mutex
	last   time.Time
	cached = true
)

// Focused reports whether our terminal (the one attached to the given tmux
// socket) is the frontmost app. Calls are throttled to once per second.
func Focused(socket string) bool {
	mu.Lock()
	defer mu.Unlock()
	if !last.IsZero() && time.Since(last) < time.Second {
		return cached
	}
	last = time.Now()
	cached = compute(socket)
	return cached
}

func compute(socket string) bool {
	fpid := frontmostPID()
	tpid := terminalPID(socket)
	if fpid == 0 || tpid == 0 {
		return true // unknown — assume focused so we don't over-chime
	}
	return fpid == tpid
}

// frontmostPID returns the pid of the frontmost GUI application.
func frontmostPID() int {
	asn, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return 0
	}
	out, err := exec.Command("lsappinfo", "info", "-only", "pid", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return 0
	}
	return lastInt(string(out)) // e.g. `"pid"=710`
}

// terminalPID walks up from the tmux client process to the GUI app (ppid 1).
func terminalPID(socket string) int {
	out, err := exec.Command("tmux", "-L", socket, "list-clients", "-F", "#{client_pid}").Output()
	if err != nil {
		return 0
	}
	pid := firstInt(string(out))
	for i := 0; pid > 1 && i < 12; i++ {
		ppid := parentPID(pid)
		if ppid <= 1 {
			break
		}
		pid = ppid
	}
	return pid
}

func parentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	return firstInt(string(out))
}

func firstInt(s string) int {
	for _, f := range strings.Fields(s) {
		if n, err := strconv.Atoi(f); err == nil {
			return n
		}
	}
	return 0
}

// lastInt returns the last integer token in s (digits split on any non-digit).
func lastInt(s string) int {
	n := 0
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		if v, err := strconv.Atoi(f); err == nil {
			n = v
		}
	}
	return n
}
