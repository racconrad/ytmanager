package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
)

const readdVerb = "re-added"

type readdState struct {
	source, title string
	rows          []engine.CollectionTrack
	cursor        int
	duplicate     map[string]bool
	confirm       bool
}

func (m *model) openTrackReadd() {
	if m.run.busy || m.library.busy {
		m.log("Wait for the current operation to finish.")
		return
	}
	source := m.ws.sourceID
	if source == "" {
		return
	}
	rows := m.eng.RestorableTracks(source)
	seen, duplicate := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		label := strings.ToLower(oneLine(row.Title))
		duplicate[label] = seen[label]
		seen[label] = true
	}
	m.ws.readd = &readdState{source: source, title: m.ws.sourceDetail.Title, rows: rows, duplicate: duplicate}
}

func (m *model) trackReaddKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.ws.readd
	if k.Paste {
		return *m, nil
	}
	key := k.String()
	switch {
	case key == "esc":
		if r.confirm {
			r.confirm = false
		} else {
			m.ws.readd = nil
		}
	case key == "enter" && len(r.rows) > 0:
		if !r.confirm {
			r.confirm = true
			return *m, nil
		}
		if m.run.busy || m.library.busy {
			m.log("Wait for the current operation to finish.")
			m.ws.readd = nil
			return *m, nil
		}
		eng, source, id := m.eng, r.source, r.rows[r.cursor].ID
		m.ws.readd = nil
		cmd := m.start(nil, readdVerb, func(ctx context.Context, emit func(engine.Event)) error {
			return eng.ReaddTrack(ctx, source, id, emit)
		})
		return *m, cmd
	case !r.confirm && len(r.rows) > 0:
		page := m.trackReaddPageSize()
		switch key {
		case "up", "k":
			r.cursor = max(0, r.cursor-1)
		case "down", "j":
			r.cursor = min(len(r.rows)-1, r.cursor+1)
		case "pgup":
			r.cursor = max(0, r.cursor-page)
		case "pgdown":
			r.cursor = min(len(r.rows)-1, r.cursor+page)
		case "home":
			r.cursor = 0
		case "end":
			r.cursor = len(r.rows) - 1
		}
	}
	return *m, nil
}

func (m model) trackReaddPageSize() int {
	return max(1, m.terminalHeight()-8)
}

func (m model) trackReaddView(b *strings.Builder) {
	r := m.ws.readd
	header(b, "Re-add track")
	fmt.Fprintf(b, "List: %s\n\n", oneLine(r.title))
	help := "esc back"
	switch {
	case len(r.rows) == 0:
		b.WriteString("No removed tracks.\n")
	case r.confirm:
		row := r.rows[r.cursor]
		fmt.Fprintf(b, "Track: %s\n", oneLine(row.Title))
		if r.duplicate[strings.ToLower(oneLine(row.Title))] {
			fmt.Fprintf(b, "ID: %s\n", oneLine(row.ID))
		}
		b.WriteString("Only this list will change.\n")
		help = "enter re-add · esc back"
	default:
		page := min(len(r.rows), m.trackReaddPageSize())
		start := min(max(0, r.cursor-page/2), len(r.rows)-page)
		for i, row := range r.rows[start : start+page] {
			label := oneLine(row.Title)
			if r.duplicate[strings.ToLower(label)] {
				label = oneLine(row.ID) + " · " + label
			}
			b.WriteString(pick(start+i == r.cursor, label) + "\n")
		}
		fmt.Fprintf(b, "%d of %d\n", r.cursor+1, len(r.rows))
		help = "↑↓ move · enter · esc back"
	}
	b.WriteString("\n" + strings.Join(m.helpLines(help), "\n"))
}
