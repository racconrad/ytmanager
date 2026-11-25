package tui

import (
	"fmt"
	"strings"

	"ytmanager/internal/engine"
)

func selectionCount(marked, total int) string {
	return fmt.Sprintf("%d/%d selected", marked, total)
}

func (m model) filteredSourceTracks() []engine.CollectionTrack {
	terms := strings.Fields(strings.ToLower(m.ws.query))
	if len(terms) == 0 {
		return m.ws.sourceDetail.Tracks
	}
	var out []engine.CollectionTrack
	for _, tr := range m.ws.sourceDetail.Tracks {
		if matchAllTerms(strings.ToLower(tr.Title+" "+tr.Status+" "+tr.Path+" "+tr.Error), terms) {
			out = append(out, tr)
		}
	}
	return out
}

func chooseWorkspaceRows[T any](rows []T, cursor int, marked func(T) bool) []T {
	var chosen []T
	for _, row := range rows {
		if marked(row) {
			chosen = append(chosen, row)
		}
	}
	if len(chosen) == 0 && cursor >= 0 && cursor < len(rows) {
		return rows[cursor : cursor+1]
	}
	return chosen
}

func setVisibleMarks[T any](marks map[string]bool, rows []T, key func(T) string) {
	all := len(rows) > 0
	for _, row := range rows {
		all = all && marks[key(row)]
	}
	for _, row := range rows {
		id := key(row)
		if all {
			delete(marks, id)
		} else {
			marks[id] = true
		}
	}
}

func workspaceMarkCount[T any](rows []T, marked func(T) bool) int {
	n := 0
	for _, row := range rows {
		if marked(row) {
			n++
		}
	}
	return n
}

func (m model) selectedWorkspaceRows(permanent bool) []engine.LibraryTrack {
	var out []engine.LibraryTrack
	seen := map[string]bool{}
	add := func(row engine.LibraryTrack) {
		key := row.ID + "\x00" + row.Path
		if !seen[key] && (permanent || row.Path != "" && row.Status != "not scanned") {
			seen[key] = true
			out = append(out, row)
		}
	}
	addAlbumTrack := func(tr engine.UnifiedTrack) {
		if tr.LibraryRow != nil {
			add(*tr.LibraryRow)
		} else if permanent {
			add(engine.LibraryTrack{ID: tr.ID, Title: tr.Title, Path: tr.Path, Missing: tr.Path != ""})
		}
	}
	switch m.ws.section {
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			for _, tr := range chooseWorkspaceRows(m.filteredAlbumTracks(alb), m.ws.listCursor, func(tr engine.UnifiedTrack) bool {
				return m.albumTrackMarked(tr.ID)
			}) {
				addAlbumTrack(tr)
			}
		} else {
			for _, alb := range chooseWorkspaceRows(m.filteredAlbums(), m.ws.listCursor, func(a engine.UnifiedAlbum) bool {
				return m.ws.markedAlbums[a.Key]
			}) {
				for _, tr := range alb.Tracks {
					if permanent && alb.HasSource && !tr.SourceMember {
						continue
					}
					addAlbumTrack(tr)
				}
			}
		}
	case secFolders:
		if fld, ok := m.activeFolder(); ok {
			for _, row := range chooseWorkspaceRows(m.filteredFolderFiles(fld), m.ws.listCursor, func(row engine.LibraryTrack) bool {
				return m.ws.markedFiles[row.Path]
			}) {
				add(row)
			}
		} else {
			for _, fld := range chooseWorkspaceRows(m.filteredFolders(), m.ws.listCursor, func(fld engine.PhysicalFolder) bool {
				return m.ws.markedFolders[fld.Dir]
			}) {
				for _, row := range fld.Files {
					add(row)
				}
			}
		}
	case secSources:
		if permanent && m.ws.sourceID != "" {
			byID := make(map[string]engine.LibraryTrack, len(m.library.all))
			for _, row := range m.library.all {
				byID[row.ID] = row
			}
			for _, tr := range chooseWorkspaceRows(m.filteredSourceTracks(), m.ws.listCursor, func(tr engine.CollectionTrack) bool {
				return m.sourceTrackMarked(tr.ID)
			}) {
				if row, ok := byID[tr.ID]; ok {
					add(row)
				} else {
					add(engine.LibraryTrack{ID: tr.ID, Title: tr.Title, Path: tr.Path, Missing: tr.Path != ""})
				}
			}
		}
	case secIssues:
		for _, issue := range chooseWorkspaceRows(m.filteredIssues(), m.ws.listCursor, func(issue engine.IssueItem) bool {
			return m.ws.markedIssues[issue.TrackID+":"+issue.Path]
		}) {
			if issue.LibraryRow != nil {
				add(*issue.LibraryRow)
			} else if permanent && issue.TrackID != "" {
				add(engine.LibraryTrack{ID: issue.TrackID, Title: issue.Title, Path: issue.Path, Missing: issue.Path != ""})
			}
		}
	}
	return out
}

func (m model) sourceTrackMarked(id string) bool {
	return m.ws.markedSourceTracks[m.ws.sourceID+"\x00"+id]
}

func (m model) albumTrackMarked(id string) bool {
	return m.ws.markedTracks[m.ws.albumKey+"\x00"+id]
}

func (m model) collectionSelection() (string, []string) {
	var ids []string
	if m.ws.section == secSources && m.ws.sourceID != "" {
		for _, tr := range chooseWorkspaceRows(m.filteredSourceTracks(), m.ws.listCursor, func(tr engine.CollectionTrack) bool {
			return m.sourceTrackMarked(tr.ID)
		}) {
			ids = append(ids, tr.ID)
		}
		return m.ws.sourceID, ids
	}
	if alb, ok := m.activeAlbum(); m.ws.section == secAlbums && ok && alb.HasSource {
		for _, tr := range chooseWorkspaceRows(m.filteredAlbumTracks(alb), m.ws.listCursor, func(tr engine.UnifiedTrack) bool {
			return m.albumTrackMarked(tr.ID)
		}) {
			ids = append(ids, tr.ID)
		}
		return alb.SourceID, ids
	}
	return "", nil
}
