package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Unavailable tracks: give each one an alternative (song, album or playlist link) or retry it.

func (m model) missingKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.eng.Missing()
	var cmd tea.Cmd
	switch k.String() {
	case "esc", "n":
		m.screen, m.cursor = screenSources, 0
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = max(0, min(len(list)-1, m.cursor+1))
	case "enter", "a":
		if len(list) > 0 {
			m.altFor = list[m.cursor].ID
			cmd = m.ask(promptAlt, "YouTube Music link for “"+cut(list[m.cursor].Title, 40)+"”", "")
		}
	case "r":
		if len(list) > 0 {
			x := list[m.cursor]
			if err := m.eng.Retry(x.ID); err != nil {
				m.fail(err)
				break
			}
			cmd = m.add([]string{x.URL})
			m.cursor = max(0, min(m.cursor, len(list)-2))
		}
	}
	return m, cmd
}

// setAlt resolves the alternative off the UI goroutine (yt-dlp takes a few seconds).
func (m *model) setAlt(id, url string) tea.Cmd {
	eng, src := m.eng, ""
	for _, x := range eng.Missing() {
		if x.ID == id {
			src = x.URL
		}
	}
	m.log(dim.Render("Looking up the alternative…"))
	return func() tea.Msg {
		title, err := eng.SetAlt(context.Background(), id, url)
		return altMsg{title, src, err}
	}
}

// altResolved re-syncs the track's source so the alternative downloads right away.
func (m *model) altResolved(msg altMsg) tea.Cmd {
	if msg.err != nil {
		m.log(bad.Render("✗ alternative: " + msg.err.Error()))
		return nil
	}
	m.log(ok.Render("✓ Alternative: " + msg.title + ". Downloading…"))
	return m.add([]string{msg.sourceURL})
}

func (m model) missingView(b *strings.Builder, w int) {
	list := m.eng.Missing()
	title := hi.Render("Unavailable tracks")
	if len(list) == 0 {
		b.WriteString(title + "\n" + ok.Render("No unavailable tracks to review.") + "\n")
		m.footer(b, m.help())
		return
	}
	cursor := min(m.cursor, len(list)-1)
	x := list[cursor]
	available := m.terminalHeight() - m.footerHeight(m.help()) - strings.Count(b.String(), "\n")
	w = min(w, 78)
	summary := fmt.Sprintf(" · %d/%d", cursor+1, len(list))
	if len(list) > 1 && available < 9 {
		summary += " ↑↓"
	}
	b.WriteString(title + dim.Render(summary) + "\n")
	var detail []string
	if x.Source != "" {
		detail = append(detail, dim.Render("From: "+x.Source))
	}
	reason := strings.Join(strings.Fields(x.Error), " ")
	if x.Alt != "" {
		detail = append(detail, bad.Render("Alternative unavailable"))
	} else if x.Failed {
		detail = append(detail, bad.Render("Download failed"))
	}
	if reason == "" && x.Alt == "" {
		reason = "Original unavailable at source."
	}
	if reason != "" {
		lines := strings.Split(ansi.Wrap(reason, w, ""), "\n")
		limit := min(3, available-2-len(detail))
		if len(lines) > limit {
			lines = append(lines[:max(0, limit-1)], ansi.Truncate(strings.Join(lines[max(0, limit-1):], " "), w, "…"))
		}
		for _, line := range lines {
			detail = append(detail, dim.Render(line))
		}
	}
	if available >= 10 && m.prompt == promptNone {
		detail = append(detail, dim.Render("Use a YouTube Music link for an alternative."))
	}
	if available < 9 {
		b.WriteString(pick(true, x.Title) + "\n")
	} else {
		rows := make([]string, 0, len(list))
		for i, item := range list {
			rows = append(rows, pick(i == cursor, item.Title))
		}
		m.listPanel(b, w, rows, cursor, 1, len(detail))
	}
	b.WriteString(strings.Join(detail, "\n") + "\n")
	m.footer(b, m.help())
}
