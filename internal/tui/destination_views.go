package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/device"
	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func (m model) devicesView(b *strings.Builder, w int) {
	wide := m.terminalWidth() >= 76 && m.terminalHeight()-m.footerHeight(m.help()) >= 14 && m.lost == nil
	if m.terminalHeight() >= 18 && m.lost == nil {
		brand := logo.Render("ytmanager")
		if !wide {
			brand += "  " + ok.Render("=^.^= [o-o]")
		}
		b.WriteString(brand + "\n")
	}
	if len(m.recent) > 0 {
		b.WriteString(hi.Render("Choose a destination") + "\n")
	} else {
		b.WriteString(hi.Render("Choose a device") + dim.Render(" · 1 of 2") + "\n")
	}
	if m.lost != nil {
		prefix := "Waiting for "
		b.WriteString(bad.Render(prefix+destinationLabel(m.lost.path, m.terminalWidth()-len(prefix))) + "\n")
		if len(m.lost.urls) > 0 && m.terminalHeight() >= 18 {
			b.WriteString(dim.Render("Downloads resume when it reconnects.") + "\n")
		}
	}
	var rows []string
	home, _ := os.UserHomeDir()
	mounts := m.pickerMounts()
	listWidth := w
	if wide {
		listWidth -= 30
	}
	for i, mount := range mounts {
		kind := "Drive"
		if i < len(m.recent) {
			kind = "Recent folder"
			if listWidth < 32 {
				kind = "Recent"
			}
		} else if device.IsMTP(mount.Path) {
			kind = "MTP"
		} else if home != "" && mount.Path == filepath.Join(home, "Music") {
			kind = "Local"
		}
		capacity := "Space unknown"
		if mount.Free > 0 {
			capacity = engine.Human(mount.Free) + " free"
		}
		label := destinationLabel(mount.Path, listWidth-6)
		if i == m.cursor {
			label = hi.Render(label)
		}
		rows = append(rows, pick(i == m.cursor, label), dim.Render(" "+kind+" · "+capacity))
	}
	rows = append(rows, pick(m.cursor == len(mounts), "Other path…"), dim.Render("  Type or paste a path"))
	if wide {
		var list strings.Builder
		m.listPanel(&list, listWidth, rows, m.cursor*2, 2, strings.Count(b.String(), "\n"))
		action := "Choose a folder"
		switch {
		case m.prompt != promptNone:
			action = "Confirm path"
		case m.cursor >= len(mounts):
			action = "Type or paste a path"
		case m.cursor < len(m.recent):
			action = "Open saved folder"
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, strings.TrimSuffix(list.String(), "\n"), "    ", pickerCassette(action)) + "\n")
	} else {
		m.listPanel(b, listWidth, rows, m.cursor*2, 2, 0)
	}
	m.footer(b, m.help())
}

func (m model) browseView(b *strings.Builder, w int) {
	b.WriteString(hi.Render("Choose a folder") + dim.Render(" · 2 of 2") + "\n")
	b.WriteString(dim.Render("At: "+destinationLabel(m.browse, m.terminalWidth()-4)) + "\n")
	b.WriteString(pick(m.cursor == 0, hi.Render("Use this folder")) + "\n")
	if m.browseHasStats {
		b.WriteString(ok.Render(ansi.Truncate("  Saved storage · "+browseSummaryText(m.browseStats, false), max(1, w), "…")) + "\n")
	} else if m.cursor >= 2 && m.cursor-2 < len(m.dirs) {
		if sub, hasSub := m.browseSubStats[m.dirs[m.cursor-2]]; hasSub {
			b.WriteString(ok.Render(ansi.Truncate("  "+m.dirs[m.cursor-2]+" · "+browseSummaryText(sub, false), max(1, w), "…")) + "\n")
		}
	}
	if m.terminalHeight() >= 18 && len(m.dirs) > 0 {
		b.WriteString(dim.Render(fmt.Sprintf("Subfolders · %d", len(m.dirs))) + "\n")
	}
	rows := []string{pick(m.cursor == 1, "Parent folder")}
	for i, name := range m.dirs {
		label := ansi.Truncate(name, w-7, "…") + "/"
		if w >= 46 {
			if sub, hasSub := m.browseSubStats[name]; hasSub {
				tag := " · " + browseSummaryText(sub, true)
				nameW := max(4, w-7-ansi.StringWidth(tag)-1)
				label = ansi.Truncate(name, nameW, "…") + "/" + dim.Render(tag)
			}
		}
		rows = append(rows, pick(m.cursor == i+2, label))
	}
	if len(m.dirs) == 0 {
		rows = append(rows, dim.Render("  No subfolders"))
		if m.terminalHeight() >= 18 {
			rows = append(rows, dim.Render("  n creates a folder"))
		}
	}
	m.listPanel(b, w, rows, max(0, m.cursor-1), 1, 0)
	m.footer(b, m.help())
}

func browseSummaryText(s manifest.Summary, compact bool) string {
	parts := []string{s.Format}
	if compact {
		parts = append(parts, countLabel(s.Saved, "track"))
		if s.Pending > 0 {
			parts = append(parts, fmt.Sprintf("%d pending", s.Pending))
		}
		return strings.Join(parts, " · ")
	}
	if s.Albums > 0 {
		parts = append(parts, countLabel(s.Albums, "album"))
	}
	if s.Playlists > 0 {
		parts = append(parts, countLabel(s.Playlists, "playlist"))
	}
	if s.Albums == 0 && s.Playlists == 0 && s.Sources > 0 {
		parts = append(parts, countLabel(s.Sources, "source"))
	}
	parts = append(parts, fmt.Sprintf("%d saved", s.Saved))
	if s.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", s.Pending))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", s.Queued))
	}
	return strings.Join(parts, " · ")
}

func destinationLabel(path string, width int) string {
	label := device.Label(path)
	if count := ansi.StringWidth(label); count > width {
		return ansi.TruncateLeft(label, count-width+1, "…")
	}
	return label
}
