package tui

import (
	"strings"
	"testing"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func TestRescanClearsReplacedAndExpandedWorkspaceMarks(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{ID: "replaced", Title: "Replaced"}, {ID: "same", Title: "Same"}, {ID: "lost-hash", Title: "Lost hash"}}
	data.Merge("album", "album", "Album", "https://music.youtube.com/playlist?list=album", entries)
	data.Merge("playlist", "list", "List", "https://music.youtube.com/playlist?list=list", append(entries, manifest.Entry{ID: "pending", Title: "Pending"}))
	before := []engine.LibraryTrack{
		{ID: "replaced", Title: "Replaced", Path: "Album/Replaced.opus", Hash: "old", Size: 10, Modified: 1, Invalid: true},
		{ID: "same", Title: "Same", Path: "Album/Same.opus", Hash: "same", Size: 10, Modified: 1, Invalid: true},
		{ID: "lost-hash", Title: "Lost hash", Path: "Album/Lost.opus", Hash: "previous", Size: 10, Modified: 1, Invalid: true},
		{ID: "file:Steady/a.opus", Title: "Steady", Path: "Steady/a.opus", Hash: "steady", Unmanaged: true},
		{ID: "file:Expanded/a.opus", Title: "First", Path: "Expanded/a.opus", Hash: "first", Unmanaged: true},
	}
	for _, row := range before[:3] {
		data.Tracks[row.ID].Path, data.Tracks[row.ID].Status = row.Path, manifest.StatusDone
	}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.screen = screenLibrary
	m.library.complete, m.library.all = true, before
	m.refreshWorkspace()

	for _, row := range before[:3] {
		m.ws.markedTracks["source:album\x00"+row.ID] = true
		m.ws.markedSourceTracks["list\x00"+row.ID] = true
		m.ws.markedFiles[row.Path] = true
		m.ws.markedIssues[row.ID+":"+row.Path] = true
	}
	for _, key := range []string{"source:album", "disk:Steady", "disk:Expanded"} {
		m.ws.markedAlbums[key] = true
	}
	for _, key := range []string{"Album", "Steady", "Expanded"} {
		m.ws.markedFolders[key] = true
	}
	m.ws.markedSourceTracks["list\x00pending"] = true

	m.scanLibrary()
	after := append([]engine.LibraryTrack(nil), before...)
	after[0].Hash, after[2].Hash = "different", ""
	after = append(after, engine.LibraryTrack{ID: "file:Expanded/b.opus", Path: "Expanded/b.opus", Hash: "new", Unmanaged: true})
	next, _ := m.Update(libraryScannedMsg{scan: m.library.scan, tracks: after})
	m = next.(model)
	for _, row := range before[:3] {
		want := row.ID == "same"
		if m.ws.markedTracks["source:album\x00"+row.ID] != want ||
			m.ws.markedSourceTracks["list\x00"+row.ID] != want ||
			m.ws.markedFiles[row.Path] != want ||
			m.ws.markedIssues[row.ID+":"+row.Path] != want {
			t.Errorf("wrong mark state for %s: want retained=%t", row.ID, want)
		}
	}
	if m.ws.markedAlbums["source:album"] || m.ws.markedAlbums["disk:Expanded"] || !m.ws.markedAlbums["disk:Steady"] ||
		m.ws.markedFolders["Album"] || m.ws.markedFolders["Expanded"] || !m.ws.markedFolders["Steady"] {
		t.Error("rescan kept a stale aggregate mark or discarded an unchanged one")
	}
	if !m.ws.markedSourceTracks["list\x00pending"] {
		t.Error("rescan cleared a saved-only source track with unchanged identity")
	}
	if !strings.Contains(strings.Join(m.logs, "\n"), "Selection changed") {
		t.Fatalf("discarded marks were not explained: %v", m.logs)
	}
}
