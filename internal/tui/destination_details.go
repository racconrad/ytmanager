package tui

import (
	"fmt"
	"strings"

	"ytmanager/internal/engine"
)

func (m model) destinationDetailRows(w int) []string {
	saved := m.ws.destination
	space := "Free space unknown"
	known := m.spaceKnown && m.space.Total > 0 && m.space.Available <= m.space.Total
	if known {
		space = engine.Human(m.space.Available) + " free of " + engine.Human(m.space.Total)
	}
	failed := fmt.Sprintf("%d failed", saved.Failed)
	if saved.Failed > 0 {
		failed = bad.Render(failed)
	}
	rows := []string{
		dim.Render("At: " + destinationLabel(m.eng.Root(), max(1, w-6))),
		strings.ToUpper(saved.Format) + " · " + space,
		countLabel(saved.Albums, "album") + " · " + countLabel(saved.Playlists, "playlist"),
		ok.Render(fmt.Sprintf("%d saved", saved.Saved)) + dim.Render(" · "+countLabel(saved.Tracks, "catalog track")),
		fmt.Sprintf("%d pending · %d queued", saved.Pending, saved.Queued),
		failed + " · " + countLabel(len(m.ws.issues), "issue"),
	}
	if known {
		used := float64(m.space.Total-m.space.Available) / float64(m.space.Total)
		const slots = 10
		n := int(used * slots)
		meter := hi.Render(strings.Repeat("#", n)) + dim.Render(strings.Repeat("-", slots-n))
		rows = append(rows, fmt.Sprintf("[%s] %.0f%% used", meter, used*100))
	}
	return rows
}

func (m model) destinationDetailStatus() string {
	label := "Saved catalog"
	if m.ws.destination.Sources == 0 && m.ws.destination.Tracks == 0 {
		label = "No saved catalog"
	}
	switch {
	case m.library.scanning:
		return label + " · Checking disk"
	case m.library.complete:
		return label + " · Disk checked"
	case m.library.scanNotice != "":
		return label + " · " + m.library.scanNotice
	default:
		return label
	}
}

func (m model) destinationSourceNames() []string {
	if len(m.ws.sources) == 0 {
		return []string{dim.Render("No saved sources")}
	}
	rows := []string{hi.Render("Saved sources")}
	for _, source := range m.ws.sources[:min(3, len(m.ws.sources))] {
		name := source.Title
		if name == "" {
			name = source.URL
		}
		if name != "" {
			rows = append(rows, oneLine(name))
		}
	}
	if more := len(m.ws.sources) - 3; more > 0 {
		rows = append(rows, dim.Render(fmt.Sprintf("+%d more in Sources", more)))
	}
	return rows
}
