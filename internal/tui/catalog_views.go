package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/ytdlp"
)

func (m *model) artistView(b *strings.Builder, w int) {
	if m.artist.artist != nil {
		m.discographyView(b, w)
	} else {
		m.artistPickerView(b, w)
	}
	m.footer(b, m.help())
}

func (m *model) artistPickerView(b *strings.Builder, w int) {
	query := oneLine(m.artist.query)
	search := m.artist.searching || m.artist.loading || m.artist.err != "" || query != "" || len(m.artist.results) > 0
	if search {
		writeCatalogLine(b, w, hi.Render("Artist search"))
	}
	switch {
	case m.artist.searching || m.artist.loading:
		frame := []string{"|", "/", "-", "\\"}[m.cat.frame%4]
		if query != "" {
			writeCatalogLine(b, w, dim.Render(query))
		}
		label := "Searching artists…"
		if m.artist.loading {
			label = "Loading discography…"
		}
		writeCatalogLine(b, w, hi.Render(frame+" "+label))
	case m.artist.err != "":
		writeCatalogLine(b, w, bad.Render(oneLine(m.artist.err)))
		writeCatalogLine(b, w, styleHelp("f search again · esc back"))
	case len(m.artist.results) > 0:
		if query != "" {
			writeCatalogLine(b, w, dim.Render("Results for "+query))
		}
		rows := make([]string, 0, len(m.artist.results)*2)
		var detail []string
		for i, r := range m.artist.results {
			sub := oneLine(r.Subscribers)
			if sub == "" {
				sub = "YouTube Music artist"
			}
			name := oneLine(r.Name)
			rows = append(rows, pick(i == m.cursor, name), dim.Render("    "+sub))
			if i == m.cursor {
				detail = []string{hi.Render(name), dim.Render(sub)}
			}
		}
		m.renderWithCoverPanel(b, w, rows, m.cursor*2, 2, m.artistHighlightedCover(), detail)
	default:
		if search {
			writeCatalogLine(b, w, dim.Render("No matching artists."))
		}
		if !search || len(m.artist.saved) > 0 {
			writeCatalogLine(b, w, hi.Render("Saved artists"))
		}
		if len(m.artist.saved) == 0 {
			if !search {
				writeCatalogLine(b, w, dim.Render("No saved artists yet."))
			}
			writeCatalogLine(b, w, styleHelp("f find an artist"))
			return
		}
		rows := make([]string, 0, len(m.artist.saved))
		for i, name := range m.artist.saved {
			rows = append(rows, pick(i == m.cursor, oneLine(name)))
		}
		m.listPanel(b, w, rows, m.cursor, 1, 0)
	}
}

func (m *model) discographyView(b *strings.Builder, w int) {
	art := m.artist.artist
	name := oneLine(art.Name)
	if name == "" {
		name = "Artist"
	}
	writeCatalogLine(b, w, hi.Render(name))
	subtitle := "Discography"
	if m.artist.inspecting {
		subtitle = "Reading metadata…"
	} else if provider := ytdlp.ProviderLabel(art.Provider); provider != "" {
		subtitle += " · " + provider
	}
	writeCatalogLine(b, w, dim.Render(subtitle))
	writeCatalogLine(b, w, m.discographyTabs(w))

	releases := m.artistReleases()
	rows := make([]string, 0, 2*len(releases))
	var detail []string
	for i, r := range releases {
		mark := dim.Render("○ ")
		if m.artist.marked[r.URL] {
			mark = hi.Render("● ")
		}
		title := oneLine(r.Title)
		badge := releaseStatusBadge(m.artist.installedSrcs, m.artist.installedAlbums, r)
		if badge == "" {
			badge = dim.Render("Not saved")
		}
		kind := "Single"
		if r.Type == "album" {
			kind = "Album"
		}
		if year := oneLine(r.Year); year != "" {
			kind += " " + year
		}
		rows = append(rows, pick(i == m.cursor, mark+title), "  "+badge+dim.Render(" · "+kind))
		if i == m.cursor {
			detail = []string{hi.Render(title), badge + dim.Render(" · "+kind)}
		}
	}
	if len(rows) == 0 {
		m.listPanel(b, w, []string{dim.Render("No releases in this tab.")}, 0, 1, 0)
		return
	}
	m.renderWithCoverPanel(b, w, rows, m.cursor*2, 2, m.artistHighlightedCover(), detail)
}

