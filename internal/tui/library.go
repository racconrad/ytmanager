package tui

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

type libraryState struct {
	all                                 []engine.LibraryTrack
	marked                              map[string]bool
	query                               string
	duplicates, search, busy            bool
	confirm                             []string
	scanning                            bool
	cancel                              context.CancelFunc
	complete, albums, orphans, problems bool
	inventoryKnown                      bool
	scanNotice                          string
	album                               string
	includeShared                       bool
	candidates                          []engine.LibraryTrack
	tools, help, purge, restore, reset  bool
	since                               time.Time // start of the current scan or operation
	targetFormat, layout                string
	plan                                *engine.LibraryPlan
	report                              *engine.FileReport
	settingsAction                      string
	helpCursor                          int
	catalogCursor                       int
	sourceTracks                        map[string]bool
	removeSource                        *engine.SourceView
	details                             *engine.LibraryTrack
}
type libraryScannedMsg struct {
	eng    *engine.Engine
	tracks []engine.LibraryTrack
	err    error
}
type startLibraryMsg struct{ eng *engine.Engine }
type libraryDeletedMsg struct {
	eng          *engine.Engine
	tracks       []engine.LibraryTrack
	report       engine.FileReport
	err, scanErr error
}
type libraryPlannedMsg struct {
	eng  *engine.Engine
	plan engine.LibraryPlan
	err  error
}
type libraryItem struct {
	tracks []engine.LibraryTrack
}

func (m *model) openLibrary(action string) tea.Cmd {
	if m.library.scanning {
		m.log("Wait for the folder scan to stop.")
		return nil
	}
	m.library = libraryState{marked: map[string]bool{}, targetFormat: "keep", layout: "keep", settingsAction: action}
	m.screen, m.cursor = screenLibrary, 0
	return m.scanLibrary()
}

func (m *model) openInitialLibrary() tea.Cmd {
	cmd := m.openLibrary("")
	m.library.albums = true
	return cmd
}

func (m *model) resumeLibrary() tea.Cmd {
	if m.library.marked == nil || m.library.sourceTracks != nil {
		return m.openInitialLibrary()
	}
	m.screen, m.cursor = screenLibrary, m.library.catalogCursor
	if m.library.scanning {
		return nil
	}
	return m.scanLibrary()
}
func (m model) libraryRows() []engine.LibraryTrack {
	query := m.library.query
	if m.library.search {
		query = m.input.Value()
	}
	all := engine.FilterLibrary(m.library.all, query, m.library.duplicates)
	var rows []engine.LibraryTrack
	for _, r := range all {
		if m.library.sourceTracks != nil && !m.library.sourceTracks[r.ID] && !m.library.sourceTracks[r.SourceID] {
			continue
		}
		if m.library.orphans && (!(m.library.complete || m.library.inventoryKnown) || !r.Orphan || r.Missing) {
			continue
		}
		if m.library.problems && !r.Missing && !r.Invalid && !r.FormatMismatch && r.Status != "unreadable" {
			continue
		}
		if m.library.album != "" && r.AlbumKey() != m.library.album {
			continue
		}
		rows = append(rows, r)
	}
	if m.library.duplicates {
		engine.SortLibrary(rows, "duplicates")
	} else if m.library.albums {
		engine.SortLibrary(rows, "albums")
	}
	return rows
}
func (m model) libraryItems() []libraryItem {
	return m.libraryItemsFrom(m.libraryRows())
}

