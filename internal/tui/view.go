package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	accent  = lipgloss.AdaptiveColor{Light: "#6B4E8A", Dark: "#C4A7E7"}
	gray    = lipgloss.AdaptiveColor{Light: "#C2BACB", Dark: "#4E4859"}
	surface = lipgloss.AdaptiveColor{Light: "#EAE2F5", Dark: "#2D253B"}
	logo    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#121017"}).Background(accent).Padding(0, 1)
	dim     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#655D6E", Dark: "#9E96AA"})
	hi      = lipgloss.NewStyle().Foreground(accent).Bold(true)
	tabOn   = lipgloss.NewStyle().Bold(true).Foreground(accent).Background(surface).Padding(0, 1)
	tabOff  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#655D6E", Dark: "#9E96AA"}).Padding(0, 1)
	ok      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#166534", Dark: "#7EE787"})
	warn    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#92400E", Dark: "#F2CC60"})
	bad     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#991B1B", Dark: "#FF7B72"})
	box     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(gray).Padding(0, 1)
)

func (m model) View() string {
	m.nextCover = nil
	var b strings.Builder
	if m.terminalWidth() < 30 || m.terminalHeight() < 12 {
		if m.overlay != nil {
			m.overlay.setPlacement(nil)
		}
		return fitLines("Enlarge terminal to at least 30x12.", m.terminalWidth(), m.terminalHeight())
	}
	if m.startup || m.opening != "" {
		if m.overlay != nil {
			m.overlay.setPlacement(nil)
		}
		return m.welcomeView()
	}
	w := m.terminalWidth() - 4
	if m.ws.readd != nil || m.ws.deleteReview != nil {
		if m.overlay != nil {
			m.overlay.setPlacement(nil)
		}
		if m.ws.readd != nil {
			m.trackReaddView(&b)
		} else {
			m.workspaceDeleteReview(&b)
		}
		return fitLines(b.String(), m.terminalWidth(), m.terminalHeight())
	}
	if m.workspaceVisible() {
		m.workspaceView(&b)
		if m.overlay != nil {
			m.overlay.setPlacement(m.nextCover)
		}
		return fitLines(b.String(), m.terminalWidth(), m.terminalHeight())
	}
	m.writeNavigation(&b)
	switch {
	case m.shortcutHelp:
		m.shortcutView(&b, w)
	case m.screen == screenDevices:
		m.devicesView(&b, w)
	case m.screen == screenBrowse:
		m.browseView(&b, w)
	case m.screen == screenSettings:
		m.settingsView(&b, w)
	case m.screen == screenLibrary:
		m.libraryView(&b, w)
	case m.screen == screenCollection:
		m.collectionView(&b, w)
	case m.screen == screenArtist:
		m.artistView(&b, w)
	case m.screen == screenMissing:
		m.missingView(&b, w)
	default:
		m.sourcesView(&b, w)
	}
	if m.overlay != nil {
		m.overlay.setPlacement(m.nextCover)
	}
	return fitLines(b.String(), m.terminalWidth(), m.terminalHeight())
}

func (m model) workspaceVisible() bool {
	return m.eng != nil && m.screen != screenDevices && m.screen != screenBrowse && !m.shortcutHelp &&
		!m.libraryOverlayVisible() && m.ws.deleteReview == nil && !(m.screen == screenCollection && m.collection.preview != nil)
}

func (m model) writeNavigation(b *strings.Builder) {
	if m.eng == nil || m.terminalWidth() < 52 || m.terminalHeight() < 18 ||
		m.shortcutHelp || m.screen == screenDevices || m.screen == screenBrowse || m.libraryOverlayVisible() {
		return
	}
	tabs := []string{"Library", "Sources", "Settings"}
	for i, page := range []screen{screenLibrary, screenSources, screenSettings} {
		if m.screen == page {
			tabs[i] = hi.Render("[" + tabs[i] + "]")
		} else {
			tabs[i] = dim.Render(tabs[i])
		}
	}
	b.WriteString(logo.Render("ytmanager") + "  " + strings.Join(tabs, "   ") + "\n")
}

func (m model) libraryOverlayVisible() bool {
	l := m.library
	return m.screen == screenLibrary && (l.candidates != nil || l.confirm != nil || l.plan != nil || l.report != nil || l.details != nil ||
		l.help || l.removeSource != nil || l.tools || l.purge || l.restore)
}

func header(b *strings.Builder, subtitle string) {
	b.WriteString(logo.Render("ytmanager") + " " + subtitle + "\n\n")
}

// styleHelp highlights the key of each "key label" pair; status text stays plain.
func styleHelp(help string) string {
	parts := strings.Split(help, " · ")
	for i, p := range parts {
		k, label, found := strings.Cut(p, " ")
		if found && (len([]rune(k)) == 1 || len([]rune(k)) <= 7 && strings.ToLower(k) == k) {
			parts[i] = hi.Render(k) + " " + dim.Render(label)
		} else {
			parts[i] = dim.Render(p)
		}
	}
	return strings.Join(parts, "   ")
}

func panel(b *strings.Builder, w int, rows []string) {
	b.WriteString(box.Width(w).Render(strings.Join(rows, "\n")) + "\n")
}

// footer writes the recent log, the text input when it is open, and the key help.
func (m model) footer(b *strings.Builder, help string) {
	bodyHeight := max(0, m.terminalHeight()-m.footerHeight(help))
	body := fitLines(b.String(), m.terminalWidth(), max(1, bodyHeight))
	b.Reset()
	if bodyHeight > 0 {
		b.WriteString(body)
		b.WriteString(strings.Repeat("\n", max(0, bodyHeight-strings.Count(body, "\n"))))
	}
	if len(m.logs) > 0 {
		b.WriteString(m.logsView() + "\n")
	}
	if m.prompt != promptNone {
		b.WriteString("\n" + ansi.Truncate(m.input.View(), m.terminalWidth(), "…") + "\n")
	}
	b.WriteString("\n" + strings.Join(m.helpLines(help), "\n"))
}

func pick(on bool, s string) string {
	if on {
		return hi.Render("› ") + s
	}
	return "  " + s
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// cutLeft shortens s to n runes, keeping the end: for paths the last folder matters most.
func cutLeft(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

func trim(s string) string     { return strings.TrimSpace(s) }
func fields(s string) []string { return strings.Fields(s) }

func anyMarked(marked map[string]bool) bool {
	for _, on := range marked {
		if on {
			return true
		}
	}
	return false
}
