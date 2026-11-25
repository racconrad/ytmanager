package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/engine"
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
	headerTitle := title
	if m.collection.filesMode && m.collection.preview == nil {
		headerTitle += " · Source files"
	}
	writeCatalogLine(b, w, hi.Render(headerTitle))

	meta := []string{countLabel(len(c.Tracks), "track")}
	year := oneLine(c.Year)
	creator := oneLine(c.Creator)
	if year != "" {
		meta = append(meta, year)
	}
	if creator != "" {
		meta = append(meta, "By "+creator)
	}
	if m.collection.preview == nil && len(c.Tracks) > 0 {
		switch {
		case c.Failed == 0 && c.Missing == 0 && c.Pending == 0 && c.Synced > 0:
			meta = append(meta, fmt.Sprintf("%d/%d synced", c.Synced, len(c.Tracks)))
		default:
			meta = append(meta, fmt.Sprintf("%d/%d synced", c.Synced, len(c.Tracks)))
			if c.Failed > 0 {
				meta = append(meta, fmt.Sprintf("%d failed", c.Failed))
			}
			if c.Missing > 0 {
				meta = append(meta, fmt.Sprintf("%d missing", c.Missing))
			}
			if c.Pending > 0 {
				meta = append(meta, fmt.Sprintf("%d pending", c.Pending))
			}
		}
	}
	rows := make([]string, 0, len(c.Tracks)*2)
	cursor := max(0, min(m.cursor, len(c.Tracks)-1))
	var focusStatus string
	var focusTrack engine.CollectionTrack
	var totalDuration float64
	for i, t := range c.Tracks {
		trackTitle := oneLine(t.Title)
		if trackTitle == "" {
			trackTitle = "Untitled track"
		}
		status := trackStatus(t.Status)
		active := false
		if j := m.run.jobs[t.ID]; j != nil {
			status = oneLine(jobStatus(j))
			active = true
		}
		if t.Duration > 0 && t.Duration < 86400 {
			totalDuration += t.Duration
		}
		if i == cursor {
			focusTrack = t
			focusStatus = status
			if !active && t.Error != "" && (t.Status == "failed" || t.Status == "unavailable") {
				focusStatus = status + ": " + oneLine(t.Error)
			} else if !active && m.collection.preview == nil && t.Path != "" && !m.collection.filesMode {
				focusStatus = status + " · " + oneLine(t.Path)
			}
		}
		if m.collection.filesMode && m.collection.preview == nil {
			syncTag := collectionSyncBadge(t, status, active)
			label := fmt.Sprintf("%02d  %s", i+1, trackTitle)
			if t.Duration > 0 && t.Duration < 86400 {
				label += dim.Render(" · " + catalogDuration(t.Duration))
			}
			label += " · " + syncTag
			sub := dim.Render("    Not downloaded yet")
			switch {
			case t.Error != "" && (t.Status == "failed" || t.Status == "unavailable"):
				sub = bad.Render("    ✗ " + oneLine(t.Error))
			case t.Status == "missing" && t.Path != "":
				sub = bad.Render("    ! Missing on disk: " + oneLine(t.Path))
			case t.Path != "":
				sub = dim.Render("    " + oneLine(t.Path))
			}
			rows = append(rows, pick(i == cursor, label), sub)
			continue
		}
		label := fmt.Sprintf("%02d  %s", i+1, trackTitle)
		if i != cursor {
			label += dim.Render(" · " + status)
		}
		if t.Duration > 0 && t.Duration < 86400 {
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
	statusStyle := hi
	if !m.artist.inspecting && (focusTrack.Status == "failed" || focusTrack.Status == "missing") {
		statusStyle = bad
	}
	switch {
	case focusStatus != "" && coverAction != "":
		separator := " · "
		statusWidth := m.terminalWidth() - ansi.StringWidth(separator+coverAction)
		statusLine = statusStyle.Render(ansi.Truncate(focusStatus, max(1, statusWidth), "…")) + dim.Render(separator) + styleHelp(coverAction)
	case focusStatus != "":
		statusLine = statusStyle.Render(focusStatus)
	case coverAction != "":
		statusLine = styleHelp(coverAction)
	}
	if statusLine != "" {
		writeCatalogLine(b, m.terminalWidth(), statusLine)
	}
	group := 1
	listCur := cursor
	if m.collection.filesMode && m.collection.preview == nil {
		group = 2
		listCur = cursor * 2
	}
	if len(rows) == 0 {
		rows = append(rows, dim.Render("No tracks in collection."))
		group, listCur = 1, 0
	}
	var detail []string
	if c.Cover != "" || (m.collection.preview == nil && (m.collection.filesMode || focusTrack.Path != "" || focusTrack.Error != "")) {
		scope := "Saved source"
		if m.collection.preview != nil {
			scope = "Collection preview"
		} else if c.Failed == 0 && c.Missing == 0 && c.Pending == 0 && c.Synced > 0 {
			scope = "Synced on disk"
		} else {
			scope = fmt.Sprintf("%d/%d synced", c.Synced, len(c.Tracks))
		}
		detail = []string{hi.Render(title), dim.Render(scope)}
		if m.collection.preview == nil && len(c.Tracks) > 0 {
			if focusTrack.Path != "" {
				detail = append(detail, dim.Render("File: "+oneLine(focusTrack.Path)))
			}
			if focusTrack.Error != "" {
				detail = append(detail, bad.Render("Error: "+oneLine(focusTrack.Error)))
			}
		}
	}
	m.renderWithCoverPanel(b, w, rows, listCur, group, c.Cover, detail)
	m.footer(b, m.help())
}

func collectionSyncBadge(t engine.CollectionTrack, status string, active bool) string {
	if active {
		return hi.Render(status)
	}
	switch {
	case t.Synced:
		return ok.Render("✓ Synced")
	case t.Status == "failed":
		return bad.Render("✗ Failed")
	case t.Status == "missing":
		return bad.Render("! Missing")
	case t.Status == "unavailable":
		return dim.Render("- Unavailable")
	case t.Status == "removed":
		return dim.Render("Removed")
	default:
		return dim.Render("· Pending")
	}
}

func catalogDuration(duration float64) string {
	seconds := int(duration)
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func writeCatalogLine(b *strings.Builder, w int, line string) {
	b.WriteString(ansi.Truncate(line, max(1, w), "…") + "\n")
}
