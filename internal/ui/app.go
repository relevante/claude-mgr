// Package ui implements the controller rail: the left-hand Bubble Tea TUI that
// lists sessions grouped by project and drives the tmux right pane.
package ui

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"claude-mgr/internal/index"
	"claude-mgr/internal/live"
	"claude-mgr/internal/overlay"
	"claude-mgr/internal/status"
	"claude-mgr/internal/tmux"
	"claude-mgr/internal/workspace"
)

// maxRestore caps how many sessions are relaunched on startup, to avoid a
// thundering herd of claude processes.
const maxRestore = 12

// inputMode is the controller's interaction mode.
type inputMode int

const (
	modeNormal inputMode = iota
	modeSearch
	modeRename
	modeNew
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
	activeOnly   bool   // show only sessions with live activity
	status       string // transient status line
	err          error

	pendingNew *pendingNew // a just-launched session awaiting id discovery

	wsPath   string          // workspace file path
	openIDs  map[string]bool // sessions open in the dashboard this run
	restored bool            // workspace restore attempted

	// Live state, refreshed by the status poller.
	statusByID8 map[string]index.Status // sessions running in our tmux (from capture-pane)
	externalIDs map[string]bool         // full ids of sessions live in other terminals
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
	return Model{
		store:     store,
		ov:        overlay.Load(overlay.DefaultPath()),
		hideEmpty: true,
		input:     ti,
		wsPath:    workspace.DefaultPath(),
		openIDs:   map[string]bool{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(scanCmd(m.store), tick(), statusTick(),
		func() tea.Msg { _ = tmux.SetControllerTitle("claude-mgr"); return nil })
}

// --- messages ---

type sessionsMsg struct {
	sessions []index.SessionMeta
	err      error
}
type tickMsg struct{}
type fullscreenMsg struct{ id string }
type statusClearMsg struct{}
type statusTickMsg struct{}
type statusMsg struct {
	byID8    map[string]index.Status
	external map[string]bool
}

const statusEvery = 800 * time.Millisecond

func statusTick() tea.Cmd {
	return tea.Tick(statusEvery, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// pollStatus scrapes our tmux panes for live status and maps external claude
// processes to their sessions. Runs off the UI thread as a tea.Cmd.
func pollStatus(store *index.Store, shown string) tea.Cmd {
	return func() tea.Msg {
		byID8 := map[string]index.Status{}
		if parked, err := tmux.ParkedPanes(); err == nil {
			for _, p := range parked {
				txt, _ := tmux.CapturePane(p.PaneID, 8)
				byID8[p.ID8] = status.Classify(txt)
			}
		}
		if shown != "" {
			if txt, err := tmux.CaptureSession(8); err == nil {
				byID8[tmux.Short(shown)] = status.Classify(txt)
			}
		}
		external := map[string]bool{}
		for id := range live.Sessions(store.ProjectsDir) {
			if _, inTmux := byID8[tmux.Short(id)]; !inTmux {
				external[id] = true
			}
		}
		return statusMsg{byID8: byID8, external: external}
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
		return m, tea.Batch(pollStatus(m.store, m.shown), statusTick())

	case statusMsg:
		m.statusByID8 = msg.byID8
		m.externalIDs = msg.external
		if m.activeOnly {
			m.rebuild() // liveness changed which rows qualify
		}
		return m, nil

	case fullscreenMsg:
		if msg.id == m.shown {
			return m, func() tea.Msg { _ = tmux.SendSession("/tui fullscreen"); return nil }
		}
		return m, nil

	case statusClearMsg:
		m.status = ""
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m.handleInputKey(msg)
	}
	switch msg.String() {
	case "q":
		// Detach the client but keep the controller + sessions running in the
		// background tmux session; re-running `claude-mgr` re-attaches.
		return m, func() tea.Msg { _ = tmux.Detach(); return nil }
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
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
			m.rebuild()
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
	}
	return m, nil
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
	prev := m.shown
	_ = tmux.Unzoom() // opening a session always returns to the split
	created, err := tmux.ShowSession(tmux.SessionRef{ID: s.SessionID, Cwd: s.Cwd}, prev)
	if err != nil {
		var c tea.Cmd
		m.status, c = flash("error: " + err.Error())
		return m, c
	}
	m.shown = s.SessionID
	m.openIDs[s.SessionID] = true
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

// persistWorkspace saves the set of open sessions + the shown one.
func (m *Model) persistWorkspace() {
	open := make([]string, 0, len(m.openIDs))
	for id := range m.openIDs {
		open = append(open, id)
	}
	sort.Strings(open)
	_ = workspace.Save(m.wsPath, open, m.shown)
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
	var refs []tmux.SessionRef
	for _, id := range saved.Open {
		c := cwd[id]
		if c == "" {
			continue // session no longer on disk
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
