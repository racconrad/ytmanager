package tui

import (
	"iter"
	"slices"

	"ytmanager/internal/engine"
)

func (m *model) refreshLibraryWorkspace() {
	album, folder := m.ws.albumKey, m.ws.folderDir
	selected := ""
	for i, key := range m.libraryWorkspaceKeys() {
		if i == m.ws.listCursor {
			selected = key
			break
		}
	}
	m.refreshWorkspace()
	if selected != "" && album == m.ws.albumKey && folder == m.ws.folderDir {
		for i, key := range m.libraryWorkspaceKeys() {
			if key == selected {
				m.ws.listCursor = i
				break
			}
		}
	}
	if m.ws.section == secAlbums && m.ws.albumKey != "" {
		if i := slices.IndexFunc(m.filteredAlbums(), func(a engine.UnifiedAlbum) bool { return a.Key == m.ws.albumKey }); i >= 0 {
			m.ws.parentCursor = i
		}
	}
	if m.ws.section == secFolders && m.ws.folderDir != "" {
		if i := slices.IndexFunc(m.filteredFolders(), func(f engine.PhysicalFolder) bool { return f.Dir == m.ws.folderDir }); i >= 0 {
			m.ws.parentCursor = i
		}
	}
}

func (m model) libraryWorkspaceKeys() iter.Seq2[int, string] {
	switch m.ws.section {
	case secAlbums:
		if album, ok := m.activeAlbum(); ok {
			return workspaceRowKeys(m.filteredAlbumTracks(album), func(t engine.UnifiedTrack) string { return t.ID })
		}
		return workspaceRowKeys(m.filteredAlbums(), func(a engine.UnifiedAlbum) string { return a.Key })
	case secFolders:
		if folder, ok := m.activeFolder(); ok {
			return workspaceRowKeys(m.filteredFolderFiles(folder), func(t engine.LibraryTrack) string { return t.Path })
		}
		return workspaceRowKeys(m.filteredFolders(), func(f engine.PhysicalFolder) string { return f.Dir })
	case secIssues:
		return workspaceRowKeys(m.filteredIssues(), issueIdentity)
	default:
		return func(func(int, string) bool) {}
	}
}

func workspaceRowKeys[T any](rows []T, key func(T) string) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		for i, row := range rows {
			if !yield(i, key(row)) {
				return
			}
		}
	}
}
