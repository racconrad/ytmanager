package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) renderWithCoverPanel(b *strings.Builder, w int, rows []string, cur, group int, coverKey string, meta []string) {
	headerRows := strings.Count(b.String(), "\n")
	available := m.terminalHeight() - m.footerHeight(m.help()) - headerRows - 2
	if w < 68 || available < max(5, group+1) || (coverKey == "" && len(meta) == 0) {
		m.listPanel(b, w, rows, cur, group, 0)
		return
	}

	cols := min(38, max(20, w/3))
	listW := w - cols - 8
	var left strings.Builder
	m.listPanel(&left, listW, rows, cur, group, headerRows)

	var right []string
	img := m.coverImage(coverKey)
	artHeight := min(cols/2, available-len(meta))
	if img != nil && artHeight >= 4 {
		if m.overlay != nil && m.overlay.enabled {
			for range artHeight {
				right = append(right, strings.Repeat(" ", cols))
			}
			// First right cell after the list, gap and panel border.
			m.nextCover = &coverPlacement{key: coverKey, img: img, row: headerRows + 2, col: listW + 7, cols: cols, rows: artHeight}
		} else {
			right = m.renderedCover(coverKey, img, cols, artHeight)
		}
	}
	for _, line := range meta {
		if len(right) == available {
			break
		}
		right = append(right, ansi.Truncate(oneLineStyled(line), cols, "…"))
	}
	if len(right) < available && m.covers.pending[coverKey] {
		right = append(right, dim.Render("Loading cover…"))
	}
	if len(right) == 0 {
		b.WriteString(strings.TrimSuffix(left.String(), "\n") + "\n")
		return
	}
	side := box.Width(cols + 2).Render(strings.Join(right, "\n"))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, strings.TrimSuffix(left.String(), "\n"), "  ", side) + "\n")
}

func oneLineStyled(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
}
