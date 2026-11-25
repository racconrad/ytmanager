package tui

import (
	"strings"

	"ytmanager/internal/engine"
)

type scanWorkspaceBaseline struct {
	albums  []engine.UnifiedAlbum
	folders []engine.PhysicalFolder
	issues  []engine.IssueItem
}

func (m *model) revalidateWorkspaceMarks(baseline scanWorkspaceBaseline, previous []engine.LibraryTrack) bool {
	removed := false
	if anyMarked(m.ws.markedFiles) {
		old, current := scanRowsBy(previous, scanRowPath), scanRowsBy(m.library.all, scanRowPath)
		removed = pruneScanMarks(m.ws.markedFiles, func(path string) bool {
			return verifiedMembersMatch(old[path], current[path], scanRowKey, verifiedTrackUnchanged)
		}) || removed
	}
	if anyMarked(m.ws.markedFolders) {
		old := scanIndex(baseline.folders, scanFolderKey)
		current := scanIndex(m.ws.folders, scanFolderKey)
		removed = pruneScanMarks(m.ws.markedFolders, func(key string) bool {
			return verifiedMembersMatch(old[key].Files, current[key].Files, scanRowKey, verifiedTrackUnchanged)
		}) || removed
	}
	if anyMarked(m.ws.markedAlbums) {
		old := scanIndex(baseline.albums, scanAlbumKey)
		current := scanIndex(m.ws.albums, scanAlbumKey)
		removed = pruneScanMarks(m.ws.markedAlbums, func(key string) bool {
			return verifiedMembersMatch(old[key].Tracks, current[key].Tracks, scanAlbumTrackKey, albumTrackUnchanged)
		}) || removed
	}
	if anyMarked(m.ws.markedTracks) {
		oldTracks, currentTracks := scanAlbumTracks(baseline.albums), scanAlbumTracks(m.ws.albums)
		removed = pruneScanMarks(m.ws.markedTracks, func(key string) bool {
			a, found := oldTracks[key]
			b, exists := currentTracks[key]
			return found && exists && albumTrackUnchanged(a, b)
		}) || removed
	}
	if anyMarked(m.ws.markedSourceTracks) {
		old, current := scanRowsBy(previous, scanRowID), scanRowsBy(m.library.all, scanRowID)
		members := map[string]map[string]engine.CollectionTrack{}
		removed = pruneScanMarks(m.ws.markedSourceTracks, func(key string) bool {
			source, id, _ := strings.Cut(key, "\x00")
			if members[source] == nil {
				members[source] = scanIndex(m.eng.CatalogCollection(source).Tracks, func(t engine.CollectionTrack) string { return t.ID })
			}
			track, exists := members[source][id]
			if !exists {
				return false
			}
			if len(old[id]) == 0 && len(current[id]) == 0 {
				return track.Path == ""
			}
			return verifiedMembersMatch(old[id], current[id], scanRowKey, verifiedTrackUnchanged)
		}) || removed
	}
	if anyMarked(m.ws.markedIssues) {
		old, current := scanRowsBy(baseline.issues, issueMarkKey), scanRowsBy(m.ws.issues, issueMarkKey)
		removed = pruneScanMarks(m.ws.markedIssues, func(key string) bool {
			return verifiedMembersMatch(old[key], current[key], issueIdentity, func(a, b engine.IssueItem) bool {
				return reviewedFileUnchanged(a.LibraryRow, b.LibraryRow, a.Path)
			})
		}) || removed
	}
	return removed
}

func pruneScanMarks(marks map[string]bool, keep func(string) bool) bool {
	removed := false
	for key, marked := range marks {
		if marked && !keep(key) {
			delete(marks, key)
			removed = true
		}
	}
	return removed
}

func verifiedMembersMatch[T any](before, after []T, key func(T) string, same func(T, T) bool) bool {
	if len(before) == 0 || len(before) != len(after) {
		return false
	}
	prior := scanIndex(before, key)
	for _, row := range after {
		k := key(row)
		old, ok := prior[k]
		if !ok || !same(old, row) {
			return false
		}
		delete(prior, k)
	}
	return len(prior) == 0
}

func scanIndex[T any](rows []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(rows))
	for _, row := range rows {
		m[key(row)] = row
	}
	return m
}

func scanRowsBy[T any](rows []T, key func(T) string) map[string][]T {
	m := make(map[string][]T)
	for _, row := range rows {
		k := key(row)
		m[k] = append(m[k], row)
	}
	return m
}

func scanAlbumTracks(albums []engine.UnifiedAlbum) map[string]engine.UnifiedTrack {
	tracks := map[string]engine.UnifiedTrack{}
	for _, album := range albums {
		for _, track := range album.Tracks {
			tracks[album.Key+"\x00"+track.ID] = track
		}
	}
	return tracks
}

func verifiedTrackUnchanged(a, b engine.LibraryTrack) bool {
	return a.ID == b.ID && a.Path != "" && a.Path == b.Path &&
		!a.Missing && !b.Missing && a.Status != "not scanned" && b.Status != "not scanned" &&
		(a.Hash == "" || b.Hash != "") && sameFileEvidence(a, b)
}

func albumTrackUnchanged(a, b engine.UnifiedTrack) bool {
	return a.ID == b.ID && a.Path == b.Path && reviewedFileUnchanged(a.LibraryRow, b.LibraryRow, a.Path)
}

func reviewedFileUnchanged(a, b *engine.LibraryTrack, path string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil && path == ""
	}
	return verifiedTrackUnchanged(*a, *b)
}

func scanRowKey(row engine.LibraryTrack) string        { return row.ID + "\x00" + row.Path }
func scanRowPath(row engine.LibraryTrack) string       { return row.Path }
func scanRowID(row engine.LibraryTrack) string         { return row.ID }
func scanFolderKey(row engine.PhysicalFolder) string   { return row.Dir }
func scanAlbumKey(row engine.UnifiedAlbum) string      { return row.Key }
func scanAlbumTrackKey(row engine.UnifiedTrack) string { return row.ID + "\x00" + row.Path }
func issueMarkKey(row engine.IssueItem) string         { return row.TrackID + ":" + row.Path }
func issueIdentity(row engine.IssueItem) string {
	return row.Kind + "\x00" + row.SourceID + "\x00" + row.TrackID + "\x00" + row.Path
}
