// Package ui implements the controller rail: the left-hand Bubble Tea TUI that
// lists sessions grouped by project and drives the tmux right pane.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"claude-mgr/internal/focus"
	"claude-mgr/internal/index"
	"claude-mgr/internal/live"
	"claude-mgr/internal/overlay"
	"claude-mgr/internal/sound"
	"claude-mgr/internal/status"
	"claude-mgr/internal/tmux"
	"claude-mgr/internal/watch"
	"claude-mgr/internal/workspace"
)

// maxRestore caps how many sessions are relaunched on startup, to avoid a
// thundering herd of claude processes.
const maxRestore = 20

// inputMode is the controller's interaction mode.
type inputMode int

const (
	modeNormal inputMode = iota
	modeSearch
	modeRename
	modeNew
)

// pendingQuit is a quit/detach awaiting y/n confirmation, so a stray keypress
// (e.g. typing in the wrong pane) can't tear things down.
type pendingQuit int

const (
	quitNone pendingQuit = iota
	quitDetach
	quitKill
)

const refreshEvery = 1500 * time.Millisecond

// rowKind distinguishes project headers from session entries in the flat list.
type rowKind int

const (
	rowHeader rowKind = iota
	rowSession
)

type row struct {
	kind  rowKind
	label string // header text
	count int    // header session count
	sess  index.SessionMeta
}

// Model is the controller state.
type Model struct {
	store *index.Store
	ov    *overlay.Overlay

	all    []index.SessionMeta // latest scan, unfiltered
	rows   []row
	cursor int    // index into rows; always points at a rowSession
	shown  string // sessionId currently displayed on the right
	selID  string // sessionId under the cursor (preserved across refreshes)

	scroll int // first visible row (viewport top)
	width  int
	height int

	mode  inputMode
	input textinput.Model
	query string

	hideEmpty    bool
	showArchived bool
	activeOnly   bool        // show only sessions with live activity
	sortRecent   bool        // flat, recency-sorted across projects (vs grouped)
	confirmQuit  pendingQuit // a detach/quit awaiting confirmation
	status       string      // transient status line
	err          error

	pendingNew *pendingNew // a just-launched session awaiting id discovery

	wsPath         string           // workspace file path
	openIDs        map[string]bool  // sessions open in the dashboard this run
	liveMiss       map[string]int   // consecutive polls an open session went missing
	answeredResume map[string]int64 // id8 → unix-ms we last auto-answered its resume prompt
	restored       bool             // workspace restore attempted

	// Live state, refreshed by the status poller.
	statusByID8    map[string]index.Status // sessions running in our tmux (from capture-pane)
	externalStatus map[string]string       // ids live in other terminals → "busy"/"idle"
	doneIDs        map[string]bool         // id8 → finished in the background since last viewed

	// fsEvents fires when the session registry changes, so status refreshes
	// instantly instead of waiting for the poll. nil if watching is unavailable.
	fsEvents <-chan struct{}

	sound   string // selected completion chime ("" = off; persisted); cycled with 'c'
	focused bool   // our terminal app is frontmost (refreshed by the status poll via lsappinfo)
}

// pendingNew tracks a brand-new session launched before its id is known.
type pendingNew struct {
	cwd   string
	since time.Time
}

