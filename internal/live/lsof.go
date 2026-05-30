// Package live maps running `claude` processes to the sessions they're driving,
// so the dashboard can flag threads that are open in other terminals. Mapping
// is best-effort: a process's cwd (via lsof) points at a project dir, and the
// most-recently-written transcript there is taken as that process's session.
package live

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sessions returns the set of session ids inferred to be running in some
// `claude` process right now. Never errors fatally — returns what it can.
func Sessions(projectsDir string) map[string]bool {
	live := map[string]bool{}
	for _, pid := range claudePIDs() {
		cwd := processCwd(pid)
		if cwd == "" {
			continue
		}
		if id := newestSessionIn(projectsDir, cwd); id != "" {
			live[id] = true
		}
	}
	return live
}

// claudePIDs returns pids of processes named exactly "claude".
func claudePIDs() []int {
	out, err := exec.Command("pgrep", "-x", "claude").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		n := 0
		for _, c := range line {
			if c < '0' || c > '9' {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > 0 {
			pids = append(pids, n)
		}
	}
	return pids
}

// processCwd returns a process's working directory via lsof.
func processCwd(pid int) string {
	out, err := exec.Command("lsof", "-a", "-d", "cwd", "-p", itoa(pid), "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}

// newestSessionIn returns the session id of the most-recently-modified
// transcript in the project dir corresponding to cwd, or "".
func newestSessionIn(projectsDir, cwd string) string {
	dir := filepath.Join(projectsDir, encodeCwd(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newestName string
	var newestMod int64 = -1
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mt := info.ModTime().UnixNano(); mt > newestMod {
			newestMod = mt
			newestName = e.Name()
		}
	}
	if newestName == "" {
		return ""
	}
	return strings.TrimSuffix(newestName, ".jsonl")
}

// encodeCwd applies Claude's forward path encoding: every "/" becomes "-".
func encodeCwd(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
