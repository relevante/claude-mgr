// Package live reports which sessions have a running claude process, so the
// dashboard can flag threads open in other terminals.
//
// Claude maintains an authoritative registry at <config>/sessions/<pid>.json,
// each recording the exact sessionId, cwd, and status of a running process. We
// read that directly — far more reliable than guessing from a process's working
// directory (which fails when one project has several live sessions) — and keep
// only entries whose pid is actually alive (stale files linger after crashes or
// reboots).
package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type record struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"` // "busy" | "idle" (informational)
}

// Sessions returns the set of session ids with a live claude process.
// projectsDir locates the sibling sessions/ registry.
func Sessions(projectsDir string) map[string]bool {
	live := map[string]bool{}
	for _, r := range readRegistry(projectsDir) {
		if pidAlive(r.PID) {
			live[r.SessionID] = true
		}
	}
	return live
}

// Statuses returns live session ids mapped to their reported status
// ("busy"/"idle"), for richer external-status display.
func Statuses(projectsDir string) map[string]string {
	out := map[string]string{}
	for _, r := range readRegistry(projectsDir) {
		if pidAlive(r.PID) {
			out[r.SessionID] = r.Status
		}
	}
	return out
}

// SessionsDir returns the registry directory (sibling of the projects dir) where
// Claude writes one <pid>.json per running process.
func SessionsDir(projectsDir string) string {
	return filepath.Join(filepath.Dir(projectsDir), "sessions")
}

// SessionForPID returns the session id a given pid is currently running,
// straight from its registry file (reflects /clear immediately). "" if unknown.
func SessionForPID(projectsDir string, pid int) string {
	dir := SessionsDir(projectsDir)
	raw, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if err != nil {
		return ""
	}
	var r record
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return r.SessionID
}

func readRegistry(projectsDir string) []record {
	dir := SessionsDir(projectsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var recs []record
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r record
		if json.Unmarshal(raw, &r) != nil || r.SessionID == "" || r.PID <= 0 {
			continue
		}
		recs = append(recs, r)
	}
	return recs
}

// pidAlive reports whether a process exists. EPERM means it exists but is owned
// by another user — still alive for our purposes.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