func (m model) discographyTabs(w int) string {
	art := m.artist.artist
	counts := [...]int{len(art.Albums) + len(art.Singles), len(art.Albums), len(art.Singles)}
	tabs := [...]string{"1 All", "2 Albums", "3 Singles"}
	var withCounts [len(tabs)]string
	for i, tab := range tabs {
		withCounts[i] = fmt.Sprintf("%s (%d)", tab, counts[i])
	}
	if ansi.StringWidth(strings.Join(withCounts[:], " "))+2 <= w {
		tabs = withCounts
	}
	for i := range tabs {
		if i == m.artist.tab {
			tabs[i] = hi.Render("[" + tabs[i] + "]")
		} else {
			tabs[i] = dim.Render(tabs[i])
		}
	}
	return strings.Join(tabs[:], " ")
}

func (m *model) collectionView(b *strings.Builder, w int) {
	c := m.activeCollection()
	title := oneLine(c.Title)
	if title == "" {
		title = "Collection"
	}
	writeCatalogLine(b, w, hi.Render(title))

	meta := []string{countLabel(len(c.Tracks), "track")}
	year := oneLine(c.Year)
	creator := oneLine(c.Creator)
	if year != "" {
		meta = append(meta, year)
	}
	if creator != "" {
		meta = append(meta, "By "+creator)
	}
	rows := make([]string, 0, len(c.Tracks))
	cursor := max(0, min(m.cursor, len(c.Tracks)-1))
	var focusStatus string
	var totalDuration float64
	for i, t := range c.Tracks {
		title := oneLine(t.Title)
		if title == "" {
			title = "Untitled track"
		}
		status := trackStatus(t.Status)
		if j := m.run.jobs[t.ID]; j != nil {
			status = oneLine(jobStatus(j))
		}
		label := fmt.Sprintf("%02d  %s", i+1, title)
		if i == cursor {
			focusStatus = status
		} else {
			label += dim.Render(" · " + status)
		}
		if t.Duration > 0 && t.Duration < 86400 {
			totalDuration += t.Duration
			label += dim.Render(" · " + catalogDuration(t.Duration))
		}
		rows = append(rows, pick(i == cursor, label))
	}
	if totalDuration > 0 {
		meta = append(meta, catalogDuration(totalDuration))
	}
	writeCatalogLine(b, w, dim.Render(strings.Join(meta, " · ")))

	if m.artist.inspecting {
		focusStatus = "Reading metadata…"
	}
	coverAction := ""
	if c.Cover != "" {
		coverAction = "c Preview cover"
		if m.terminalWidth() < 36 {
			coverAction = "c cover"
		}
	}
	var statusLine string
	switch {
	case focusStatus != "" && coverAction != "":
		separator := " · "
		statusWidth := m.terminalWidth() - ansi.StringWidth(separator+coverAction)
		statusLine = hi.Render(ansi.Truncate(focusStatus, statusWidth, "…")) + dim.Render(separator) + styleHelp(coverAction)
	case focusStatus != "":
		statusLine = hi.Render(focusStatus)
	case coverAction != "":
		statusLine = styleHelp(coverAction)
	}
	if statusLine != "" {
		writeCatalogLine(b, m.terminalWidth(), statusLine)
	}
	if len(rows) == 0 {
		rows = append(rows, dim.Render("No tracks in collection."))
	}
	var detail []string
	if c.Cover != "" {
		scope := "Saved source"
		if m.collection.preview != nil {
			scope = "Collection preview"
		}
		detail = []string{hi.Render(title), dim.Render(scope)}
	}
	m.renderWithCoverPanel(b, w, rows, cursor, 1, c.Cover, detail)
	m.footer(b, m.help())
}

func catalogDuration(duration float64) string {
	seconds := int(duration)
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func writeCatalogLine(b *strings.Builder, w int, line string) {
	b.WriteString(ansi.Truncate(line, max(1, w), "…") + "\n")
}