func (m model) libraryItemsFrom(rows []engine.LibraryTrack) []libraryItem {
	if !m.library.albums || m.library.album != "" {
		items := make([]libraryItem, 0, len(rows))
		for _, r := range rows {
			items = append(items, libraryItem{tracks: []engine.LibraryTrack{r}})
		}
		return items
	}
	var items []libraryItem
	byKey := map[string]int{}
	for _, r := range rows {
		key := r.AlbumKey()
		idx, ok := byKey[key]
		if !ok {
			idx = len(items)
			byKey[key] = idx
			items = append(items, libraryItem{})
		}
		items[idx].tracks = append(items[idx].tracks, r)
	}
	return items
}
func (m model) librarySelection(allVisible bool) []engine.LibraryTrack {
	rows := m.libraryRows()
	var selected []engine.LibraryTrack
	for _, r := range rows {
		if m.library.marked[r.ID] {
			selected = append(selected, r)
		}
	}
	if len(selected) > 0 {
		return selected
	}
	if allVisible {
		return rows
	}
	items := m.libraryItems()
	if len(items) > 0 {
		return items[min(m.cursor, len(items)-1)].tracks
	}
	return nil
}
func (m *model) reviewDeletion() {
	// A single physical file may back multiple saved IDs. The review includes
	// every affected entry, so future syncs cannot recreate a deleted alias.
	paths, ids := map[string]bool{}, map[string]bool{}
	for _, r := range m.library.candidates {
		paths[r.Path] = true
		ids[r.ID] = true
	}
	for _, r := range m.library.all {
		if paths[r.Path] && !ids[r.ID] {
			m.library.candidates = append(m.library.candidates, r)
			ids[r.ID] = true
		}
	}
	m.library.confirm = nil
	for _, r := range m.library.candidates {
		if !m.library.includeShared && m.library.albums && r.References > 1 {
			continue
		}
		m.library.confirm = append(m.library.confirm, r.ID)
	}
	m.cursor = 0
}
func (m *model) libraryOperation(action string) tea.Cmd {
	eng := m.eng
	ctx, cancel := context.WithCancel(context.Background())
	m.library.cancel, m.library.busy, m.library.since = cancel, true, time.Now()
	selected := append([]engine.LibraryTrack(nil), m.library.candidates...)
	ids := map[string]bool{}
	for _, id := range m.library.confirm {
		ids[id] = true
	}
	var chosen []engine.LibraryTrack
	for _, r := range selected {
		if ids[r.ID] {
			chosen = append(chosen, r)
		}
	}
	plan := m.library.plan
	orphans := m.library.orphans
	if m.library.reset {
		action = "reset"
	}
	m.library.confirm, m.library.candidates, m.library.plan = nil, nil, nil
	m.library.purge, m.library.restore, m.library.reset = false, false, false
	return func() tea.Msg {
		var report engine.FileReport
		var err error
		switch action {
		case "trash":
			if orphans {
				report, err = eng.CleanOrphans(ctx, chosen)
			} else {
				report, err = eng.TrashFiles(ctx, chosen)
			}
		case "reset":
			report, err = eng.ResetTracks(ctx, chosen)
		case "restore":
			report, err = eng.RestoreTrash(ctx)
		case "purge":
			report, err = eng.EmptyTrash(ctx)
		case "apply":
			if plan != nil {
				report, err = eng.ApplyLibraryPlan(ctx, *plan)
			}
		}
		// Always keep a physical inventory after a mutation, including partial failures.
		rows, scanErr := eng.RefreshLibrary(ctx)
		return libraryDeletedMsg{eng, rows, report, err, scanErr}
	}
}