// New builds the initial model.
func New(store *index.Store) Model {
	ti := textinput.New()
	ti.Prompt = ""
	wsPath := workspace.DefaultPath()
	m := Model{
		store:      store,
		ov:         overlay.Load(overlay.DefaultPath()),
		hideEmpty:  true,
		activeOnly: true, // default to the "active only" filter; f toggles to all
		input:      ti,
		wsPath:     wsPath,
		openIDs:    map[string]bool{},
		sound:      workspace.Load(wsPath).Sound, // restore the chime selection
		focused:    true,                         // assume focused until a blur says otherwise
	}
	// Event-drive status off the registry dir; fall back to polling on error.
	if w, err := watch.NewRegistry(live.SessionsDir(store.ProjectsDir)); err == nil {
		m.fsEvents = w.Events()
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{scanCmd(m.store), tick(), statusTick(m.statusInterval()),
		func() tea.Msg {
			_ = tmux.SetControllerTitle("claude-mgr")
			tmux.BindLoadKeys() // global next/prev-and-load keys
			return nil
		}}
	if m.fsEvents != nil {
		cmds = append(cmds, waitForFS(m.fsEvents))
	}
	return tea.Batch(cmds...)
}

// --- messages ---

type sessionsMsg struct {
	sessions []index.SessionMeta
	err      error
}
type tickMsg struct{}
type fsEventMsg struct{} // the session registry changed on disk
type fullscreenMsg struct{ id string }
type statusClearMsg struct{}
type statusTickMsg struct{}
type statusMsg struct {
	byID8       map[string]index.Status
	external    map[string]string // id → "busy"/"idle" for sessions live elsewhere
	resume      map[string]string // id8 → paneID showing the resume summary/full prompt
	shownActual string            // the shown pane's current session id (may differ after /clear)
	focused     bool              // our terminal app is frontmost (for the chime)
}

const (
	statusEvery     = 800 * time.Millisecond // poll cadence when watching is unavailable
	statusSafetyNet = 5 * time.Second        // slow self-heal poll when fs events drive status
)

// statusInterval is the poll cadence: a fast loop normally, but only a slow
// safety-net once registry file events drive the refreshes.
func (m Model) statusInterval() time.Duration {
	if m.fsEvents != nil {
		return statusSafetyNet
	}
	return statusEvery
}

func statusTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// waitForFS blocks until the registry changes, then asks for a status refresh.
func waitForFS(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return fsEventMsg{}
	}
}

// pollStatus reads each live session's activity and maps external claude
// processes to their sessions. Runs off the UI thread as a tea.Cmd.
//
// Status comes from Claude's own pid registry (idle/busy/waiting), which is
// authoritative and real-time; we only scrape the pane as a fallback (a session
// not yet in the registry) and to refine a generic "waiting" into a specific
// permission ⚠ and to spot the resume prompt.
func pollStatus(store *index.Store, shown string) tea.Cmd {
	return func() tea.Msg {
		reg := live.Statuses(store.ProjectsDir)
		regByShort := make(map[string]index.Status, len(reg))
		for id, st := range reg {
			regByShort[tmux.Short(id)] = status.FromRegistry(st)
		}
		// classify resolves a pane's status: prefer the registry flag, fall back to
		// pane text; a confirmed permission dialog upgrades waiting (◐) → ⚠.
		classify := func(id8, txt string) index.Status {
			st, ok := regByShort[id8]
			return status.Resolve(st, ok, txt)
		}

		byID8 := map[string]index.Status{}
		resume := map[string]string{}
		if parked, err := tmux.ParkedPanes(); err == nil {
			for _, p := range parked {
				txt, _ := tmux.CapturePane(p.PaneID, 8)
				byID8[p.ID8] = classify(p.ID8, txt)
				if status.IsResumePrompt(txt) {
					resume[p.ID8] = p.PaneID
				}
			}
		}
		if shown != "" {
			if txt, err := tmux.CaptureSession(8); err == nil {
				byID8[tmux.Short(shown)] = classify(tmux.Short(shown), txt)
				if status.IsResumePrompt(txt) {
					if pid, ok := tmux.SessionPaneID(); ok {
						resume[tmux.Short(shown)] = pid
					}
				}
			}
		}
		external := map[string]string{}
		for id, st := range reg {
			if _, inTmux := byID8[tmux.Short(id)]; !inTmux {
				external[id] = st
			}
		}
		// Always read the shown pane's real session id — even when we think nothing
		// is shown — so an orphaned pane (failed adoption / reaped id) gets
		// re-adopted instead of rendering as "running elsewhere".
		shownActual := ""
		if pid, ok := tmux.SessionPanePID(); ok {
			shownActual = live.SessionForPID(store.ProjectsDir, pid)
		}
		return statusMsg{byID8: byID8, external: external, resume: resume, shownActual: shownActual,
			focused: focus.Focused(tmux.Socket)}
	}
}

