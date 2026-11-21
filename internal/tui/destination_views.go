package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/device"
	"ytmanager/internal/engine"
)

func (m model) devicesView(b *strings.Builder, w int) {
	b.WriteString(hi.Render("Choose a device") + dim.Render(" · 1 of 2") + "\n")
	if m.lost != nil {
		prefix := "Waiting for "
		b.WriteString(bad.Render(prefix+destinationLabel(m.lost.path, m.terminalWidth()-len(prefix))) + "\n")
		if len(m.lost.urls) > 0 && m.terminalHeight() >= 18 {
			b.WriteString(dim.Render("Downloads resume when it reconnects.") + "\n")
		}
	}
	var rows []string
	home, _ := os.UserHomeDir()
	for i, mount := range m.mounts {
		kind := "Drive"
		if device.IsMTP(mount.Path) {
			kind = "MTP"
		} else if home != "" && mount.Path == filepath.Join(home, "Music") {
			kind = "Local"
		}
		capacity := "Space unknown"
		if mount.Free > 0 {
			capacity = engine.Human(mount.Free) + " free"
		}
		rows = append(rows, pick(i == m.cursor, destinationLabel(mount.Path, w-6)), dim.Render(" "+kind+" · "+capacity))
	}
	rows = append(rows, pick(m.cursor == len(m.mounts), "Other path…"), dim.Render("  Type or paste a path"))
	m.listPanel(b, w, rows, m.cursor*2, 2, 0)
	m.footer(b, m.help())
}

func (m model) browseView(b *strings.Builder, w int) {
	b.WriteString(hi.Render("Choose a folder") + dim.Render(" · 2 of 2") + "\n")
	b.WriteString(dim.Render("At: "+destinationLabel(m.browse, m.terminalWidth()-4)) + "\n")
	b.WriteString(pick(m.cursor == 0, hi.Render("Use this folder")) + "\n")
	if m.terminalHeight() >= 18 && len(m.dirs) > 0 {
		b.WriteString(dim.Render(fmt.Sprintf("Subfolders · %d", len(m.dirs))) + "\n")
	}
	rows := []string{pick(m.cursor == 1, "Parent folder")}
	for i, name := range m.dirs {
		rows = append(rows, pick(m.cursor == i+2, ansi.Truncate(name, w-7, "…")+"/"))
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

func destinationLabel(path string, width int) string {
	label := device.Label(path)
	if count := ansi.StringWidth(label); count > width {
		return ansi.TruncateLeft(label, count-width+1, "…")
	}
	return label
}
