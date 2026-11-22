package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/engine"
)

func (m *model) libraryCatalogView(b *strings.Builder, w int) {
	rows := m.libraryRows()
	items := m.libraryItemsFrom(rows)
	albumList := m.library.albums && m.library.album == ""
	label := "Library / Tracks"
	switch {
	case m.library.problems:
		label = "Library / Problems"
	case m.library.orphans:
		label = "Library / Outside playlists"
	case m.library.duplicates:
		label = "Library / Possible duplicates"
	case m.library.album != "" && len(rows) > 0:
		label = libraryAlbumName(rows[0])
	case albumList:
		label = "Library / Albums"
	}
	b.WriteString(hi.Render(ansi.Truncate(oneLine(label), w, "…")) + "\n")
	b.WriteString(m.libraryActivity(w))

	marked := 0
	for _, row := range rows {
		if m.library.marked[row.ID] {
			marked++
		}
	}
	inventory := countLabel(len(rows), "track")
	if albumList {
		inventory = countLabel(len(items), "album") + " · " + inventory
	}
	if marked > 0 {
		inventory += fmt.Sprintf(" · %d selected", marked)
	}
	line := m.library.scanNotice + " · s retry"
	switch {
	case m.library.scanNotice != "":
	case m.library.query != "":
		line = "Find: " + m.library.query + " · " + inventory
	case m.library.scanning && m.terminalHeight() < 16:
		line = destinationLabel(m.eng.Root(), max(1, w))
	default:
		line = destinationLabel(m.eng.Root(), max(1, w-ansi.StringWidth(inventory)-3)) + " · " + inventory
	}
	if m.library.search {
		b.WriteString(ansi.Truncate(m.input.View(), w, "…") + "\n")
	} else {
		b.WriteString(dim.Render(ansi.Truncate(line, w, "…")) + "\n")
	}

	lines := m.libraryCatalogRows(items, albumList)
	if len(lines) == 0 {
		message := "No tracks match this view."
		switch {
		case m.library.scanning || (!m.library.complete && m.library.scanNotice == ""):
			message = "Looking for music…"
		case !m.library.complete:
			message = "No scan results."
		case len(m.library.all) == 0 && m.library.query == "" && m.library.sourceTracks == nil:
			message = "No music in this folder."
		}
		lines = []string{dim.Render(message)}
	}
	if albumList && len(items) > 0 && w >= 68 {
		item := items[min(m.cursor, len(items)-1)]
		row := item.tracks[0]
		meta := []string{hi.Render("Selected album"), libraryAlbumName(row)}
		if artist := libraryAlbumArtist(row); artist != "" {
			meta = append(meta, dim.Render(artist))
		}
		meta = append(meta, dim.Render(countLabel(len(item.tracks), "track")), dim.Render(albumFolderLabel(item)))
		m.renderWithCoverPanel(b, w, lines, m.cursor*2, 2, m.libraryTrackCoverKey(row), meta)
	} else {
		m.listPanel(b, w, lines, m.cursor*2, 2, 0)
	}
	m.footer(b, m.help())
}

func albumFolderLabel(item libraryItem) string {
	folders := map[string]bool{}
	for _, track := range item.tracks {
		folders[filepath.Dir(track.Path)] = true
	}
	if len(folders) > 1 {
		return countLabel(len(folders), "folder")
	}
	folder := filepath.Dir(item.tracks[0].Path)
	if folder == "." {
		folder = "selected destination"
	}
	return "Folder: " + folder
}

func (m model) libraryCatalogRows(items []libraryItem, albumList bool) []string {
	var lines []string
	// Checkboxes appear only once something is marked; until then they are noise.
	selecting, indent := anyMarked(m.library.marked), "  "
	if selecting {
		indent = "      "
	}
	for i, item := range items {
		mark, marked := "", true
		for _, row := range item.tracks {
			marked = marked && m.library.marked[row.ID]
		}
		if selecting {
			mark = "[ ] "
			if marked {
				mark = "[x] "
			}
		}
		row := item.tracks[0]
		title, detail := libraryTrackText(row)
		if albumList {
			title = libraryAlbumName(row)
			detail = countLabel(len(item.tracks), "track")
			if artist := libraryAlbumArtist(row); artist != "" {
				detail = artist + " · " + detail
			}
		}
		lines = append(lines, pick(i == m.cursor, mark+oneLine(title)), dim.Render(indent+oneLine(detail)))
	}
	return lines
}

func libraryAlbumName(row engine.LibraryTrack) string {
	if row.Album != "" {
		return row.Album
	}
	if dir := filepath.Base(filepath.Dir(row.Path)); dir != "." {
		return dir
	}
	return "Loose tracks"
}

func libraryAlbumArtist(row engine.LibraryTrack) string {
	if row.AlbumArtist != "" {
		return row.AlbumArtist
	}
	return row.Artist
}

func libraryTrackText(row engine.LibraryTrack) (string, string) {
	title := row.Title
	if row.Duration > 0 {
		title += fmt.Sprintf(" · %d:%02d", int(row.Duration)/60, int(row.Duration)%60)
	}
	status := row.Status
	if status == "" {
		status = "present"
	}
	parts := []string{status}
	if row.Unmanaged {
		parts = append(parts, "external")
	}
	if row.Orphan {
		parts = append(parts, "unlisted")
	}
	if row.Artist != "" {
		parts = append(parts, row.Artist)
	}
	if row.Album != "" {
		parts = append(parts, row.Album)
	}
	if row.Codec != "" {
		quality := row.Codec
		if row.Bitrate > 0 {
			quality += fmt.Sprintf(" ~%dk", row.Bitrate/1000)
		}
		if row.SampleRate > 0 {
			quality += fmt.Sprintf(" %dHz", row.SampleRate)
		}
		parts = append(parts, quality)
	}
	if row.References > 1 {
		parts = append(parts, fmt.Sprintf("shared in %d sources", row.References))
	}
	if note := strings.TrimLeft(row.Note, " ·"); note != "" {
		parts = append(parts, note)
	}
	return title, strings.Join(parts, " · ")
}
