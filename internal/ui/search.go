package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"claude-mgr/internal/index"
	"claude-mgr/internal/tmux"
)

// displayName resolves a session's shown name: a custom overlay name wins over
// the app's auto-title.
func (m *Model) displayName(s index.SessionMeta) string {
	if n, ok := m.ov.Name(s.SessionID); ok {
		return n
	}
	return s.AutoTitle
}

func (m *Model) sessionTitle(s index.SessionMeta) string {
	return brandGlyph(s) + " " + m.displayName(s)
}

func sessionKey(s index.SessionMeta) string {
	return tmux.SessionKey(s.SessionID, s.AppName())
}

// visible applies the empty/archived/active filters.
func (m *Model) visible(s index.SessionMeta) bool {
	if m.hideEmpty && s.IsEmpty() {
		return false
	}
	if !m.showArchived && (m.ov.IsArchived(s.SessionID) || s.Archived) {
		return false
	}
	if m.activeOnly && !m.isLive(s) {
		return false
	}
	return true
}

// isLive reports whether a session currently has activity anywhere — open in
// our dashboard this run, running in our tmux (status from capture), or live in
// another terminal (external). openIDs is set synchronously on open/restore, so
// restored sessions count as live immediately, before the first status poll.
func (m *Model) isLive(s index.SessionMeta) bool {
	if m.openIDs[s.SessionID] {
		return true
	}
	if _, ok := m.statusByID8[sessionKey(s)]; ok {
		return true
	}
	_, ext := m.externalStatus[s.SessionID]
	return ext
}

// rebuild regenerates the flat row list from m.all, honoring filters and the
// active search query, then restores the cursor onto the previously-selected
// session. When searching, results are a single fuzzy-ranked list (no groups);
// otherwise sessions are grouped by project, pinned ones first.
func (m *Model) rebuild() {
	switch {
	case strings.TrimSpace(m.query) != "":
		m.rows = m.searchRows()
	case m.sortRecent:
		m.rows = m.recentRows()
	default:
		m.rows = m.groupedRows()
	}
	if c := m.rowForID(m.selID); c >= 0 {
		m.cursor = c
	} else {
		m.cursor = m.firstSessionRow()
	}
	m.syncSelection()
	m.clampScroll()
}

func (m *Model) groupedRows() []row {
	var pinned []index.SessionMeta
	var rest []index.SessionMeta
	for _, s := range m.all {
		if !m.visible(s) {
			continue
		}
		if m.ov.IsPinned(s.SessionID) {
			pinned = append(pinned, s)
		} else {
			rest = append(rest, s)
		}
	}
	var rows []row
	if len(pinned) > 0 {
		rows = append(rows, row{kind: rowHeader, label: "★ pinned", count: len(pinned)})
		for _, s := range pinned {
			rows = append(rows, row{kind: rowSession, sess: s})
		}
	}
	for _, g := range index.GroupByProject(rest) {
		rows = append(rows, row{kind: rowHeader, label: g.Label, count: len(g.Sessions)})
		for _, s := range g.Sessions {
			rows = append(rows, row{kind: rowSession, sess: s})
		}
	}
	return rows
}

// recentRows is a flat list of all visible sessions across projects, newest
// first (m.all is already recency-sorted by the index). Rows show their project
// inline since there are no group headers.
func (m *Model) recentRows() []row {
	var ss []index.SessionMeta
	for _, s := range m.all {
		if m.visible(s) {
			ss = append(ss, s)
		}
	}
	rows := make([]row, 0, len(ss)+1)
	rows = append(rows, row{kind: rowHeader, label: "⟳ recent activity", count: len(ss)})
	for _, s := range ss {
		rows = append(rows, row{kind: rowSession, sess: s})
	}
	return rows
}

// searchRows fuzzy-matches the query against "name · project" for each visible
// session and returns the ranked results as a flat list.
func (m *Model) searchRows() []row {
	var cand []index.SessionMeta
	var hay []string
	for _, s := range m.all {
		if !m.visible(s) {
			continue
		}
		cand = append(cand, s)
		hay = append(hay, m.displayName(s)+" · "+s.ProjectLabel())
	}
	matches := fuzzy.Find(m.query, hay)
	rows := make([]row, 0, len(matches))
	for _, mt := range matches {
		rows = append(rows, row{kind: rowSession, sess: cand[mt.Index]})
	}
	return rows
}

// --- new session launch + adoption ---

func normalizeApp(app string) string {
	if app == index.AppCodex {
		return index.AppCodex
	}
	return index.AppClaude
}

func toggleApp(app string) string {
	if normalizeApp(app) == index.AppCodex {
		return index.AppClaude
	}
	return index.AppCodex
}

