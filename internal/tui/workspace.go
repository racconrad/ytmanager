package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

type navSection int

const (
	secDetails navSection = iota
	secAlbums
	secFolders
	secSources
	secArtists
	secSettings
	secIssues
)

func (s navSection) tab() navSection {
	if s == secIssues {
		return secDetails
	}
	return s
}

type focusPane int

const (
	paneList focusPane = iota
	paneMenu
	paneSide
)

var sectionNames = []string{"Details", "Albums", "Folders", "Sources", "Artists", "Settings"}

type wsAction struct {
	id    string
	label string
}

type workspaceState struct {
	section            navSection
	focus              focusPane
	menuCursor         int
	listCursor         int
	parentCursor       int
	sideCursor         int
	albumKey           string // non-empty when drilled into an album's tracks
	folderDir          string // non-empty when drilled into a physical folder's files
	sourceID           string // non-empty when drilled into a source's tracks & files
	query              string
	searching          bool
	albums             []engine.UnifiedAlbum
	folders            []engine.PhysicalFolder
	issues             []engine.IssueItem
	sources            []engine.SourceView
	destination        manifest.Summary
	sourceDetail       engine.Collection
	markedAlbums       map[string]bool
	markedFolders      map[string]bool
	markedTracks       map[string]bool
	markedSourceTracks map[string]bool
	markedFiles        map[string]bool
	markedIssues       map[string]bool
	readd              *readdState
	deleteReview       []engine.LibraryTrack
	deleteCursor       int
	removing           bool
	lastGen            int
	lastLibLen         int
	lastScreen         screen
}

func (m *model) initWorkspace() {
	m.ws = workspaceState{
		section:            secAlbums,
		focus:              paneList,
		menuCursor:         int(secAlbums),
		markedAlbums:       map[string]bool{},
		markedFolders:      map[string]bool{},
		markedTracks:       map[string]bool{},
		markedSourceTracks: map[string]bool{},
		markedFiles:        map[string]bool{},
		markedIssues:       map[string]bool{},
		lastScreen:         m.screen,
	}
	m.refreshWorkspace()
}

func (m *model) syncWorkspaceFromState() {
	if m.eng == nil {
		return
	}
	if m.ws.lastGen == 0 || m.ws.lastLibLen != len(m.library.all) {
		m.refreshWorkspace()
	}
	if m.screen == m.ws.lastScreen {
		return
	}
	m.ws.lastScreen = m.screen
	switch m.screen {
	case screenArtist:
		m.ws.section = secArtists
		m.ws.menuCursor = int(secArtists)
		m.ws.listCursor = m.cursor
	case screenSettings:
		m.ws.section = secSettings
		m.ws.menuCursor = int(secSettings)
		m.ws.listCursor = m.cursor
	case screenMissing:
		m.ws.section = secIssues
		m.ws.menuCursor = int(secDetails)
		m.ws.listCursor = m.cursor
	case screenSources:
		if m.ws.section != secSources {
			m.ws.section = secSources
			m.ws.menuCursor = int(secSources)
			m.ws.listCursor = m.cursor
		}
	}
	m.clampWorkspaceCursor()
}

func (m *model) refreshWorkspace() {
	if m.eng == nil {
		return
	}
	if m.ws.markedAlbums == nil {
		m.ws.markedAlbums = map[string]bool{}
		m.ws.markedFolders = map[string]bool{}
		m.ws.markedTracks = map[string]bool{}
		m.ws.markedSourceTracks = map[string]bool{}
		m.ws.markedFiles = map[string]bool{}
		m.ws.markedIssues = map[string]bool{}
	}
	rows := m.libraryDisplayRows()
	m.ws.albums = m.eng.UnifiedAlbums(rows)
	m.ws.folders = engine.BuildPhysicalFolders(rows)
	m.ws.issues = m.eng.WorkspaceIssues(rows)
	m.ws.sources = m.eng.CatalogSources()
	if m.ws.section == secDetails {
		m.ws.destination = m.eng.CatalogSummary()
	}
	if m.ws.albumKey != "" {
		if _, ok := m.activeAlbum(); !ok {
			m.ws.albumKey = ""
		}
	}
	if m.ws.folderDir != "" {
		if _, ok := m.activeFolder(); !ok {
			m.ws.folderDir = ""
		}
	}
	if m.ws.sourceID != "" {
		found := false
		for _, s := range m.ws.sources {
			if s.ID == m.ws.sourceID {
				found = true
				break
			}
		}
		if found {
			m.ws.sourceDetail = m.eng.CatalogCollection(m.ws.sourceID)
		} else {
			m.ws.sourceID = ""
		}
	}
	m.ws.lastGen++
	m.ws.lastLibLen = len(m.library.all)
	m.clampWorkspaceCursor()
}

func (m *model) setSection(sec navSection) tea.Cmd {
	return m.switchSection(sec, paneList)
}

func (m *model) switchSection(sec navSection, focus focusPane) tea.Cmd {
	m.ws.section = sec
	m.ws.focus = focus
	m.ws.menuCursor = int(sec.tab())
	m.ws.albumKey = ""
	m.ws.folderDir = ""
	m.ws.sourceID = ""
	m.ws.listCursor = 0
	m.ws.sideCursor = 0
	m.ws.query = ""
	m.ws.searching = false
	m.cursor = 0
	switch sec {
	case secDetails:
		m.screen = screenLibrary
		m.ws.destination = m.eng.CatalogSummary()
	case secAlbums, secFolders:
		m.screen = screenLibrary
	case secSources:
		m.screen = screenSources
		m.ws.sources = m.eng.CatalogSources()
	case secIssues:
		m.screen = screenMissing
	case secArtists:
		m.screen = screenArtist
		if m.artist.marked == nil {
			m.enterArtist(artistState{})
		}
	case secSettings:
		m.screen = screenSettings
	}
	m.ws.lastScreen = m.screen
	m.clampWorkspaceCursor()
	return m.requestCover(m.workspaceCoverKey())
}