func scanCmd(store *index.Store) tea.Cmd {
	return func() tea.Msg {
		s, err := store.Scan()
		return sessionsMsg{sessions: s, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func sendFullscreen(id string) tea.Cmd {
	// Fullscreen is the default renderer in current Claude, so we normally do
	// NOT force it — sending "/tui fullscreen" otherwise leaves a visible
	// "Already using the fullscreen renderer" line in the conversation. Opt in
	// via CLAUDE_MGR_FORCE_FULLSCREEN for setups whose default is classic.
	if os.Getenv("CLAUDE_MGR_FORCE_FULLSCREEN") == "" {
		return nil
	}
	return tea.Tick(2500*time.Millisecond, func(time.Time) tea.Msg { return fullscreenMsg{id: id} })
}

func flash(s string) (string, tea.Cmd) {
	return s, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{} })
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		// Re-pin the rail width so a font-size change can't shrink it.
		return m, func() tea.Msg { tmux.RePin(); return nil }

	case sessionsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.all = msg.sessions
		cmd := m.reconcilePendingNew()
		m.rebuild()
		var rcmd tea.Cmd
		if !m.restored {
			m.restored = true
			rcmd = m.restoreWorkspace()
		}
		return m, tea.Batch(cmd, rcmd)

	case tickMsg:
		return m, tea.Batch(scanCmd(m.store), tick())

	case statusTickMsg:
		return m, tea.Batch(pollStatus(m.store, m.shown), statusTick(m.statusInterval()))

	case fsEventMsg:
		// A registry file changed — refresh status now and keep listening.
		return m, tea.Batch(pollStatus(m.store, m.shown), waitForFS(m.fsEvents))

	case statusMsg:
		m.focused = msg.focused
		if m.sound != "" && m.anyChimeWorthy(msg.byID8) {
			sound.Play(m.sound) // an agent stopped working (done / needs you) off-screen
		}
		m.markCompleted(msg.byID8) // background working→idle = "done, go check" (green)
		m.statusByID8 = msg.byID8
		m.externalStatus = msg.external
		m.adoptShownID(msg.shownActual)          // /clear changed the shown session's id
		answer := m.autoAnswerResume(msg.resume) // pick "full session as-is"
		reaped := m.reconcileLive()              // clean up sessions exited inside the dashboard
		if m.activeOnly || reaped {
			m.rebuild()
		}
		return m, answer

	case fullscreenMsg:
		if msg.id == m.shown {
			return m, func() tea.Msg { _ = tmux.SendSession("/tui fullscreen"); return nil }
		}
		return m, nil

	case statusClearMsg:
		m.status = ""
		return m, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.moveBy(-1, 3)
		case tea.MouseButtonWheelDown:
			m.moveBy(1, 3)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global "move one item and load it" — delivered here even while the Claude
	// pane is focused (via a tmux send-keys binding), so it works in any mode.
	switch msg.String() {
	case "alt+down":
		if m.moveWrap(1) {
			return m.showSelected()
		}
		return m, nil
	case "alt+up":
		if m.moveWrap(-1) {
			return m.showSelected()
		}
		return m, nil
	case "alt+'":
		if m.jumpAttention(-1) {
			return m.showSelected()
		}
		return m, nil
	case "alt+/":
		if m.jumpAttention(1) {
			return m.showSelected()
		}
		return m, nil
	case "alt+t":
		if cwd := m.openCwd(); cwd != "" {
			return m, func() tea.Msg { _ = openTerminal(cwd); return nil }
		}
		return m, nil
	}
	if m.mode != modeNormal {
		return m.handleInputKey(msg)
	}
	if m.confirmQuit != quitNone {
		return m.handleConfirm(msg)
	}
	switch msg.String() {
	case "q":
		// Detach (background) — confirmed, so a stray key can't drop you out.
		m.confirmQuit = quitDetach
		return m, nil
	case "Q":
		// Full quit: tear down the dashboard and every session pane (all still
		// resumable from disk). Confirmed.
		m.confirmQuit = quitKill
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "ctrl+d", "pgdown":
		m.moveBy(1, m.pageStep())
	case "ctrl+u", "pgup":
		m.moveBy(-1, m.pageStep())
	case "g", "home":
		m.cursor = m.firstSessionRow()
		m.syncSelection()
		m.clampScroll()
	case "G", "end":
		m.cursor = m.lastSessionRow()
		m.syncSelection()
		m.clampScroll()
	case "enter":
		return m.showSelected()
	case "tab", "l", "right":
		return m, func() tea.Msg { _ = tmux.FocusSession(); return nil }
	case "z":
		// Zoom the session fullscreen and move focus into it so the user can
		// type immediately. Un-zoom is a tmux-level key (Option+z / Ctrl-b z),
		// since once zoomed the rail is hidden and can't catch a keypress.
		return m, func() tea.Msg { _ = tmux.Zoom(); _ = tmux.FocusSession(); return nil }
	case "/":
		return m.enterInput(modeSearch, "", "search: ")
	case "r":
		if s, ok := m.currentSession(); ok {
			return m.enterInput(modeRename, m.displayName(s), "rename: ")
		}
	case "n":
		cwd := ""
		if s, ok := m.currentSession(); ok {
			cwd = s.Cwd
		}
		return m.enterInput(modeNew, cwd, "new in: ")
	case "p":
		if s, ok := m.currentSession(); ok {
			_ = m.ov.TogglePinned(s.SessionID)
			m.rebuild()
		}
	case "a":
		if s, ok := m.currentSession(); ok {
			_ = m.ov.ToggleArchived(s.SessionID)
			// Flash the result: archiving hides the row instantly, so without
			// feedback an accidental 'a' looks like the session vanished.
			var c tea.Cmd
			if m.ov.IsArchived(s.SessionID) {
				m.status, c = flash("archived: " + m.displayName(s) + " · A shows · a undoes")
			} else {
				m.status, c = flash("unarchived: " + m.displayName(s))
			}
			m.rebuild()
			return m, c
		}
	case "A":
		m.showArchived = !m.showArchived
		m.rebuild()
	case "e":
		m.hideEmpty = !m.hideEmpty
		m.rebuild()
	case "f":
		m.activeOnly = !m.activeOnly
		var c tea.Cmd
		if m.activeOnly {
			m.status, c = flash("filter: active only")
		} else {
			m.status, c = flash("filter: all sessions")
		}
		m.rebuild()
		return m, c
	case "s":
		m.sortRecent = !m.sortRecent
		var c tea.Cmd
		if m.sortRecent {
			m.status, c = flash("sort: recent activity")
		} else {
			m.status, c = flash("sort: by project")
		}
		m.rebuild()
		return m, c
	case "c":
		m.sound = sound.Next(m.sound) // off → each sound → off
		m.persistWorkspace()
		var c tea.Cmd
		if m.sound == "" {
			m.status, c = flash("🔕 chime: off")
		} else {
			m.status, c = flash("🔔 chime: " + m.sound)
			sound.Play(m.sound) // preview the newly-selected sound
		}
		return m, c
	}
	return m, nil
}

// handleConfirm resolves a pending detach/quit: y/enter acts, anything else cancels.
func (m Model) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	act := m.confirmQuit
	m.confirmQuit = quitNone
	switch msg.String() {
	case "y", "Y", "enter":
		switch act {
		case quitDetach:
			return m, func() tea.Msg { _ = tmux.Detach(); return nil }
		case quitKill:
			return m, func() tea.Msg { _ = tmux.KillServer(); return nil }
		}
	}
	return m, nil // any other key cancels
}

