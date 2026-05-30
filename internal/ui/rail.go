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
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")).Padding(0, 1)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	selStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("238"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	shownStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
	workingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	permStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	externalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("44"))
	footStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	inputStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
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
		b.WriteString(permStyle.Render(truncate("error: "+m.err.Error(), w)))
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

	rel := index.RelTime(s.LastActive, now)
	// layout:  "  <mark> <title>      <rel>"
	prefix := "  " + mark + " "
	avail := w - lipgloss.Width(prefix) - lipgloss.Width(rel) - 1
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
	body := title + strings.Repeat(" ", pad) + " " + rel

	if selected {
		return selStyle.Width(w).Render(prefix + body)
	}
	return markStyle.Render(prefix) + title + strings.Repeat(" ", pad) + " " + dimStyle.Render(rel)
}

// statusMark chooses the status glyph + style for a session: the shown session
// gets ▶; sessions live in our tmux reflect their captured activity; sessions
// live in another terminal show a cyan dot; everything else is a dim ○.
func (m Model) statusMark(s index.SessionMeta) (string, lipgloss.Style) {
	if s.SessionID == m.shown {
		return "▶", shownStyle
	}
	if st, ok := m.statusByID8[tmux.Short(s.SessionID)]; ok {
		switch st {
		case index.StatusWorking:
			return "●", workingStyle
		case index.StatusPermission:
			return "⚠", permStyle
		case index.StatusWaiting:
			return "◐", shownStyle
		default:
			return "●", dimStyle // live in our tmux but idle
		}
	}
	if m.externalIDs[s.SessionID] {
		return "●", externalStyle // live in another terminal
	}
	return "○", dimStyle
}

func (m Model) footer(w int) string {
	if m.mode != modeNormal {
		// Show the active text input (search/rename/new).
		return inputStyle.Render(truncate(m.input.Prompt+m.input.Value()+"▏", w))
	}
	if m.status != "" {
		return footStyle.Render(truncate(m.status, w))
	}
	help := "↵ open · / find · f active · r name · n new · z zoom · q detach · Q quit"
	return footStyle.Render(truncate(help, w))
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
