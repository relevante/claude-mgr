package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"claude-mgr/internal/index"
	"claude-mgr/internal/tmux"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")).Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	footStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	// Status dots — color encodes WHERE (your dashboard = colored, elsewhere =
	// gray), glyph + color encode WHAT. See statusMark.
	workStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))             // working (green): Claude busy
	attnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))            // needs permission (red): your turn
	idleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")) // open here, idle (bright white)
	awayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))            // running elsewhere (darker gray)
	dormantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("236"))            // dormant (dimmest gray)
	// shownGutter marks the session shown on the right with a bar in the left
	// gutter — not a background, and not a triangle (which means "working").
	shownGutter = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))

	// Context-fill pie: muted until it gets concerning.
	pieLow   = lipgloss.NewStyle().Foreground(lipgloss.Color("66"))  // calm slate
	pieAmber = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // ~75%+
	pieRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")) // ~90%+

	inputStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	detachConfirm = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("220"))
	quitConfirm   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160"))
)

// --- navigation ---

func (m *Model) moveCursor(d int) {
	if len(m.rows) == 0 {
		return
	}
	i := m.cursor
	for {
		i += d
		if i < 0 || i >= len(m.rows) {
			return // hit an end; leave cursor where it was
		}
		if m.rows[i].kind == rowSession {
			m.cursor = i
			m.syncSelection()
			m.clampScroll()
			return
		}
	}
}

// moveBy steps the cursor n session-rows in direction dir (±1).
func (m *Model) moveBy(dir, n int) {
	for i := 0; i < n; i++ {
		m.moveCursor(dir)
	}
}

// pageStep is how far Ctrl-d/Ctrl-u (and PgUp/PgDn) jump — half a screen.
func (m *Model) pageStep() int {
	if s := m.viewportHeight() / 2; s > 1 {
		return s
	}
	return 1
}

// needsAttention reports whether a session is worth jumping to: working,
// awaiting a permission answer, or finished in the background (green dot).
func (m *Model) needsAttention(s index.SessionMeta) bool {
	id8 := tmux.Short(s.SessionID)
	if st, ok := m.statusByID8[id8]; ok && (st == index.StatusWorking || st == index.StatusPermission) {
		return true
	}
	return m.doneIDs[id8]
}

// jumpAttention moves the cursor to the next/prev session needing attention.
// It reports whether the cursor actually moved (false = none in that direction).
func (m *Model) jumpAttention(dir int) bool {
	i := m.cursor
	for {
		i += dir
		if i < 0 || i >= len(m.rows) {
			return false // none in that direction
		}
		if m.rows[i].kind == rowSession && m.needsAttention(m.rows[i].sess) {
			m.cursor = i
			m.syncSelection()
			m.clampScroll()
			return true
		}
	}
}

func (m *Model) firstSessionRow() int {
	for i, r := range m.rows {
		if r.kind == rowSession {
			return i
		}
	}
	return 0
}

func (m *Model) lastSessionRow() int {
	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].kind == rowSession {
			return i
		}
	}
	return 0
}

func (m *Model) rowForID(id string) int {
	if id == "" {
		return -1
	}
	for i, r := range m.rows {
		if r.kind == rowSession && r.sess.SessionID == id {
			return i
		}
	}
	return -1
}

func (m *Model) syncSelection() {
	if s, ok := m.currentSession(); ok {
		m.selID = s.SessionID
	}
}

func (m *Model) currentSession() (index.SessionMeta, bool) {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == rowSession {
		return m.rows[m.cursor].sess, true
	}
	return index.SessionMeta{}, false
}

func (m *Model) viewportHeight() int {
	h := m.height - 2 // title + footer
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampScroll() {
	vp := m.viewportHeight()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+vp {
		m.scroll = m.cursor - vp + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// --- view ---

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	w := m.width

	var b strings.Builder
	b.WriteString(titleStyle.Width(w).Render("claude-mgr"))
	b.WriteByte('\n')

	if m.err != nil {
		b.WriteString(attnStyle.Render(truncate("error: "+m.err.Error(), w)))
		return b.String()
	}
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("no sessions found"))
		return b.String()
	}

	vp := m.viewportHeight()
	end := m.scroll + vp
	if end > len(m.rows) {
		end = len(m.rows)
	}
	now := time.Now()
	for i := m.scroll; i < end; i++ {
		b.WriteString(m.renderRow(m.rows[i], i == m.cursor, w, now))
		b.WriteByte('\n')
	}
	// pad to keep footer pinned
	for i := end - m.scroll; i < vp; i++ {
		b.WriteByte('\n')
	}

	b.WriteString(m.footer(w))
	return b.String()
}