func newPrompt(app string) string {
	return "new " + brandGlyph(index.SessionMeta{App: normalizeApp(app)}) + " in: "
}

// launchNew opens a brand-new agent session in cwd and records a pending
// adoption so the real session id can be bound once the app writes metadata.
func (m Model) launchNew(cwd, app string) (tea.Model, tea.Cmd) {
	cwd = expandHome(strings.TrimSpace(cwd))
	if cwd == "" {
		return m, nil
	}
	app = normalizeApp(app)
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		var c tea.Cmd
		m.status, c = flash("no such directory: " + cwd)
		return m, c
	}
	tmpID := "new" + itoa(time.Now().UnixNano())
	_ = tmux.Unzoom() // launching returns to the split
	if err := tmux.LaunchNew(cwd, tmpID, m.actualShownRef(), app); err != nil {
		var c tea.Cmd
		m.status, c = flash("error: " + err.Error())
		return m, c
	}
	m.shown = tmpID
	m.pendingNew = &pendingNew{cwd: cwd, app: app, since: time.Now()}
	var c tea.Cmd
	m.status, c = flash("＋ new " + brandGlyph(index.SessionMeta{App: app}) + " in " + cwd)
	return m, tea.Batch(c, sendFullscreen(tmpID, app), scanCmd(m.store))
}

// pendingNewGrace is how long we keep trying to adopt a just-launched session
// before giving up, so an unexpected cwd form can't pin pendingNew forever (which
// would also block adoptShownID's pid-based self-heal). 15s is far longer than a
// new session takes to write its first transcript line.
const pendingNewGrace = 15 * time.Second

// findPendingNew picks the freshly-launched session: same working directory and a
// transcript written since the launch, latest activity winning. Paths are
// compared after filepath.Clean so a trailing slash (e.g. from tab-completion)
// can't cause a miss.
func findPendingNew(all []index.SessionMeta, cwd string, since time.Time, app string) (index.SessionMeta, bool) {
	want := filepath.Clean(cwd)
	app = normalizeApp(app)
	var best index.SessionMeta
	var found bool
	for _, s := range all {
		if s.AppName() != app {
			continue
		}
		if filepath.Clean(s.Cwd) != want {
			continue
		}
		if s.FileMtime.Before(since) {
			continue
		}
		if !found || s.LastActive.After(best.LastActive) {
			best, found = s, true
		}
	}
	return best, found
}

// reconcilePendingNew looks for the transcript a freshly-launched session has
// started writing and adopts its real id (renaming the placeholder so future
// switching addresses the same process). Returns a flash cmd on adoption.
func (m *Model) reconcilePendingNew() tea.Cmd {
	if m.pendingNew == nil {
		return nil
	}
	best, found := findPendingNew(m.all, m.pendingNew.cwd, m.pendingNew.since, m.pendingNew.app)
	if !found {
		// Give up after a grace period so a never-matching launch can't pin
		// pendingNew (and block adoptShownID, which then adopts via the pane pid).
		if time.Since(m.pendingNew.since) > pendingNewGrace {
			m.pendingNew = nil
		}
		return nil
	}
	tmpID := m.shown
	tmux.AdoptParked(tmpID, best.SessionID, best.AppName())
	if m.shown == tmpID {
		m.shown = best.SessionID
	}
	m.selID = best.SessionID
	m.openIDs[best.SessionID] = true
	m.pendingNew = nil
	m.persistWorkspace()
	var c tea.Cmd
	m.status, c = flash("▶ " + m.displayName(best))
	return c
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

// completeDirPath does shell-style Tab completion of a directory path: a single
// match completes fully (with a trailing slash to descend); multiple matches
// complete to their longest common prefix. Only directories are offered, since
// the field is a working directory.
func completeDirPath(partial string) string {
	if partial == "" {
		return partial
	}
	expanded := partial
	if strings.HasPrefix(expanded, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = home + strings.TrimPrefix(expanded, "~")
		}
	}
	var dir, base string
	if strings.HasSuffix(expanded, "/") {
		dir, base = expanded, ""
	} else {
		dir, base = filepath.Dir(expanded), filepath.Base(expanded)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return partial
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), base) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return partial
	case 1:
		return filepath.Join(dir, matches[0]) + "/"
	default:
		lcp := matches[0]
		for _, mm := range matches[1:] {
			lcp = commonPrefix(lcp, mm)
		}
		if lcp == base {
			return partial // already at the common prefix; nothing to add
		}
		return filepath.Join(dir, lcp)
	}
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// itoa is a tiny non-negative int64 formatter (avoids strconv import churn).
func itoa(n int64) string {
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