func (m model) filteredAlbums() []engine.UnifiedAlbum {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return m.ws.albums
	}
	terms := strings.Fields(q)
	var out []engine.UnifiedAlbum
	for _, a := range m.ws.albums {
		hay := strings.ToLower(a.Title + " " + a.Artist + " " + strings.Join(a.Folders, " "))
		if matchAllTerms(hay, terms) {
			out = append(out, a)
		}
	}
	return out
}

func (m model) activeAlbum() (engine.UnifiedAlbum, bool) {
	if m.ws.albumKey == "" {
		return engine.UnifiedAlbum{}, false
	}
	for _, a := range m.ws.albums {
		if a.Key == m.ws.albumKey {
			return a, true
		}
	}
	return engine.UnifiedAlbum{}, false
}

func (m model) filteredAlbumTracks(alb engine.UnifiedAlbum) []engine.UnifiedTrack {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return alb.Tracks
	}
	terms := strings.Fields(q)
	var out []engine.UnifiedTrack
	for _, tr := range alb.Tracks {
		hay := strings.ToLower(tr.Title + " " + tr.Path + " " + tr.Status + " " + tr.Error)
		if matchAllTerms(hay, terms) {
			out = append(out, tr)
		}
	}
	return out
}

func (m model) filteredFolderFiles(fld engine.PhysicalFolder) []engine.LibraryTrack {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return fld.Files
	}
	terms := strings.Fields(q)
	var out []engine.LibraryTrack
	for _, f := range fld.Files {
		hay := strings.ToLower(f.Title + " " + f.Artist + " " + f.Path + " " + f.Sources + " " + f.Note)
		if matchAllTerms(hay, terms) {
			out = append(out, f)
		}
	}
	return out
}

func (m model) filteredFolders() []engine.PhysicalFolder {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return m.ws.folders
	}
	terms := strings.Fields(q)
	var out []engine.PhysicalFolder
	for _, f := range m.ws.folders {
		hay := strings.ToLower(f.Dir + " " + f.LinkedSources)
		if matchAllTerms(hay, terms) {
			out = append(out, f)
		}
	}
	return out
}

func (m model) activeFolder() (engine.PhysicalFolder, bool) {
	if m.ws.folderDir == "" {
		return engine.PhysicalFolder{}, false
	}
	for _, f := range m.ws.folders {
		if f.Dir == m.ws.folderDir {
			return f, true
		}
	}
	return engine.PhysicalFolder{}, false
}

func (m model) filteredIssues() []engine.IssueItem {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return m.ws.issues
	}
	terms := strings.Fields(q)
	var out []engine.IssueItem
	for _, iss := range m.ws.issues {
		hay := strings.ToLower(iss.Badge + " " + iss.Title + " " + iss.Subtitle + " " + iss.Path + " " + iss.Error + " " + iss.SourceTitle)
		if matchAllTerms(hay, terms) {
			out = append(out, iss)
		}
	}
	return out
}

func (m model) filteredSources() []engine.SourceView {
	q := strings.ToLower(strings.TrimSpace(m.ws.activeQuery()))
	if q == "" {
		return m.ws.sources
	}
	terms := strings.Fields(q)
	var out []engine.SourceView
	for _, s := range m.ws.sources {
		hay := strings.ToLower(s.Title + " " + s.Creator + " " + s.Type + " " + s.URL)
		if matchAllTerms(hay, terms) {
			out = append(out, s)
		}
	}
	return out
}

func (w workspaceState) activeQuery() string {
	return w.query
}

func matchAllTerms(hay string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

func (m model) workspaceListLen() int {
	switch m.ws.section {
	case secDetails:
		return len(m.destinationDetailRows(m.terminalWidth()))
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			return len(m.filteredAlbumTracks(alb))
		}
		return len(m.filteredAlbums())
	case secFolders:
		if fld, ok := m.activeFolder(); ok {
			return len(m.filteredFolderFiles(fld))
		}
		return len(m.filteredFolders())
	case secSources:
		if m.ws.sourceID != "" {
			return len(m.filteredSourceTracks())
		}
		return len(m.filteredSources())
	case secIssues:
		return len(m.filteredIssues())
	case secArtists:
		if m.artist.artist != nil {
			return len(m.artistReleases())
		}
		if len(m.artist.results) > 0 {
			return len(m.artist.results)
		}
		return len(m.artist.saved)
	case secSettings:
		return len(settings)
	}
	return 0
}

func (m *model) clampWorkspaceCursor() {
	n := m.workspaceListLen()
	if n == 0 {
		m.ws.listCursor = 0
	} else {
		m.ws.listCursor = max(0, min(n-1, m.ws.listCursor))
	}
	if (m.ws.section == secArtists || m.ws.section == secSettings || m.ws.section == secSources) && m.ws.sourceID == "" {
		m.cursor = m.ws.listCursor
	}
	actions := m.workspaceActions()
	if len(actions) == 0 {
		m.ws.sideCursor = 0
	} else {
		m.ws.sideCursor = max(0, min(len(actions)-1, m.ws.sideCursor))
	}
}

func (m model) workspaceCoverKey() string {
	if m.eng == nil || m.terminalWidth() < 78 || m.terminalHeight() < 18 {
		return ""
	}
	switch m.ws.section {
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			if m.ws.listCursor < len(alb.Tracks) {
				tr := alb.Tracks[m.ws.listCursor]
				if tr.LibraryRow != nil {
					if k := m.libraryTrackCoverKey(*tr.LibraryRow); k != "" {
						return k
					}
				}
			}
			return alb.Cover
		}
		albs := m.filteredAlbums()
		if len(albs) > 0 && m.ws.listCursor < len(albs) {
			alb := albs[m.ws.listCursor]
			for _, tr := range alb.Tracks {
				if tr.LibraryRow != nil && tr.Synced {
					if k := m.libraryTrackCoverKey(*tr.LibraryRow); k != "" {
						return k
					}
				}
			}
			return alb.Cover
		}
	case secFolders:
		if fld, ok := m.activeFolder(); ok && len(fld.Files) > 0 && m.ws.listCursor < len(fld.Files) {
			return m.libraryTrackCoverKey(fld.Files[m.ws.listCursor])
		}
		flds := m.filteredFolders()
		if len(flds) > 0 && m.ws.listCursor < len(flds) && len(flds[m.ws.listCursor].Files) > 0 {
			return m.libraryTrackCoverKey(flds[m.ws.listCursor].Files[0])
		}
	case secSources:
		if m.ws.sourceID != "" {
			return m.ws.sourceDetail.Cover
		}
		srcs := m.filteredSources()
		if len(srcs) > 0 && m.ws.listCursor < len(srcs) {
			return srcs[m.ws.listCursor].Cover
		}
	case secIssues:
		iss := m.filteredIssues()
		if len(iss) > 0 && m.ws.listCursor < len(iss) && iss[m.ws.listCursor].LibraryRow != nil {
			return m.libraryTrackCoverKey(*iss[m.ws.listCursor].LibraryRow)
		}
	case secArtists:
		return m.artistHighlightedCover()
	}
	return ""
}

