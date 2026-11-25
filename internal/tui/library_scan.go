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

type libraryScan struct {
	eng    *engine.Engine
	ctx    context.Context
	events chan tea.Msg
	marks  scanWorkspaceBaseline
}

type libraryDiscoveredMsg struct {
	scan *libraryScan
	rows []engine.LibraryTrack
}

func (m *model) scanLibrary() tea.Cmd {
	if m.library.scan != nil && m.library.cancel != nil {
		m.library.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	scan := &libraryScan{eng: m.eng, ctx: ctx, events: make(chan tea.Msg)}
	scan.marks = scanWorkspaceBaseline{albums: m.ws.albums, folders: m.ws.folders, issues: m.ws.issues}
	m.library.inventoryKnown = m.library.inventoryKnown || m.library.complete
	m.library.scan, m.library.preview, m.library.complete = scan, nil, false
	m.library.cancel, m.library.scanning = cancel, true
	m.library.scanNotice = ""
	m.library.since = time.Now()
	return func() tea.Msg {
		go scan.run()
		return scan.next()
	}
}

func (s *libraryScan) run() {
	defer close(s.events)
	rows, err := s.eng.RefreshLibraryProgress(s.ctx, func(rows []engine.LibraryTrack) {
		s.send(libraryDiscoveredMsg{s, rows})
	})
	s.send(libraryScannedMsg{s, rows, err})
}

func (s *libraryScan) send(msg tea.Msg) {
	select {
	case s.events <- msg:
	case <-s.ctx.Done():
	}
}

func (s *libraryScan) next() tea.Msg {
	if msg, ok := <-s.events; ok {
		return msg
	}
	return libraryScannedMsg{scan: s, err: s.ctx.Err()}
}

func (m model) activeLibraryScan(s *libraryScan) bool {
	return s != nil && s == m.library.scan && s.eng == m.eng && m.library.scanning
}

func (m model) libraryDisplayRows() []engine.LibraryTrack {
	if m.library.preview != nil {
		return m.library.preview
	}
	if len(m.library.all) == 0 && !m.library.inventoryKnown && !m.library.complete && m.eng != nil {
		return m.eng.ManifestLibrary()
	}
	return m.library.all
}

func (m *model) applyLibraryDiscovery(batch []engine.LibraryTrack) {
	rows := slices.Clone(m.libraryDisplayRows())
	byID := make(map[string]int, len(rows))
	for i, row := range rows {
		byID[row.ID] = i
	}
	for _, row := range batch {
		if i, found := byID[row.ID]; found {
			old := rows[i]
			if old.Path != row.Path || old.Missing || old.Status == "not scanned" || !sameFileEvidence(old, row) {
				rows[i] = row
			}
		} else {
			byID[row.ID] = len(rows)
			rows = append(rows, row)
		}
	}
	m.library.preview = rows
	m.library.cachedValid = false
	m.refreshLibraryWorkspace()
}

func (m *model) finishLibraryScan(msg libraryScannedMsg) {
	if m.library.cancel != nil {
		m.library.cancel()
	}
	m.library.cancel, m.library.scanning = nil, false
	m.library.scan, m.library.preview = nil, nil
	m.library.cachedValid = false
	if msg.err != nil {
		m.library.complete = false
		if errors.Is(msg.err, context.Canceled) {
			m.library.scanNotice = "Scan cancelled"
		} else {
			m.library.scanNotice = "Scan failed"
			m.fail(fmt.Errorf("could not scan folder: %w", msg.err))
		}
		m.refreshLibraryWorkspace()
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
	m.library.allGen++
	m.library.cachedValid = false
	if key := m.library.album; key != "" && !slices.ContainsFunc(msg.tracks, func(row engine.LibraryTrack) bool { return row.AlbumKey() == key }) {
		m.library.album = ""
	}
	m.library.marked = map[string]bool{}
	for _, row := range msg.tracks {
		old, marked := oldMarks[row.ID]
		if marked && verifiedTrackUnchanged(old, row) {
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
	summary := scanSummary(before, msg.tracks, m.library.inventoryKnown)
	// The app opens on Library, so a queue saved by a previous session must be announced here.
	if queued := m.eng.QueuedSources(); queued > 0 && !m.run.busy {
		summary += " · " + countLabel(queued, "source") + " waiting to download: g, then Enter resumes"
	}
	m.library.inventoryKnown = true
	m.refreshLibraryWorkspace()
	if m.revalidateWorkspaceMarks(msg.scan.marks, before) {
		summary += " · Selection changed; review marks again"
	}
	m.log(summary)
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