func (m model) libraryKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.library.removeSource != nil {
		if key == "esc" {
			m.library.removeSource = nil
			m.screen, m.cursor = screenSources, 0
		}
		if key == "enter" {
			if err := m.eng.Remove(m.library.removeSource.ID); err != nil {
				m.fail(err)
			} else {
				m.log("Source removed; audio files kept. Library → o reviews files outside playlists.")
			}
			m.library.removeSource = nil
			m.screen, m.cursor = screenSources, 0
		}
		return m, nil
	}
	if m.library.scanning || m.library.busy {
		if m.library.scanning && m.library.settingsAction == "" && (key == "g" || pastedSource(k)) {
			if m.library.cancel != nil {
				m.library.cancel()
			}
			m.leaveLibrary()
			if k.Paste {
				cmd := m.paste(string(k.Runes))
				return m, cmd
			}
		} else if (key == "x" || key == "esc") && m.library.cancel != nil {
			m.library.cancel()
		} else if m.library.scanning && m.library.candidates == nil && m.library.plan == nil && !m.library.tools {
			// Browsing and marking stay usable; finishLibraryScan keeps marks whose files did not change.
			items := m.libraryItems()
			switch key {
			case " ":
				if len(items) > 0 {
					m.toggleLibraryMarks(items[m.cursor].tracks)
				}
			case "a":
				m.toggleLibraryMarks(m.libraryRows())
			default:
				m.libraryNavigate(key, len(items))
			}
		}
		return m, nil
	}
	if m.library.help || m.library.report != nil || m.library.details != nil {
		if key == "esc" || key == "?" || key == "enter" {
			if m.library.help && m.library.settingsAction == "help" {
				m.library.help, m.library.settingsAction = false, ""
				m.screen, m.cursor = screenSettings, libraryGuide
				return m, nil
			}
			back := 0
			if m.library.help {
				back = m.library.helpCursor
			}
			m.library.help = false
			m.library.report = nil
			m.library.details = nil
			m.cursor = back
		} else {
			m.libraryNavigate(key, len(m.libraryReadLines()))
		}
		return m, nil
	}
	if m.library.purge || m.library.restore {
		if key == "esc" {
			m.library.purge, m.library.restore = false, false
		}
		if key == "enter" {
			action := "purge"
			if m.library.restore {
				action = "restore"
			}
			return m, m.libraryOperation(action)
		}
		return m, nil
	}
	if m.library.plan != nil {
		if key == "esc" {
			m.library.plan = nil
			m.library.tools = true
			m.cursor = 0
		} else if key == "i" && len(m.library.plan.Changes) > 0 {
			c := m.library.plan.Changes[m.cursor]
			r := c.Track
			r.Note = "Target: " + c.To + " · " + c.Problem
			m.library.details = &r
			m.cursor = 0
		} else if key == "enter" {
			for _, c := range m.library.plan.Changes {
				if c.Problem != "" {
					m.log("Resolve preview conflicts or narrow the selection before applying.")
					return m, nil
				}
			}
			return m, m.libraryOperation("apply")
		} else {
			m.libraryNavigate(key, len(m.library.plan.Changes))
		}
		return m, nil
	}
	if m.library.candidates != nil {
		switch key {
		case "esc":
			m.library.confirm, m.library.candidates, m.library.reset = nil, nil, false
		case "h":
			m.library.includeShared = !m.library.includeShared
			m.reviewDeletion()
		case "i":
			if len(m.library.confirm) > 0 {
				id := m.library.confirm[min(m.cursor, len(m.library.confirm)-1)]
				for _, r := range m.library.candidates {
					if r.ID == id {
						copy := r
						m.library.details = &copy
						m.cursor = 0
						break
					}
				}
			}
		case "enter":
			if len(m.library.confirm) > 0 {
				return m, m.libraryOperation("trash")
			}
		default:
			m.libraryNavigate(key, len(m.library.confirm))
		}
		return m, nil
	}
	if m.library.tools {
		switch key {
		case "esc", "t":
			m.library.tools = false
			m.cursor = 0
		case "left", "h", "right", "l", " ":
			step := 1
			if key == "left" || key == "h" {
				step = -1
			}
			if m.cursor == 0 {
				m.library.targetFormat = cycle([]string{"keep", "opus", "m4a", "mp3"}, m.library.targetFormat, step)
			}
			if m.cursor == 1 {
				m.library.layout = cycle([]string{"keep", "", "artist", "flat"}, m.library.layout, step)
			}
		case "enter":
			switch m.cursor {
			case 0, 1:
				m.cursor++
			case 2:
				rows := m.librarySelection(true)
				format := m.library.targetFormat
				if format == "keep" {
					format = ""
				}
				layout, eng := m.library.layout, m.eng
				ctx, cancel := context.WithCancel(context.Background())
				m.library.cancel, m.library.busy, m.library.since = cancel, true, time.Now()
				return m, func() tea.Msg {
					plan, err := eng.PlanLibrary(ctx, rows, format, layout)
					return libraryPlannedMsg{eng, plan, err}
				}
			case 3:
				m.library.restore = true
			case 4:
				m.library.purge = true
			case 5:
				m.library.candidates = m.librarySelection(true)
				m.library.tools = false
				if len(m.library.candidates) > 0 {
					m.library.reset, m.library.includeShared = true, true
					m.reviewDeletion()
				}
			case 6:
				m.library.help, m.library.helpCursor = true, m.cursor
				m.cursor = 0
			}
		default:
			m.libraryNavigate(key, 7)
		}
		return m, nil
	}
	if m.library.search {
		switch key {
		case "esc":
			m.library.search = false
			m.input.Blur()
			m.cursor = 0
		case "enter":
			m.library.query = m.input.Value()
			m.library.search = false
			m.input.Blur()
			m.cursor = 0
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			m.cursor = 0
			return m, cmd
		}
		return m, nil
	}
	if pastedSource(k) {
		m.leaveLibrary()
		cmd := m.paste(string(k.Runes))
		return m, cmd
	}
	items := m.libraryItems()
	switch key {
	case "q":
		return m, tea.Quit
	case "g":
		m.leaveLibrary()
		return m, nil
	case "O":
		m.openSettings()
		return m, nil
	case "c":
		m.changeDestination()
		return m, nil
	case "esc", "l":
		if m.library.album != "" {
			m.library.album = ""
			m.cursor = 0
		} else {
			m.leaveLibrary()
			return m, nil
		}
	case "enter":
		if m.library.albums && m.library.album == "" && len(items) > 0 {
			m.library.album = items[m.cursor].tracks[0].AlbumKey()
			m.cursor = 0
		}
	case "/":
		m.library.search = true
		m.input.SetValue(m.library.query)
		m.input.Placeholder = "Search song, artist, album, source or path"
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "s":
		return m, m.scanLibrary()
	case "f":
		m.library.duplicates = !m.library.duplicates
		m.library.albums = false
		m.library.album = ""
		m.cursor = 0
		m.library.marked = map[string]bool{}
	case "b":
		m.library.albums = !m.library.albums
		m.library.album = ""
		m.cursor = 0
	case "F":
		if artist := m.librarySelectedArtist(items); artist != "" {
			return m, m.openArtistQuery(artist)
		}
		return m, m.openSavedArtists()
	case "o":
		if !m.library.complete {
			m.log("Finish a complete scan before reviewing files outside playlists.")
			return m, m.scanLibrary()
		}
		m.library.orphans = !m.library.orphans
		m.cursor = 0
		m.library.marked = map[string]bool{}
	case "!":
		m.library.problems = !m.library.problems
		m.cursor = 0
	case " ":
		if len(items) > 0 {
			m.toggleLibraryMarks(items[m.cursor].tracks)
		}
	case "a":
		m.toggleLibraryMarks(m.libraryRows())
	case "d":
		if !m.library.complete {
			return m, m.scanLibrary()
		}
		m.library.candidates = m.librarySelection(false)
		if len(m.library.candidates) > 0 {
			m.library.includeShared = !m.library.albums
			m.reviewDeletion()
		}
	case "t":
		if !m.library.complete {
			return m, m.scanLibrary()
		}
		m.library.tools = true
		m.cursor = 0
	case "i":
		if len(items) > 0 {
			r := items[m.cursor].tracks[0]
			m.library.details = &r
			m.cursor = 0
		}
	case "u":
		m.library.restore = true
	case "X":
		m.library.purge = true
	case "?":
		m.library.help, m.library.helpCursor = true, m.cursor
		m.cursor = 0
	default:
		m.libraryNavigate(key, len(items))
	}
	return m, m.requestCover(m.libraryCoverKey())
}

