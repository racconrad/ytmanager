package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
)

func (m *model) scanLibrary() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.library.inventoryKnown = m.library.inventoryKnown || m.library.complete
	m.library.cancel, m.library.scanning = cancel, true
	m.library.scanNotice = ""
	m.library.since = time.Now()
	eng := m.eng
	return func() tea.Msg {
		rows, err := eng.RefreshLibrary(ctx)
		return libraryScannedMsg{eng, rows, err}
	}
}

func (m *model) finishLibraryScan(msg libraryScannedMsg) {
	if m.library.cancel != nil {
		m.library.cancel()
	}
	m.library.cancel, m.library.scanning = nil, false
	if msg.err != nil {
		m.library.complete = false
		if errors.Is(msg.err, context.Canceled) {
			m.library.scanNotice = "Scan cancelled"
		} else {
			m.library.scanNotice = "Scan failed"
			m.fail(fmt.Errorf("could not scan folder: %w", msg.err))
		}
		return
	}

	before := m.library.all
	items := m.libraryItems()
	selected := ""
	if m.cursor < len(items) {
		selected = m.libraryItemKey(items[m.cursor])
	}
	oldMarks := map[string]engine.LibraryTrack{}
	for _, row := range before {
		if m.library.marked[row.ID] {
			oldMarks[row.ID] = row
		}
	}
	m.library.all, m.library.complete = msg.tracks, true
	if key := m.library.album; key != "" && !slices.ContainsFunc(msg.tracks, func(row engine.LibraryTrack) bool { return row.AlbumKey() == key }) {
		m.library.album = ""
	}
	m.library.marked = map[string]bool{}
	for _, row := range msg.tracks {
		old, marked := oldMarks[row.ID]
		if marked && !row.Missing && row.Status != "not scanned" && old.Path == row.Path &&
			(old.Hash == "" || row.Hash != "") && sameFileEvidence(old, row) {
			m.library.marked[row.ID] = true
		}
	}
	items = m.libraryItems()
	m.cursor = max(0, min(m.cursor, len(items)-1))
	for i, item := range items {
		if selected != "" && m.libraryItemKey(item) == selected {
			m.cursor = i
			break
		}
	}
	m.log(scanSummary(before, msg.tracks, m.library.inventoryKnown))
	m.library.inventoryKnown = true
}

func (m model) libraryItemKey(item libraryItem) string {
	if m.library.albums && m.library.album == "" {
		return item.tracks[0].AlbumKey()
	}
	return item.tracks[0].ID
}

func sameFileEvidence(a, b engine.LibraryTrack) bool {
	if a.Hash != "" && b.Hash != "" {
		return a.Hash == b.Hash
	}
	return a.Size == b.Size && a.Modified == b.Modified
}

func scanInventory(rows []engine.LibraryTrack) (map[string]engine.LibraryTrack, map[string]bool) {
	files := map[string]engine.LibraryTrack{}
	albums := map[string]bool{}
	for _, row := range rows {
		if row.Path == "" || row.Missing || row.Status == "not scanned" {
			continue
		}
		files[row.Path] = row
		if row.Album != "" || filepath.Dir(row.Path) != "." {
			albums[row.AlbumKey()] = true
		}
	}
	return files, albums
}

func scanSummary(before, after []engine.LibraryTrack, compare bool) string {
	now, albums := scanInventory(after)
	if !compare {
		return "Folder scanned: " + countLabel(len(now), "file") + " · " + countLabel(len(albums), "album")
	}
	previous, oldAlbums := scanInventory(before)
	added, gone, changed, newAlbums := 0, 0, 0, 0
	for path, row := range now {
		if old, ok := previous[path]; !ok {
			added++
		} else if !sameFileEvidence(old, row) {
			changed++
		}
	}
	for path := range previous {
		if _, ok := now[path]; !ok {
			gone++
		}
	}
	for album := range albums {
		if !oldAlbums[album] {
			newAlbums++
		}
	}
	var changes []string
	if added > 0 {
		changes = append(changes, "+"+countLabel(added, "file"))
	}
	if newAlbums > 0 {
		changes = append(changes, "+"+countLabel(newAlbums, "album"))
	}
	if gone > 0 {
		changes = append(changes, fmt.Sprintf("%d missing", gone))
	}
	if changed > 0 {
		changes = append(changes, fmt.Sprintf("%d modified", changed))
	}
	if len(changes) == 0 {
		return "No folder changes · " + countLabel(len(now), "file")
	}
	return strings.Join(changes, " · ")
}

func countLabel(n int, item string) string {
	if n != 1 {
		item += "s"
	}
	return fmt.Sprintf("%d %s", n, item)
}