func (m Model) renderRow(r row, selected bool, w int, now time.Time) string {
	if r.kind == rowHeader {
		label := fmt.Sprintf("%s (%d)", r.label, r.count)
		return headerStyle.Render(truncate(label, w))
	}
	s := r.sess
	mark, markStyle := m.statusMark(s)

	// Right-side meta: relative time, prefixed with the project in recent mode
	// (which has no group headers to show it). The project is reverse-truncated
	// so the most specific tail of the path survives.
	rel := index.RelTime(s.LastActive, now)
	meta := rel
	if m.sortRecent && m.query == "" {
		if p := s.ProjectLabel(); p != "" {
			meta = truncateLeft(p, projCap(w)) + " · " + rel
		}
	}

	// Context-fill pie, shown just left of the time.
	pie, pieStyle := contextPie(s)
	rightPlain := meta
	if pie != "" {
		rightPlain = pie + " " + meta
	}

	// Left gutter: a bar marks the session shown on the right.
	gut := " "
	if s.SessionID == m.shown {
		gut = "▌"
	}
	prefix := gut + " " + mark + " " // width 4
	avail := w - lipgloss.Width(prefix) - lipgloss.Width(rightPlain) - 1
	if avail < 4 {
		avail = 4
	}
	name := m.displayName(s)
	if m.ov.IsPinned(s.SessionID) {
		name = "★ " + name
	}
	title := truncate(name, avail)
	pad := avail - lipgloss.Width(title)
	if pad < 1 {
		pad = 1
	}
	gap := strings.Repeat(" ", pad)

	// Render each segment separately so the icons keep their own color. On the
	// selected row, every segment also gets the selection background, so the
	// cursor highlight spans the row without flattening the icon colors.
	gutSty, markSty, pieSty := shownGutter, markStyle, pieStyle
	txtSty := lipgloss.NewStyle() // title
	metaSty := dimStyle           // time / project
	spSty := lipgloss.NewStyle()  // separators + padding
	if selected {
		bg := lipgloss.Color("238")
		gutSty = gutSty.Background(bg)
		markSty = markSty.Background(bg)
		pieSty = pieSty.Background(bg)
		txtSty = txtSty.Bold(true).Foreground(lipgloss.Color("231")).Background(bg)
		metaSty = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(bg)
		spSty = spSty.Background(bg)
	}

	var b strings.Builder
	b.WriteString(gutSty.Render(gut))
	b.WriteString(spSty.Render(" "))
	b.WriteString(markSty.Render(mark))
	b.WriteString(spSty.Render(" "))
	b.WriteString(txtSty.Render(title))
	b.WriteString(spSty.Render(gap + " "))
	if pie != "" {
		b.WriteString(pieSty.Render(pie))
		b.WriteString(spSty.Render(" "))
	}
	b.WriteString(metaSty.Render(meta))
	return b.String()
}

// contextLimit is the assumed context-window size (tokens); defaults to 1M.
const contextLimit = 1_000_000

// contextPie returns a quarter-filled circle for the session's context usage,
// colored neutral → amber → red as it fills. Empty for sessions with no turn.
func contextPie(s index.SessionMeta) (string, lipgloss.Style) {
	if s.ContextTokens <= 0 {
		return "", lipgloss.Style{}
	}
	frac := float64(s.ContextTokens) / contextLimit
	if frac > 1 {
		frac = 1
	}
	levels := []string{"○", "◔", "◑", "◕", "●"}
	idx := int(frac*4 + 0.5)
	if idx > 4 {
		idx = 4
	}
	st := pieLow
	switch {
	case frac >= 0.90:
		st = pieRed
	case frac >= 0.75:
		st = pieAmber
	}
	return levels[idx], st
}

// projCap is the column budget for the inline project label in recent mode,
// scaled to the rail width so it isn't needlessly truncated when there's room.
func projCap(w int) int {
	c := w / 3
	if c < 12 {
		c = 12
	}
	if c > 30 {
		c = 30
	}
	return c
}

// statusMark chooses the status glyph + style for a session. Color encodes
// WHERE it runs (your dashboard = colored, another terminal = gray, nowhere =
// dim hollow); glyph + color encode WHAT it's doing. "Shown on the right" is a
// separate row background (renderRow), so status stays visible for that row too.
func (m Model) statusMark(s index.SessionMeta) (string, lipgloss.Style) {
	// In your dashboard: colored, from real captured status.
	if st, ok := m.statusByID8[tmux.Short(s.SessionID)]; ok {
		switch st {
		case index.StatusWorking:
			return "▶", workStyle // green play: Claude busy
		case index.StatusPermission:
			return "⚠", attnStyle // red: your turn
		case index.StatusWaiting:
			return "◐", attnStyle // red: your turn
		default:
			if m.doneIDs[tmux.Short(s.SessionID)] {
				return "●", workStyle // finished in the background — go check (green)
			}
			return "●", idleStyle // open here, idle (white)
		}
	}
	// Running in another terminal: gray; glyph still shows busy vs idle.
	if st, ok := m.externalStatus[s.SessionID]; ok {
		if st == "busy" {
			return "▶", awayStyle
		}
		return "●", awayStyle
	}
	return "○", dormantStyle // dormant — nothing running
}

func (m Model) footer(w int) string {
	switch m.confirmQuit {
	case quitDetach:
		return detachConfirm.Width(w).Render(truncate("detach? (sessions keep running)  y = yes · any key = no", w))
	case quitKill:
		return quitConfirm.Width(w).Render(truncate("QUIT & close all sessions?  y = yes · any key = no", w))
	}
	if m.mode != modeNormal {
		// Show the active text input (search/rename/new).
		return inputStyle.Render(truncate(m.input.Prompt+m.input.Value()+"▏", w))
	}
	if m.status != "" {
		return footStyle.Render(truncate(m.status, w))
	}
	help := "↵ open · / find · s recent · f active · r name · n new · q detach · Q quit"
	return footStyle.Render(truncate(help, w))
}

// truncateLeft shortens s to at most w columns by dropping from the FRONT,
// keeping the tail (e.g. "…/sensorpush-sensor-esp") where the detail lives.
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// truncate shortens s to at most w display columns, adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