// toggleLibraryMarks marks every row, or clears them when all are already marked.
func (m *model) toggleLibraryMarks(rows []engine.LibraryTrack) {
	all := len(rows) > 0
	for _, r := range rows {
		all = all && m.library.marked[r.ID]
	}
	for _, r := range rows {
		m.library.marked[r.ID] = !all
	}
}

func (m *model) leaveLibrary() {
	m.library.catalogCursor = m.cursor
	m.screen, m.cursor = screenSources, 0
}

func pastedSource(k tea.KeyMsg) bool {
	if !k.Paste {
		return false
	}
	for _, word := range fields(string(k.Runes)) {
		if ytdlp.Provider(word) != "" || ytdlp.IsArtistURL(word) {
			return true
		}
	}
	return false
}

func (m model) librarySelectedArtist(items []libraryItem) string {
	if len(items) == 0 {
		return ""
	}
	r := items[min(m.cursor, len(items)-1)].tracks[0]
	for _, candidate := range []string{r.AlbumArtist, r.Artist} {
		if c := strings.TrimSpace(strings.TrimSuffix(candidate, " - Topic")); c != "" && !strings.EqualFold(c, "Unknown Artist") {
			return c
		}
	}
	if r.Creators != "" {
		first := strings.TrimSpace(strings.Split(r.Creators, "·")[0])
		if first != "" {
			return first
		}
	}
	if r.Path != "" {
		parts := strings.Split(filepath.ToSlash(filepath.Clean(r.Path)), "/")
		if len(parts) >= 2 && !strings.EqualFold(parts[0], "Unknown") {
			return parts[0]
		}
	}
	return ""
}

func (m model) libraryCoverKey() string {
	if m.terminalWidth() < 72 || m.terminalHeight() < 16 {
		return ""
	}
	var r engine.LibraryTrack
	switch {
	case m.library.details != nil:
		r = *m.library.details
	case m.library.albums:
		items := m.libraryItems()
		if len(items) == 0 {
			return ""
		}
		r = items[min(m.cursor, len(items)-1)].tracks[0]
	default:
		return ""
	}
	return m.libraryTrackCoverKey(r)
}

func (m model) libraryTrackCoverKey(r engine.LibraryTrack) string {
	if r.Path != "" && m.eng != nil {
		if !r.Missing && !r.Invalid {
			return filepath.Join(m.eng.Root(), r.Path)
		}
	}
	if m.eng != nil && r.SourceID != "" {
		for _, s := range m.eng.Snapshot() {
			if s.ID == r.SourceID && s.Cover != "" {
				return s.Cover
			}
		}
	}
	return ""
}
func (m *model) libraryNavigate(key string, total int) {
	switch key {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(max(0, total-1), m.cursor+1)
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = max(0, total-1)
	case "pgup":
		m.cursor = max(0, m.cursor-max(1, m.terminalHeight()/3))
	case "pgdown":
		m.cursor = min(max(0, total-1), m.cursor+max(1, m.terminalHeight()/3))
	}
}