func (m model) workspaceActions() []wsAction {
	switch m.ws.section {
	case secDetails:
		var acts []wsAction
		if len(m.ws.issues) > 0 {
			acts = append(acts, wsAction{"details_issues", "Review problems (4)"})
		}
		return append(acts,
			wsAction{"details_albums", "Open albums (1)"},
			wsAction{"details_sources", "Open sources (3)"},
			wsAction{"change_dest", "Choose another folder (c)"},
		)
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			acts := []wsAction{{"back_album", "Back to albums"}}
			tracks := m.filteredAlbumTracks(alb)
			if m.ws.listCursor < len(tracks) {
				tr := tracks[m.ws.listCursor]
				if alb.HasSource {
					acts = append(acts, wsAction{"exclude_tracks", "Remove from this album (-)"})
				}
				acts = append(acts, wsAction{"delete_permanent", "Delete from all lists (d)"})
				if tr.Status == "failed" || tr.Status == "missing" {
					acts = append(acts, wsAction{"retry_track", "Retry download"})
				}
				if tr.Status == "unavailable" || tr.Status == "failed" {
					acts = append(acts, wsAction{"set_alt", "Set alternative link…"})
				}
				if tr.LibraryRow != nil && !tr.LibraryRow.Missing {
					acts = append(acts, wsAction{"organize_selected", "Organize / convert…"})
					acts = append(acts, wsAction{"reset_selected", "Reset & redownload"})
				}
			}
			acts = append(acts, wsAction{"repair", "Repair / relink files"})
			if alb.Artist != "" {
				acts = append(acts, wsAction{"find_artist", "Find artist online"})
			}
			acts = append(acts, wsAction{"scan", "Scan device"})
			return acts
		}
		albs := m.filteredAlbums()
		var acts []wsAction
		if len(albs) > 0 && m.ws.listCursor < len(albs) {
			alb := albs[m.ws.listCursor]
			acts = append(acts, wsAction{"open_album", "Open tracks"})
			if alb.HasSource && alb.SourceURL != "" {
				if alb.Failed > 0 || alb.Missing > 0 {
					acts = append(acts, wsAction{"retry_album", "Retry failed / missing"})
				}
				acts = append(acts, wsAction{"sync_album", "Sync / download"})
			}
			acts = append(acts,
				wsAction{"organize_selected", "Organize folders…"},
				wsAction{"reset_selected", "Reset & redownload"},
				wsAction{"delete_permanent", "Delete album permanently (d)"},
			)
			if alb.Artist != "" {
				acts = append(acts, wsAction{"find_artist", "Find artist online"})
			}
		}
		acts = append(acts, wsAction{"repair", "Repair / relink all"}, wsAction{"scan", "Scan device"})
		return acts

	case secFolders:
		if _, ok := m.activeFolder(); ok {
			return []wsAction{
				{"back_folder", "Back to folders"},
				{"organize_selected", "Organize / convert…"},
				{"reset_selected", "Reset & redownload"},
				{"delete_permanent", "Delete permanently (d)"},
				{"repair", "Repair / relink files"},
				{"scan", "Scan device"},
			}
		}
		flds := m.filteredFolders()
		var acts []wsAction
		if len(flds) > 0 {
			acts = append(acts,
				wsAction{"open_folder", "Open folder files"},
				wsAction{"organize_selected", "Organize folder…"},
				wsAction{"delete_permanent", "Delete folder audio (d)"},
			)
		}
		acts = append(acts, wsAction{"repair", "Repair / relink all"}, wsAction{"scan", "Scan device"})
		return acts

	case secSources:
		if m.ws.sourceID != "" {
			acts := []wsAction{{"readd_track", "Re-add track…"}, {"back_source", "Back to sources"}}
			if len(m.filteredSourceTracks()) > 0 {
				acts = append(acts, wsAction{"exclude_tracks", "Remove from this list (-)"}, wsAction{"delete_permanent", "Delete from all lists (d)"})
			}
			return append(acts,
				wsAction{"retry_source", "Retry errors"},
				wsAction{"sync_source", "Sync source"},
				wsAction{"refresh_source", "Refresh metadata"},
				wsAction{"repair", "Repair / relink files"},
			)
		}
		srcs := m.filteredSources()
		var acts []wsAction
		if len(srcs) > 0 {
			acts = append(acts,
				wsAction{"add_link", "Add link…"},
				wsAction{"sync_picked", "Sync selected"},
				wsAction{"download_picked", "Download / resume"},
				wsAction{"open_source", "Open tracks & files"},
				wsAction{"find_artist", "Find selected artist"},
				wsAction{"remove_source", "Remove source (keep audio)"},
			)
		} else {
			acts = append(acts, wsAction{"add_link", "Add link…"})
		}
		acts = append(acts,
			wsAction{"repair", "Repair / relink all"},
			wsAction{"export_m3u", "Export playlists"},
		)
		return acts

	case secIssues:
		var acts []wsAction
		if cur, found := m.currentProblem(); found {
			switch cur.Kind {
			case "download_failed":
				acts = append(acts, wsAction{"retry_issue", "Retry download"}, wsAction{"set_alt", "Set alternative link…"}, wsAction{"delete_permanent", "Delete from library (d)"})
			case "unavailable":
				acts = append(acts, wsAction{"set_alt", "Set alternative link…"}, wsAction{"retry_issue", "Retry download"}, wsAction{"delete_permanent", "Delete from library (d)"})
			case "missing_file":
				acts = append(acts, wsAction{"retry_issue", "Download missing file"}, wsAction{"repair", "Repair / relink files"})
			case "relink_ready":
				acts = append(acts, wsAction{"repair", "Repair / relink files"}, wsAction{"delete_permanent", "Delete permanently (d)"})
			case "corrupt_file":
				acts = append(acts, wsAction{"reset_selected", "Reset & redownload"}, wsAction{"delete_permanent", "Delete permanently (d)"})
			case "duplicate", "unlisted":
				acts = append(acts, wsAction{"delete_permanent", "Delete permanently (d)"}, wsAction{"repair", "Repair / relink files"})
			}
		}
		acts = append(acts, wsAction{"restore_trash", "Restore last trash"}, wsAction{"scan", "Scan device"})
		return acts

	case secArtists:
		if m.artist.artist != nil {
			return []wsAction{
				{"artist_download", "Download selected"},
				{"artist_preview", "Preview tracks"},
				{"artist_save_meta", "Save metadata only"},
				{"artist_search", "New artist search…"},
			}
		}
		return []wsAction{
			{"artist_open", "Open selected artist"},
			{"artist_search", "Search artist…"},
			{"artist_saved", "Show saved artists"},
		}

	case secSettings:
		return []wsAction{
			{"setting_run", "Change / run selected"},
			{"change_dest", "Change device or folder"},
			{"restore_trash", "Restore last trash"},
			{"empty_trash", "Empty trash permanently"},
			{"scan", "Scan device"},
		}
	}
	return nil
}