// enterInput switches into a text-input mode with an initial value and prompt.
func (m Model) enterInput(mode inputMode, value, prompt string) (tea.Model, tea.Cmd) {
	m.mode = mode
	m.input.Prompt = prompt
	m.input.SetValue(value)
	m.input.CursorEnd()
	if mode == modeSearch {
		m.input.SetValue("")
		m.query = ""
	}
	return m, m.input.Focus()
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// First Esc clears a non-empty entry; a second Esc cancels the mode.
		if m.input.Value() != "" {
			m.input.SetValue("")
			if m.mode == modeSearch {
				m.query = ""
				m.rebuild()
			}
			return m, nil
		}
		wasSearch := m.mode == modeSearch
		m.mode = modeNormal
		m.input.Blur()
		if wasSearch {
			m.query = ""
			m.rebuild()
		}
		return m, nil
	case "tab":
		if m.mode == modeNew {
			m.input.SetValue(completeDirPath(m.input.Value()))
			m.input.CursorEnd()
		}
		return m, nil
	case "up":
		m.moveCursor(-1)
		return m, nil
	case "down":
		m.moveCursor(1)
		return m, nil
	case "enter":
		return m.commitInput()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.mode == modeSearch {
		m.query = m.input.Value()
		m.rebuild()
	}
	return m, cmd
}

