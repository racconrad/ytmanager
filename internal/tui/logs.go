package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const logLimit = 500

func (m model) wrapLog(s string) []string {
	width := m.terminalWidth()
	margin := 6
	if m.workspaceVisible() && m.wideActivityFooter() {
		_, width = m.activityColumns()
	} else if m.workspaceVisible() {
		margin = ansi.StringWidth("Logs › ")
	}
	return strings.Split(ansi.Wrap(s, max(1, width-margin), ""), "\n")
}

func (m model) logLines() []string {
	var lines []string
	for _, entry := range m.logs {
		lines = append(lines, m.wrapLog(entry)...)
	}
	return lines
}

// latestLog wraps the newest entry so recovery instructions in errors stay readable.
func (m model) latestLog() []string {
	lines := m.wrapLog("Logs › " + m.logs[len(m.logs)-1])
	return lines[:min(m.logPage()+1, len(lines))]
}

func (m *model) scrollLogs(lines int) {
	n := len(m.logLines())
	// Short histories still open one step so the earlier lines become readable.
	limit := max(n-m.logPage(), min(1, n-1))
	m.logScroll = min(max(0, limit), max(0, m.logScroll+lines))
}

func (m model) logsView() string {
	if m.logScroll == 0 {
		return dim.Render(strings.Join(m.latestLog(), "\n"))
	}
	lines, h := m.logLines(), m.logHeight()
	end := max(0, len(lines)-m.logScroll)
	start := max(0, end-h)
	label := fmt.Sprintf("Logs · %d-%d/%d · PgUp/PgDn · Ctrl+End latest", start+1, end, len(lines))
	rows := []string{dim.Render(ansi.Truncate(label, max(1, m.terminalWidth()-2), "…"))}
	for _, line := range lines[start:end] {
		rows = append(rows, dim.Render("  "+line))
	}
	for len(rows) < h+1 {
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

func (m model) compactLog() string {
	if len(m.logs) == 0 {
		return dim.Render("Logs")
	}
	lines := m.wrapLog(m.logs[len(m.logs)-1])
	if m.logScroll > 0 {
		lines = m.logLines()
	}
	line := lines[max(0, len(lines)-1-m.logScroll)]
	return dim.Render("Logs › " + line)
}

func (m model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.prompt != promptNone {
		return m, nil
	}
	direction := 0
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		direction = 1
	case tea.MouseButtonWheelDown:
		direction = -1
	default:
		return m, nil
	}
	logTop := m.terminalHeight() - m.footerHeight(m.help())
	logX, logRows := 0, m.logHeight()+1
	if m.workspaceVisible() {
		logRows = 1
		if m.wideActivityFooter() {
			cat, _ := m.activityColumns()
			logX, logRows = cat+2, m.activityFooterHeight()
		}
	}
	if len(m.logs) > 0 && msg.X >= logX && msg.Y >= logTop && msg.Y < logTop+logRows {
		m.scrollLogs(direction * 3)
		return m, nil
	}
	k := tea.KeyMsg{Type: tea.KeyDown}
	if direction > 0 {
		k.Type = tea.KeyUp
	}
	for range 3 {
		next, _ := m.key(k)
		m = next.(model)
	}
	return m, nil
}