func (m *model) workspaceKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.syncWorkspaceFromState()
	key := k.String()
	if m.ws.readd != nil {
		return m.trackReaddKey(k)
	}
	if m.ws.deleteReview != nil || m.ws.removing {
		if key == "x" && m.ws.removing && m.library.cancel != nil {
			m.library.cancel()
		}
		if !k.Paste {
			return m.workspaceRemovalKey(key)
		}
		return *m, nil
	}

	// Reuse existing modal confirmation / preview / report / prompt flows safely.
	if m.library.removeSource != nil || m.library.help || m.library.report != nil ||
		m.library.details != nil || m.library.purge || m.library.restore ||
		m.library.plan != nil || m.library.candidates != nil || m.library.confirm != nil || m.library.tools {
		next, cmd := m.libraryKey(k)
		if nm, ok := next.(model); ok {
			if !nm.libraryOverlayVisible() {
				switch nm.screen {
				case screenSettings:
					nm.ws.section = secSettings
					nm.ws.menuCursor = int(secSettings)
					nm.ws.listCursor = nm.cursor
				case screenSources:
					nm.ws.section = secSources
					nm.ws.menuCursor = int(secSources)
					nm.ws.listCursor = nm.cursor
				}
				nm.ws.lastScreen = nm.screen
				nm.refreshWorkspace()
			}
			return nm, cmd
		}
		return next, cmd
	}
	if m.prompt != promptNone {
		return m.promptKey(k)
	}
	if !k.Paste && key == "?" {
		m.shortcutHelp, m.shortcutCursor = true, 0
		return *m, nil
	}
	if m.ws.searching {
		switch key {
		case "esc":
			m.ws.searching = false
			m.input.Blur()
			m.clampWorkspaceCursor()
			return *m, nil
		case "enter":
			m.ws.query = m.input.Value()
			m.ws.searching = false
			m.input.Blur()
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
			return *m, m.requestCover(m.workspaceCoverKey())
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			m.ws.query = m.input.Value()
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
			return *m, cmd
		}
	}
	if m.screen == screenCollection {
		next, cmd := m.collectionKey(k)
		if nm, ok := next.(model); ok {
			if nm.screen != screenCollection {
				nm.refreshWorkspace()
				nm.syncWorkspaceFromState()
			}
			return nm, cmd
		}
		return next, cmd
	}

	if k.Paste {
		return *m, m.paste(string(k.Runes))
	}

	// Artist discography retains its own 1-3 tabs.
	if !(m.ws.section == secArtists && m.artist.artist != nil && (key == "1" || key == "2" || key == "3")) {
		switch key {
		case "0":
			return *m, m.setSection(secDetails)
		case "1":
			return *m, m.setSection(secAlbums)
		case "2":
			return *m, m.setSection(secFolders)
		case "3":
			return *m, m.setSection(secSources)
		case "4":
			return *m, m.setSection(secIssues)
		case "5":
			return *m, m.setSection(secArtists)
		case "6":
			return *m, m.setSection(secSettings)
		}
	}

	// Pane & top-menu switching: Tab toggles List <-> Actions (2 columns); [/] switches tabs.
	switch key {
	case "tab":
		if m.ws.focus == paneList {
			m.ws.focus = paneSide
		} else {
			m.ws.focus = paneList
		}
		m.clampWorkspaceCursor()
		return *m, nil
	case "shift+tab":
		if m.ws.focus == paneMenu {
			m.ws.focus = paneList
		} else {
			m.ws.focus = paneMenu
		}
		m.clampWorkspaceCursor()
		return *m, nil
	case "[":
		prev := (int(m.ws.section.tab()) + len(sectionNames) - 1) % len(sectionNames)
		return *m, m.setSection(navSection(prev))
	case "]":
		next := (int(m.ws.section.tab()) + 1) % len(sectionNames)
		return *m, m.setSection(navSection(next))
	}

	// Legacy global shortcuts kept working as optional accelerators
	switch key {
	case "?":
		m.shortcutHelp, m.shortcutCursor = true, 0
		return *m, nil
	case "g":
		return *m, m.setSection(secSources)
	case "l":
		if m.ws.focus == paneList && m.ws.section == secAlbums && m.ws.albumKey != "" {
			m.ws.albumKey = ""
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
			return *m, m.requestCover(m.workspaceCoverKey())
		}
		if m.ws.section != secSettings && m.ws.section != secArtists {
			return *m, m.setSection(secAlbums)
		}
	case "O", "o":
		return *m, m.setSection(secSettings)
	case "/":
		if m.ws.section != secDetails && m.ws.section != secSettings && m.ws.section != secArtists {
			m.ws.searching = true
			m.ws.focus = paneList
			m.input.SetValue(m.ws.query)
			m.input.Placeholder = "Filter albums, folders, files, errors…"
			m.input.CursorEnd()
			return *m, m.input.Focus()
		}
	case "u":
		m.screen = screenLibrary
		m.library.restore = true
		return *m, nil
	case "f":
		return *m, m.ask(promptArtist, "Artist name, link, or Enter for downloaded artists", "")
	case "F":
		return m.runWorkspaceAction("find_artist")
	case "c":
		if m.ws.section != secArtists && m.ws.section != secSources {
			m.changeDestination()
			return *m, nil
		}
	case "ctrl+home":
		if len(m.logs) > 0 {
			m.scrollLogs(len(m.logLines()))
			return *m, nil
		}
	case "ctrl+end":
		if len(m.logs) > 0 {
			m.logScroll = 0
			return *m, nil
		}
	case "pgup":
		if len(m.logs) > 0 {
			m.scrollLogs(m.logPage())
			return *m, nil
		}
	case "pgdown":
		if len(m.logs) > 0 {
			m.scrollLogs(-m.logPage())
			return *m, nil
		}
	}

	switch m.ws.focus {
	case paneMenu:
		return m.workspaceMenuKey(k)
	case paneSide:
		return m.workspaceSideKey(k)
	default:
		return m.workspaceListKey(k)
	}
}

