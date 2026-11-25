package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

func TestOpeningDestinationByPastedPathShowsDetailsAndScans(t *testing.T) {
	dest := t.TempDir()
	m := newModel()
	next, scan := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(dest), Paste: true})
	m = next.(model)
	if m.screen != screenLibrary || m.ws.section != secDetails || !m.library.albums || !m.library.scanning || scan == nil {
		t.Fatalf("expected destination details and a background scan: section=%v scanning=%v", m.ws.section, m.library.scanning)
	}
	m = finishTestLibraryScan(t, m, scan)
	if m.screen != screenLibrary || m.ws.section != secDetails || !m.library.complete || m.eng.Root() != dest {
		t.Fatalf("destination scan did not finish: screen=%v library=%+v", m.screen, m.library)
	}
	if m = press(m, "g"); m.screen != screenSources {
		t.Fatal("download sources not accessible from destination details")
	}
}

func TestLibraryCanVisitSettingsAndCancelFolderChangeWithoutLosingFocus(t *testing.T) {
	m := scrollModel(t)
	m.library = libraryState{complete: true, albums: true, all: []engine.LibraryTrack{
		{ID: "first", Album: "First", Path: "First/a.opus"}, {ID: "second", Album: "Second", Path: "Second/b.opus"},
	}}
	m = press(m, "1", "down")
	m = press(m, "O")
	if m.ws.section != secSettings {
		t.Fatal("settings inaccessible from albums")
	}
	m = press(m, "1")
	if m.ws.section != secAlbums {
		t.Fatal("could not return to albums from settings")
	}
	m = press(m, "down", "c")
	if m.screen != screenBrowse {
		t.Fatal("folder picker inaccessible from workspace")
	}
	m = press(m, "esc")
	if m.ws.section != secAlbums || m.ws.listCursor != 1 {
		t.Fatalf("cancel did not restore album focus: section=%v cursor=%d", m.ws.section, m.ws.listCursor)
	}
}

func TestLibraryKeepsAlbumFocusAfterSourcesAndArtist(t *testing.T) {
	m := scrollModel(t)
	m.library = libraryState{complete: true, albums: true, marked: map[string]bool{}, all: []engine.LibraryTrack{
		{ID: "first", Album: "First", Path: "First/a.opus"}, {ID: "second", Album: "Second", Artist: "Example Two", Path: "Second/b.opus"},
	}}
	m = press(m, "1", "F", "1")
	if m.ws.section != secAlbums {
		t.Fatal("could not return to albums from artist explorer")
	}
	m = press(m, "g", "l")
	if m.ws.section != secAlbums {
		t.Fatal("returning from Sources did not switch to Albums")
	}
}

func TestSourcesIsReachableWhileOpeningLibrary(t *testing.T) {
	m := scrollModel(t)
	scan := m.openInitialLibrary()
	m = press(m, "g")
	if m.screen != screenSources || m.ws.section != secSources {
		t.Fatal("automatic scan blocked access to Sources")
	}
	for range 4 {
		m = press(m, "down")
	}
	if n := finishTestLibraryScan(t, m, scan); n.screen != screenSources || n.ws.listCursor != 4 || n.library.scanning {
		t.Fatal("offscreen scan result changed source focus or left a stuck scan")
	}
}

func TestLibraryStillAcceptsPastedArtistLinks(t *testing.T) {
	m := scrollModel(t)
	scan := m.openInitialLibrary()
	link := "https://music.youtube.com/channel/UCabc"
	next, lookup := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(link), Paste: true})
	m = next.(model)
	if m.screen != screenArtist || lookup == nil {
		t.Fatalf("pasted artist link was not routed from Library: screen=%v", m.screen)
	}
	if n := finishTestLibraryScan(t, m, scan); n.screen != screenArtist || n.library.scanning {
		t.Fatal("cancelled local scan changed the explorer")
	}
}

func TestCompactTrashReviewShowsFilenameAndDetailsKey(t *testing.T) {
	m := sized(scrollModel(t), 30, 12)
	m.screen = screenLibrary
	row := engine.LibraryTrack{ID: "sample", Title: "Song", Path: strings.Repeat("very-long-folder/", 5) + "Song.opus"}
	m.library.candidates, m.library.confirm = []engine.LibraryTrack{row}, []string{row.ID}
	view := assertFits(t, m)
	if !strings.Contains(view, "Song.opus") || !strings.Contains(view, "i details") {
		t.Fatalf("review hides the filename or full-path shortcut:\n%s", view)
	}
}

func TestStaleCollectionInspectionCannotWriteToNewDestination(t *testing.T) {
	m := scrollModel(t)
	old := m.eng
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.screen = screenCollection
	next, _ := m.Update(inspectLoadedMsg{
		eng: old, from: screenCollection, url: "https://music.youtube.com/playlist?list=PLstale", save: true,
		listing: &ytdlp.Listing{ID: "stale", Title: "Stale result", Type: "album", Entries: []ytdlp.Entry{{ID: "track", Title: "Track"}}},
	})
	if len(next.(model).eng.Snapshot()) != 0 {
		t.Fatal("a result from the previous folder was saved to the new folder")
	}
}

func TestWideLibraryShowsAlbumInventoryAndSelectedDetails(t *testing.T) {
	m := sized(newModel(), 112, 30)
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.library = libraryState{complete: true, albums: true, all: []engine.LibraryTrack{
		{ID: "first", Album: "First Night", Artist: "Example One", Path: "First/a.opus"},
		{ID: "second", Album: "Second Night", Artist: "Example Two", Path: "Second/b.opus"},
		{ID: "third", Album: "Second Night", Artist: "Example Two", Path: "Another/c.opus"},
	}}
	m.refreshWorkspace()
	m = press(m, "down")
	view := assertFits(t, m)
	for _, label := range []string{"2 albums", "Selected album", "Example Two", "Folders"} {
		if !strings.Contains(view, label) {
			t.Fatalf("%q missing from album catalog:\n%s", label, view)
		}
	}
	if view = assertFits(t, sized(m, 34, 12)); !strings.Contains(view, "Second Night") {
		t.Fatalf("compact view lost the focused album:\n%s", view)
	}
}

func TestSourcesShowsSeparateDownloadQueueAndCatalog(t *testing.T) {
	m := sized(scrollModel(t), 100, 26)
	m = press(m, "3")
	m.run = run{verb: "downloaded", busy: true, total: 5, done: 2}
	view := assertFits(t, m)
	if !strings.Contains(view, "Sources") || !strings.Contains(view, "2/5 processed") || !strings.Contains(view, "A000") {
		t.Fatalf("queue and sources status missing:\n%s", view)
	}
}
