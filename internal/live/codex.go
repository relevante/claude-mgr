package live

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var rolloutID = regexp.MustCompile(`rollout-[^/]*-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl`)

var codexCache = struct {
	sync.Mutex
	at       time.Time
	statuses map[string]string
}{}

const codexLiveTTL = 2 * time.Second

// CodexSessions returns the set of Codex session ids with a live codex process.
func CodexSessions() map[string]bool {
	statuses := CodexStatuses()
	out := make(map[string]bool, len(statuses))
	for id := range statuses {
		out[id] = true
	}
	return out
}

// CodexStatuses returns live Codex sessions. Codex does not expose a reliable
// external busy/idle signal, so every live external session is reported idle.
func CodexStatuses() map[string]string {
	codexCache.Lock()
	if time.Since(codexCache.at) < codexLiveTTL && codexCache.statuses != nil {
		out := cloneStatuses(codexCache.statuses)
		codexCache.Unlock()
		return out
	}
	codexCache.Unlock()

	statuses := map[string]string{}
	for _, pid := range codexPIDs() {
		if id := CodexSessionForPID(pid); id != "" {
			statuses[id] = "idle"
		}
	}

	codexCache.Lock()
	codexCache.at = time.Now()
	codexCache.statuses = cloneStatuses(statuses)
	codexCache.Unlock()
	return statuses
}

// CodexSessionForPID returns the Codex session id held open by pid, if any.
func CodexSessionForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("lsof", "-nP", "-F", "n", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return codexSessionIDFromLsof(string(out))
}

func codexPIDs() []int {
	out, err := exec.Command("pgrep", "-x", "codex").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func codexSessionIDFromLsof(out string) string {
	for _, m := range rolloutID.FindAllStringSubmatch(out, -1) {
		if len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

func cloneStatuses(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