func (m *model) workspaceMenuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	wide := m.terminalWidth() >= 72 && m.terminalHeight() >= 16
	switch k.String() {
	case "left", "h":
		m.ws.menuCursor = (m.ws.menuCursor + len(sectionNames) - 1) % len(sectionNames)
		return *m, m.switchSection(navSection(m.ws.menuCursor), paneMenu)
	case "right", "l":
		if !wide {
			return *m, m.switchSection(navSection(m.ws.menuCursor), paneList)
		}
		m.ws.menuCursor = (m.ws.menuCursor + 1) % len(sectionNames)
		return *m, m.switchSection(navSection(m.ws.menuCursor), paneMenu)
	case "up", "k":
		if !wide {
			m.ws.menuCursor = max(0, m.ws.menuCursor-1)
			return *m, m.switchSection(navSection(m.ws.menuCursor), paneMenu)
		}
	case "down", "j":
		if !wide {
			m.ws.menuCursor = min(len(sectionNames)-1, m.ws.menuCursor+1)
			return *m, m.switchSection(navSection(m.ws.menuCursor), paneMenu)
		}
		m.ws.focus = paneList
		return *m, nil
	case "enter", "esc":
		m.ws.focus = paneList
		return *m, nil
	}
	return *m, nil
}

func (m *model) workspaceSideKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	acts := m.workspaceActions()
	switch k.String() {
	case "up", "k":
		m.ws.sideCursor = max(0, m.ws.sideCursor-1)
	case "down", "j":
		m.ws.sideCursor = min(max(0, len(acts)-1), m.ws.sideCursor+1)
	case "left", "h", "esc":
		m.ws.focus = paneList
	case "enter":
		if len(acts) > 0 && m.ws.sideCursor < len(acts) {
			return m.runWorkspaceAction(acts[m.ws.sideCursor].id)
		}
	}
	return *m, nil
}

