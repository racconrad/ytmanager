package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const cassetteWidth = 22

func cassetteScene(face, reel string) []string {
	return []string{
		"         " + hi.Render(catHead),
		"         " + ok.Render(catFaces[face]),
		hi.Render("   .-----") + ok.Render(catFeet["sit"]) + hi.Render("-----."),
		hi.Render("   |    ") + ok.Render("("+reel+")---("+reel+")") + hi.Render("    |"),
		hi.Render(`   |      /___\      |`),
		hi.Render("   '-----------------'"),
	}
}

func centeredLine(line string, width int) string {
	return strings.Repeat(" ", max(0, (width-ansi.StringWidth(line))/2)) + line
}

func (m model) welcomeView() string {
	w, h := m.terminalWidth(), m.terminalHeight()
	status := "Finding destinations…"
	if m.opening != "" {
		status = "Opening destination…"
	}
	scene := cassetteScene(
		[...]string{"open", "happy", "open", "blink"}[m.cat.frame/3%4],
		[...]string{"o", "O", "*", "O"}[m.cat.frame/2%4],
	)
	pad := strings.Repeat(" ", max(0, (w-cassetteWidth)/2))
	lines := []string{centeredLine(logo.Render("ytmanager"), w), ""}
	for _, line := range scene {
		lines = append(lines, pad+line)
	}
	lines = append(lines, "", centeredLine(warn.Render(m.spinnerFrame())+" "+status, w))
	if m.opening != "" {
		path := destinationLabel(m.opening, min(w-4, 48))
		lines = append(lines, centeredLine(dim.Render(path), w))
	}
	blank := strings.Repeat("\n", max(0, (h-len(lines))/2))
	return fitLines(blank+strings.Join(lines, "\n"), w, h)
}

func pickerCassette(action string) string {
	scene := cassetteScene("open", "o")
	return strings.Join(scene, "\n") + "\n\n" + hi.Render("Enter") + " " + action
}