func (m Model) commitInput() (tea.Model, tea.Cmd) {
	val := m.input.Value()
	mode := m.mode
	switch mode {
	case modeSearch:
		m.mode = modeNormal
		m.input.Blur()
		return m.showSelected() // open the highlighted match (keeps the filter)
	case modeRename:
		if s, ok := m.currentSession(); ok {
			_ = m.ov.SetName(s.SessionID, val)
		}
		m.mode = modeNormal
		m.input.Blur()
		m.rebuild()
		return m, nil
	case modeNew:
		m.mode = modeNormal
		m.input.Blur()
		return m.launchNew(val)
	}
	return m, nil
}

func (m Model) showSelected() (tea.Model, tea.Cmd) {
	s, ok := m.currentSession()
	if !ok {
		return m, nil
	}
	prev := m.actualShownID() // the pane's real id now (may have /clear'd) — park it correctly
	_ = tmux.Unzoom()         // opening a session always returns to the split
	created, err := tmux.ShowSession(tmux.SessionRef{ID: s.SessionID, Cwd: s.Cwd}, prev)
	if err != nil {
		var c tea.Cmd
		m.status, c = flash("error: " + err.Error())
		return m, c
	}
	m.shown = s.SessionID
	m.openIDs[s.SessionID] = true
	delete(m.doneIDs, tmux.Short(s.SessionID)) // viewing it clears the "go check" green
	m.persistWorkspace()
	name := m.displayName(s)
	var cmds []tea.Cmd
	cmds = append(cmds, func() tea.Msg { _ = tmux.SetSessionTitle(name); return nil })
	if created {
		cmds = append(cmds, sendFullscreen(s.SessionID))
	}
	var c tea.Cmd
	m.status, c = flash("▶ " + m.displayName(s))
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

// actualShownID returns the session id the shown pane is really running right
// now (from Claude's process registry), falling back to m.shown. Used so we
// park a session under a window name that matches its content, even if it
// /clear'd since the last poll.
func (m *Model) actualShownID() string {
	if m.shown == "" {
		return ""
	}
	if pid, ok := tmux.SessionPanePID(); ok {
		if actual := live.SessionForPID(m.store.ProjectsDir, pid); actual != "" {
			return actual
		}
	}
	return m.shown
}

// shouldAdoptShown decides whether the shown pane's real session id (actual)
// should replace what we currently think is shown. It adopts whenever the pane
// is running a different live session than we're tracking — including when we
// think nothing is shown (m.shown==""), which happens if a new-session adoption
// failed or the shown id was reaped, orphaning a pane that's actually displayed.
// Skipped mid new-session launch, which is handled by reconcilePendingNew.
func shouldAdoptShown(actual, shown string, pendingNew bool) bool {
	return actual != "" && actual != shown && !pendingNew
}

// adoptShownID re-points tracking to the session the shown pane is really
// running (e.g. /clear started a fresh id in the same process, or we lost track
// of it). The old id, if any, becomes a normal dormant entry — no duplicate.
func (m *Model) adoptShownID(actual string) {
	if !shouldAdoptShown(actual, m.shown, m.pendingNew != nil) {
		return
	}
	old := m.shown
	m.shown = actual
	m.openIDs[actual] = true
	if old != "" {
		delete(m.openIDs, old)
		if st, ok := m.statusByID8[tmux.Short(old)]; ok {
			m.statusByID8[tmux.Short(actual)] = st // same pane, carry its status to the new id
		}
		delete(m.doneIDs, tmux.Short(old))
		delete(m.liveMiss, old)
	}
	m.persistWorkspace()
	m.rebuild()
}

// dbg appends a diagnostic line to a temp log when CLAUDE_MGR_DEBUG is set;
// otherwise a no-op. Handy for settling chime/focus behavior against real runs.
func dbg(format string, args ...any) {
	if os.Getenv("CLAUDE_MGR_DEBUG") == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "claude-mgr-debug.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}

// chimeForTransition reports whether an active→not-active transition warrants
// the completion chime: the agent stopped (finished, or now needs you) and it's
// either not the session you're viewing, or the window isn't focused. Active
// includes a running background shell, so handing off busy→shell stays silent
// and the chime fires when the whole task lands.
func chimeForTransition(prev, next index.Status, isShown, focused bool) bool {
	if !prev.Active() || next.Active() {
		return false
	}
	return !isShown || !focused
}

// anyChimeWorthy reports whether any session just stopped working in a way that
// should sound the chime, comparing incoming statuses to the current ones.
func (m *Model) anyChimeWorthy(next map[string]index.Status) bool {
	shownID8 := ""
	if m.shown != "" {
		shownID8 = tmux.Short(m.shown)
	}
	worthy := false
	for id8, st := range next {
		prev := m.statusByID8[id8]
		if !prev.Active() || st.Active() {
			continue
		}
		isShown := shownID8 != "" && id8 == shownID8
		c := chimeForTransition(prev, st, isShown, m.focused)
		dbg("stop id8=%s next=%v isShown=%v focused=%v shown8=%s -> chime=%v", id8, st, isShown, m.focused, shownID8, c)
		if c {
			worthy = true
		}
	}
	return worthy
}

// markCompleted flags a session that just went from active (working, or a
// background shell running) to idle while it was NOT the shown one — i.e. it
// finished a run in the background. The flag (rendered as a green dot) clears
// when the session is next opened.
func (m *Model) markCompleted(next map[string]index.Status) {
	shownID8 := ""
	if m.shown != "" {
		shownID8 = tmux.Short(m.shown)
	}
	for id8, cur := range next {
		if id8 == shownID8 {
			continue
		}
		if m.statusByID8[id8].Active() && cur == index.StatusIdle {
			if m.doneIDs == nil {
				m.doneIDs = map[string]bool{}
			}
			m.doneIDs[id8] = true
		}
	}
}

// autoAnswerResume selects "Resume full session as-is" (option 2) on any pane
// showing Claude's resume summary/full prompt. Guarded per session so we answer
// once (the prompt waits for input and won't clear on its own, so there's no
// race; the 10s window just prevents a double-send before it clears).
func (m *Model) autoAnswerResume(resume map[string]string) tea.Cmd {
	if len(resume) == 0 {
		return nil
	}
	if m.answeredResume == nil {
		m.answeredResume = map[string]int64{}
	}
	nowMs := time.Now().UnixMilli()
	var cmds []tea.Cmd
	for id8, pane := range resume {
		if last, ok := m.answeredResume[id8]; ok && nowMs-last < 10000 {
			continue
		}
		m.answeredResume[id8] = nowMs
		p := pane
		// The menu is "arrow to select, Enter to confirm" — number keys do NOT
		// select, so a bare Enter would confirm the DEFAULT (option 1, "resume
		// from summary"), which runs /compact. Move ❯ down once to option 2
		// ("full session as-is"), pause so it registers, then confirm.
		cmds = append(cmds, func() tea.Msg {
			_ = tmux.SendPaneKeys(p, "Down")
			time.Sleep(350 * time.Millisecond)
			_ = tmux.SendPaneKeys(p, "Enter")
			return nil
		})
	}
	return tea.Batch(cmds...)
}

// reconcileLive reaps sessions that exited inside the dashboard (e.g. /exit or
// Ctrl-C closed their pane). A session is open iff it's live in our tmux (its
// id8 appears in the captured status set). We require it to be missing for two
// consecutive polls before dropping it, to tolerate a poll that raced a
// just-opened session. Returns whether anything changed.
func (m *Model) reconcileLive() bool {
	if m.pendingNew != nil {
		return false // a brand-new session is mid-launch; don't reap it
	}
	if m.liveMiss == nil {
		m.liveMiss = map[string]int{}
	}
	changed := false
	for id := range m.openIDs {
		if _, ok := m.statusByID8[tmux.Short(id)]; ok {
			delete(m.liveMiss, id)
			continue
		}
		m.liveMiss[id]++
		if m.liveMiss[id] >= 2 {
			delete(m.openIDs, id)
			delete(m.liveMiss, id)
			delete(m.doneIDs, tmux.Short(id))
			if m.shown == id {
				m.shown = ""
			}
			changed = true
		}
	}
	if changed {
		m.persistWorkspace()
	}
	return changed
}

// openCwd is the working directory to open a terminal in: the shown session's,
// else the cursor selection's.
func (m *Model) openCwd() string {
	if m.shown != "" {
		for _, s := range m.all {
			if s.SessionID == m.shown {
				return s.Cwd
			}
		}
	}
	if s, ok := m.currentSession(); ok {
		return s.Cwd
	}
	return ""
}

// openTerminal opens a new terminal window at dir (macOS `open -a`). The app is
// overridable via CLAUDE_MGR_TERMINAL; CLAUDE_MGR_OPEN_CMD replaces the launcher
// entirely (used by tests).
func openTerminal(dir string) error {
	if custom := os.Getenv("CLAUDE_MGR_OPEN_CMD"); custom != "" {
		return exec.Command(custom, dir).Run()
	}
	app := os.Getenv("CLAUDE_MGR_TERMINAL")
	if app == "" {
		app = "Terminal"
	}
	return exec.Command("open", "-a", app, dir).Run()
}

// persistWorkspace saves the set of open sessions + the shown one.
func (m *Model) persistWorkspace() {
	open := make([]string, 0, len(m.openIDs))
	for id := range m.openIDs {
		open = append(open, id)
	}
	sort.Strings(open)
	_ = workspace.Save(m.wsPath, open, m.shown, m.sound)
}

// restoreWorkspace relaunches the sessions saved from a previous run as parked
// windows and shows the last-shown one. Runs once, after the first scan.
func (m *Model) restoreWorkspace() tea.Cmd {
	saved := workspace.Load(m.wsPath)
	if len(saved.Open) == 0 {
		return nil
	}
	cwd := map[string]string{}
	for _, s := range m.all {
		cwd[s.SessionID] = s.Cwd
	}
	// Don't re-resume a thread that's already running in another terminal —
	// two processes on one session id corrupt the transcript.
	liveElsewhere := live.Sessions(m.store.ProjectsDir)
	var refs []tmux.SessionRef
	for _, id := range saved.Open {
		c := cwd[id]
		if c == "" || liveElsewhere[id] {
			continue // gone from disk, or already live elsewhere
		}
		refs = append(refs, tmux.SessionRef{ID: id, Cwd: c})
		m.openIDs[id] = true
		if len(refs) >= maxRestore {
			break
		}
	}
	if len(refs) == 0 {
		return nil
	}
	tmux.RestoreParked(refs)

	// A controller hot-restart (respawn-pane) leaves the previously-shown
	// session's pane alive in the main window. ShowSession would treat it as an
	// unknown occupant and KILL it — adopt it instead, so a rail restart never
	// interrupts the session you were looking at.
	if pid, ok := tmux.SessionPanePID(); ok {
		if occupant := live.SessionForPID(m.store.ProjectsDir, pid); occupant != "" {
			m.shown = occupant
			m.selID = occupant
			m.openIDs[occupant] = true
			var c tea.Cmd
			m.status, c = flash(fmt.Sprintf("restored %d thread(s)", len(refs)))
			return c
		}
	}

	showID := saved.Shown
	if cwd[showID] == "" {
		showID = refs[0].ID
	}
	created, _ := tmux.ShowSession(tmux.SessionRef{ID: showID, Cwd: cwd[showID]}, "")
	m.shown = showID
	m.selID = showID

	var cmds []tea.Cmd
	if created {
		cmds = append(cmds, sendFullscreen(showID))
	}
	var c tea.Cmd
	m.status, c = flash(fmt.Sprintf("restored %d thread(s)", len(refs)))
	cmds = append(cmds, c)
	return tea.Batch(cmds...)
}