func (m *model) workspaceListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()

	if m.ws.section == secArtists {
		if key == "left" && m.artist.artist == nil {
			m.ws.focus = paneMenu
			return *m, nil
		}
		if key == "right" && m.artist.artist == nil {
			m.ws.focus = paneSide
			return *m, nil
		}
		if key == "esc" && m.artist.artist == nil && len(m.artist.results) == 0 && !m.artist.searching && !m.artist.loading {
			m.ws.focus = paneMenu
			return *m, nil
		}
		next, cmd := m.artistKey(k)
		nm := next.(model)
		nm.ws.listCursor = nm.cursor
		return nm, cmd
	}
	if m.ws.section == secSettings {
		switch key {
		case "left":
			if m.ws.listCursor >= moveFiles {
				m.ws.focus = paneMenu
				return *m, nil
			}
		case "right":
			if m.ws.listCursor >= moveFiles {
				m.ws.focus = paneSide
				m.clampWorkspaceCursor()
				return *m, nil
			}
		case "esc":
			m.ws.focus = paneMenu
			return *m, nil
		case "enter":
			if m.ws.listCursor < moveFiles {
				m.cursor = m.ws.listCursor
				m.changeSetting(1)
				return *m, nil
			}
		}
		next, cmd := m.settingsKey(k)
		nm := next.(model)
		nm.ws.listCursor = nm.cursor
		if nm.screen != screenSettings && nm.screen != screenBrowse {
			// settings action opened tools/library or sources
			if nm.screen == screenSources {
				nm.ws.section = secSources
				nm.ws.menuCursor = int(secSources)
			} else if nm.screen == screenLibrary {
				nm.ws.section = secAlbums
				nm.ws.menuCursor = int(secAlbums)
			}
			nm.ws.lastScreen = nm.screen
		}
		return nm, cmd
	}

	n := m.workspaceListLen()
	moved := false
	switch key {
	case "up", "k":
		m.ws.listCursor = max(0, m.ws.listCursor-1)
		moved = true
	case "down", "j":
		m.ws.listCursor = min(max(0, n-1), m.ws.listCursor+1)
		moved = true
	case "pgup":
		m.ws.listCursor = max(0, m.ws.listCursor-max(1, m.terminalHeight()/3))
		moved = true
	case "pgdown":
		m.ws.listCursor = min(max(0, n-1), m.ws.listCursor+max(1, m.terminalHeight()/3))
		moved = true
	case "home":
		m.ws.listCursor = 0
		moved = true
	case "end":
		m.ws.listCursor = max(0, n-1)
		moved = true
	}
	if moved {
		m.clampWorkspaceCursor()
		if m.ws.albumKey == "" && m.ws.folderDir == "" && m.ws.sourceID == "" {
			m.cursor = m.ws.listCursor
		}
		return *m, m.requestCover(m.workspaceCoverKey())
	}
	switch key {
	case "left", "h":
		switch {
		case m.ws.section == secAlbums && m.ws.albumKey != "":
			m.ws.albumKey = ""
			m.ws.listCursor = m.ws.parentCursor
			m.clampWorkspaceCursor()
			return *m, m.requestCover(m.workspaceCoverKey())
		case m.ws.section == secFolders && m.ws.folderDir != "":
			m.ws.folderDir = ""
			m.ws.listCursor = m.ws.parentCursor
			m.clampWorkspaceCursor()
			return *m, m.requestCover(m.workspaceCoverKey())
		case m.ws.section == secSources && m.ws.sourceID != "":
			m.ws.sourceID = ""
			m.ws.listCursor = m.ws.parentCursor
			m.clampWorkspaceCursor()
			return *m, m.requestCover(m.workspaceCoverKey())
		default:
			m.ws.focus = paneMenu
			return *m, nil
		}
	case "right":
		m.ws.focus = paneSide
		m.clampWorkspaceCursor()
		return *m, nil
	case "esc":
		switch {
		case m.ws.section == secIssues:
			return *m, m.setSection(secDetails)
		case m.ws.section == secAlbums && m.ws.albumKey != "":
			m.ws.albumKey = ""
			m.ws.listCursor = m.ws.parentCursor
		case m.ws.section == secFolders && m.ws.folderDir != "":
			m.ws.folderDir = ""
			m.ws.listCursor = m.ws.parentCursor
		case m.ws.section == secSources && m.ws.sourceID != "":
			m.ws.sourceID = ""
			m.ws.listCursor = m.ws.parentCursor
		case m.ws.query != "":
			m.ws.query = ""
			m.ws.listCursor = 0
		default:
			m.ws.focus = paneMenu
		}
		m.clampWorkspaceCursor()
		return *m, m.requestCover(m.workspaceCoverKey())
	case " ":
		m.toggleWorkspaceMark()
		return *m, nil
	case "enter":
		switch m.ws.section {
		case secDetails:
			if len(m.ws.issues) > 0 {
				return *m, m.setSection(secIssues)
			}
			return *m, m.setSection(secAlbums)
		case secAlbums:
			if m.ws.albumKey == "" {
				albs := m.filteredAlbums()
				if len(albs) > 0 && m.ws.listCursor < len(albs) {
					m.ws.parentCursor = m.ws.listCursor
					m.ws.albumKey = albs[m.ws.listCursor].Key
					m.ws.listCursor = 0
					m.clampWorkspaceCursor()
					return *m, m.requestCover(m.workspaceCoverKey())
				}
			} else {
				m.ws.focus = paneSide
				m.clampWorkspaceCursor()
			}
		case secFolders:
			if m.ws.folderDir == "" {
				flds := m.filteredFolders()
				if len(flds) > 0 && m.ws.listCursor < len(flds) {
					m.ws.parentCursor = m.ws.listCursor
					m.ws.folderDir = flds[m.ws.listCursor].Dir
					m.ws.listCursor = 0
					m.clampWorkspaceCursor()
					return *m, m.requestCover(m.workspaceCoverKey())
				}
			} else {
				m.ws.focus = paneSide
				m.clampWorkspaceCursor()
			}
		case secSources:
			if m.ws.sourceID == "" {
				srcs := m.filteredSources()
				if len(srcs) > 0 && m.ws.listCursor < len(srcs) {
					m.ws.parentCursor = m.ws.listCursor
					m.ws.sourceID = srcs[m.ws.listCursor].ID
					m.ws.sourceDetail = m.eng.CatalogCollection(m.ws.sourceID)
					m.ws.listCursor = 0
					m.clampWorkspaceCursor()
					return *m, m.requestCover(m.workspaceCoverKey())
				}
			} else {
				m.ws.focus = paneSide
				m.clampWorkspaceCursor()
			}
		case secIssues:
			if action := m.primaryProblemAction(); action != "" {
				return m.runWorkspaceAction(action)
			}
			if _, found := m.currentProblem(); found {
				m.ws.focus = paneSide
				m.clampWorkspaceCursor()
			}
		}
		return *m, nil
	// Helpful single-key shortcuts for power users
	case "S", "v":
		if m.ws.section == secSources && m.ws.sourceID == "" {
			return m.runWorkspaceAction("open_source")
		}
	case "s":
		if m.ws.section == secSources && !m.run.busy {
			return m.runWorkspaceAction("sync_picked")
		}
		return m.runWorkspaceAction("scan")
	case "r":
		if m.ws.section == secIssues {
			return m.runWorkspaceAction("retry_issue")
		}
		return m.runWorkspaceAction("repair")
	case "d":
		if m.ws.section == secSources && m.ws.sourceID == "" {
			return m.runWorkspaceAction("remove_source")
		}
		return m.runWorkspaceAction("delete_permanent")
	case "-":
		return m.runWorkspaceAction("exclude_tracks")
	case "a":
		if m.ws.section == secSources && m.ws.sourceID == "" {
			return m.runWorkspaceAction("add_link")
		}
		fallthrough
	case "ctrl+a":
		m.toggleAllVisibleMarks()
		return *m, nil
	case "p":
		m.togglePause()
	case "x":
		if m.run.busy {
			m.run.cancel()
		} else if m.library.scanning && m.library.cancel != nil {
			m.library.cancel()
		}
	}
	return *m, nil
}

func (m *model) toggleAllVisibleMarks() {
	switch m.ws.section {
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			setVisibleMarks(m.ws.markedTracks, m.filteredAlbumTracks(alb), func(tr engine.UnifiedTrack) string {
				return alb.Key + "\x00" + tr.ID
			})
		} else {
			setVisibleMarks(m.ws.markedAlbums, m.filteredAlbums(), func(a engine.UnifiedAlbum) string { return a.Key })
		}
	case secFolders:
		if fld, ok := m.activeFolder(); ok {
			setVisibleMarks(m.ws.markedFiles, m.filteredFolderFiles(fld), func(f engine.LibraryTrack) string { return f.Path })
		} else {
			setVisibleMarks(m.ws.markedFolders, m.filteredFolders(), func(f engine.PhysicalFolder) string { return f.Dir })
		}
	case secSources:
		if m.ws.sourceID != "" {
			setVisibleMarks(m.ws.markedSourceTracks, m.filteredSourceTracks(), func(tr engine.CollectionTrack) string {
				return m.ws.sourceID + "\x00" + tr.ID
			})
		} else {
			setVisibleMarks(m.marked, m.filteredSources(), func(s engine.SourceView) string { return s.ID })
		}
	case secIssues:
		setVisibleMarks(m.ws.markedIssues, m.filteredIssues(), func(it engine.IssueItem) string { return it.TrackID + ":" + it.Path })
	}
}

func (m *model) toggleWorkspaceMark() {
	switch m.ws.section {
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			tracks := m.filteredAlbumTracks(alb)
			if m.ws.listCursor < len(tracks) {
				id := alb.Key + "\x00" + tracks[m.ws.listCursor].ID
				m.ws.markedTracks[id] = !m.ws.markedTracks[id]
			}
		} else if albs := m.filteredAlbums(); len(albs) > 0 && m.ws.listCursor < len(albs) {
			k := albs[m.ws.listCursor].Key
			m.ws.markedAlbums[k] = !m.ws.markedAlbums[k]
		}
	case secFolders:
		if fld, ok := m.activeFolder(); ok {
			files := m.filteredFolderFiles(fld)
			if m.ws.listCursor < len(files) {
				p := files[m.ws.listCursor].Path
				m.ws.markedFiles[p] = !m.ws.markedFiles[p]
			}
		} else if flds := m.filteredFolders(); len(flds) > 0 && m.ws.listCursor < len(flds) {
			d := flds[m.ws.listCursor].Dir
			m.ws.markedFolders[d] = !m.ws.markedFolders[d]
		}
	case secSources:
		if m.ws.sourceID != "" {
			if tracks := m.filteredSourceTracks(); m.ws.listCursor < len(tracks) {
				key := m.ws.sourceID + "\x00" + tracks[m.ws.listCursor].ID
				m.ws.markedSourceTracks[key] = !m.ws.markedSourceTracks[key]
			}
		} else {
			if srcs := m.filteredSources(); len(srcs) > 0 && m.ws.listCursor < len(srcs) {
				id := srcs[m.ws.listCursor].ID
				m.marked[id] = !m.marked[id]
			}
		}
	case secIssues:
		if iss := m.filteredIssues(); len(iss) > 0 && m.ws.listCursor < len(iss) {
			k := iss[m.ws.listCursor].TrackID + ":" + iss[m.ws.listCursor].Path
			m.ws.markedIssues[k] = !m.ws.markedIssues[k]
		}
	}
}

func (m model) selectedPhysicalRows() []engine.LibraryTrack {
	return m.selectedWorkspaceRows(false)
}

func (m *model) runWorkspaceAction(action string) (tea.Model, tea.Cmd) {
	m.ws.focus = paneList
	switch action {
	case "details_albums":
		return *m, m.setSection(secAlbums)
	case "details_sources":
		return *m, m.setSection(secSources)
	case "details_issues":
		return *m, m.setSection(secIssues)
	case "readd_track":
		m.openTrackReadd()
		return *m, nil
	case "exclude_tracks", "delete_permanent":
		return m.startWorkspaceRemoval(action)
	case "open_album":
		albs := m.filteredAlbums()
		if m.ws.listCursor < len(albs) {
			m.ws.parentCursor = m.ws.listCursor
			m.ws.albumKey = albs[m.ws.listCursor].Key
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
		}
		return *m, m.requestCover(m.workspaceCoverKey())
	case "back_album":
		m.ws.albumKey = ""
		m.ws.listCursor = m.ws.parentCursor
		m.clampWorkspaceCursor()
		return *m, m.requestCover(m.workspaceCoverKey())
	case "open_folder":
		flds := m.filteredFolders()
		if m.ws.listCursor < len(flds) {
			m.ws.parentCursor = m.ws.listCursor
			m.ws.folderDir = flds[m.ws.listCursor].Dir
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
		}
		return *m, m.requestCover(m.workspaceCoverKey())
	case "back_folder":
		m.ws.folderDir = ""
		m.ws.listCursor = m.ws.parentCursor
		m.clampWorkspaceCursor()
		return *m, m.requestCover(m.workspaceCoverKey())
	case "open_source":
		srcs := m.filteredSources()
		if m.ws.listCursor < len(srcs) {
			m.ws.parentCursor = m.ws.listCursor
			m.ws.sourceID = srcs[m.ws.listCursor].ID
			m.ws.sourceDetail = m.eng.CatalogCollection(m.ws.sourceID)
			m.ws.listCursor = 0
			m.clampWorkspaceCursor()
		}
		return *m, m.requestCover(m.workspaceCoverKey())
	case "back_source":
		m.ws.sourceID = ""
		m.ws.listCursor = m.ws.parentCursor
		m.clampWorkspaceCursor()
		return *m, m.requestCover(m.workspaceCoverKey())
	case "scan":
		if m.library.scanning {
			m.log("Folder scan already in progress.")
			return *m, nil
		}
		return *m, m.scanLibrary()
	case "repair":
		if m.run.busy {
			m.log("Wait for downloads to finish before repairing.")
			return *m, nil
		}
		return *m, m.start(nil, "repaired", m.eng.Repair)
	case "restore_trash":
		m.screen = screenLibrary
		m.library.restore = true
		return *m, nil
	case "empty_trash":
		m.screen = screenLibrary
		m.library.purge = true
		return *m, nil
	case "change_dest":
		m.changeDestination()
		return *m, nil
	case "add_link":
		return *m, m.ask(promptURLs, "Paste YouTube, SoundCloud or Bandcamp links", "")
	case "export_m3u":
		if !m.run.busy {
			return *m, m.start(nil, "exported", m.eng.ExportM3U)
		}
	case "download_picked":
		if !m.run.busy {
			urls := m.pickedURLs(m.filteredSources())
			if len(urls) > 0 || m.ws.activeQuery() == "" && m.eng.HasPending() {
				return *m, m.download(urls)
			}
		}
	case "sync_picked":
		if !m.run.busy {
			if urls := m.pickedURLs(m.filteredSources()); len(urls) > 0 {
				return *m, m.sync(urls)
			}
		}
	case "sync_source":
		if !m.run.busy && m.ws.sourceDetail.URL != "" {
			return *m, m.sync([]string{m.ws.sourceDetail.URL})
		}
	case "retry_source":
		if !m.run.busy && m.ws.sourceID != "" && m.ws.sourceDetail.URL != "" {
			n, err := m.eng.RetrySource(m.ws.sourceID)
			if err != nil {
				m.fail(err)
				return *m, nil
			}
			if n == 0 {
				m.log("No failed or missing tracks in this source.")
				return *m, nil
			}
			return *m, m.download([]string{m.ws.sourceDetail.URL})
		}
	case "refresh_source":
		if m.ws.sourceDetail.URL != "" && !m.artist.inspecting {
			m.artist.inspecting = true
			eng := m.eng
			rawURL := m.ws.sourceDetail.URL
			return *m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
				defer cancel()
				listing, err := eng.Inspect(ctx, rawURL)
				return inspectLoadedMsg{eng: eng, from: screenCollection, url: rawURL, listing: listing, save: true, err: err}
			}
		}
	case "remove_source":
		if !m.run.busy && m.ws.listCursor < len(m.filteredSources()) {
			s := m.filteredSources()[m.ws.listCursor]
			m.screen = screenLibrary
			m.library.removeSource = &s
			return *m, nil
		}
	case "sync_album", "retry_album":
		albs := m.filteredAlbums()
		if !m.run.busy && m.ws.listCursor < len(albs) {
			alb := albs[m.ws.listCursor]
			if action == "retry_album" && alb.SourceID != "" {
				if _, err := m.eng.RetrySource(alb.SourceID); err != nil {
					m.fail(err)
					return *m, nil
				}
			}
			if alb.SourceURL != "" {
				return *m, m.download([]string{alb.SourceURL})
			}
		}
	case "retry_track":
		if alb, ok := m.activeAlbum(); ok && alb.SourceURL != "" {
			if tracks := m.filteredAlbumTracks(alb); m.ws.listCursor < len(tracks) {
				if err := m.eng.Retry(tracks[m.ws.listCursor].ID); err != nil {
					m.fail(err)
					return *m, nil
				}
				return *m, m.download([]string{alb.SourceURL})
			}
		}
	case "retry_issue":
		cmd := m.retryProblem()
		return *m, cmd
	case "set_alt":
		if m.ws.section == secIssues {
			if item, found := m.currentProblem(); found && item.TrackID != "" {
				m.altFor = item.TrackID
				return *m, m.ask(promptAlt, "YouTube Music link for “"+cut(item.Title, 36)+"”", "")
			}
		} else if alb, ok := m.activeAlbum(); ok {
			if tracks := m.filteredAlbumTracks(alb); m.ws.listCursor < len(tracks) {
				tr := tracks[m.ws.listCursor]
				m.altFor = tr.ID
				return *m, m.ask(promptAlt, "YouTube Music link for “"+cut(tr.Title, 36)+"”", "")
			}
		}
	case "reset_selected":
		if m.run.busy {
			m.log("Wait for downloads to finish before modifying audio files.")
			return *m, nil
		}
		if m.library.scanning || !m.library.complete {
			m.log("Scanning device files before modifying audio…")
			if m.library.scanning {
				return *m, nil
			}
			return *m, m.scanLibrary()
		}
		rows := m.selectedPhysicalRows()
		if len(rows) == 0 {
			m.log("No physical files selected for this action.")
			return *m, nil
		}
		m.screen = screenLibrary
		m.library.candidates = rows
		m.library.reset = true
		m.library.albums = m.ws.section == secAlbums && m.ws.albumKey == ""
		m.library.includeShared = true
		m.reviewDeletion()
		return *m, nil
	case "organize_selected":
		if m.run.busy {
			m.log("Wait for downloads to finish before organizing files.")
			return *m, nil
		}
		if m.library.scanning || !m.library.complete {
			m.log("Scanning device files first…")
			if m.library.scanning {
				return *m, nil
			}
			return *m, m.scanLibrary()
		}
		rows := m.selectedPhysicalRows()
		if len(rows) == 0 {
			m.log("No physical files selected to organize.")
			return *m, nil
		}
		m.screen = screenLibrary
		m.library.candidates = nil
		m.library.marked = map[string]bool{}
		for _, r := range rows {
			m.library.marked[r.ID] = true
		}
		m.library.targetFormat = "keep"
		m.library.layout = m.opt.Folders
		m.library.tools = true
		m.cursor = 0
		return *m, nil
	case "find_artist":
		artist := ""
		switch m.ws.section {
		case secAlbums:
			if alb, ok := m.activeAlbum(); ok {
				artist = alb.Artist
			} else if albs := m.filteredAlbums(); m.ws.listCursor < len(albs) {
				artist = albs[m.ws.listCursor].Artist
			}
		case secSources:
			if srcs := m.filteredSources(); m.ws.listCursor < len(srcs) {
				artist = srcs[m.ws.listCursor].Creator
			}
		}
		if artist != "" {
			return *m, m.openArtistQuery(artist)
		}
		return *m, m.openSavedArtists()
	case "artist_search":
		return *m, m.ask(promptArtist, "Artist name, link, or Enter for downloaded artists", "")
	case "artist_saved":
		return *m, m.openSavedArtists()
	case "artist_open", "artist_download":
		next, cmd := m.artistKey(tea.KeyMsg{Type: tea.KeyEnter})
		return next, cmd
	case "artist_preview":
		next, cmd := m.artistKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
		return next, cmd
	case "artist_save_meta":
		next, cmd := m.artistKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
		return next, cmd
	case "setting_run":
		next, cmd := m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
		return next, cmd
	}
	return *m, nil
}
