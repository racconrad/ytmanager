package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

func TestOpeningDestinationStartsAlbumLibraryScan(t *testing.T) {
	dest := t.TempDir()
	m := newModel()
	next, scan := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(dest), Paste: true})
	m = next.(model)
	if m.screen != screenLibrary || !m.library.albums || !m.library.scanning || scan == nil {
		t.Fatalf("expected initial Library scan: screen=%v scanning=%v albums=%v", m.screen, m.library.scanning, m.library.albums)
	}
	next, _ = m.Update(scan())
	m = next.(model)
	if m.screen != screenLibrary || !m.library.complete || m.eng.Root() != dest {
		t.Fatalf("destination scan did not finish: screen=%v library=%+v", m.screen, m.library)
	}
	if m = press(m, "g"); m.screen != screenSources {
		t.Fatal("download sources not accessible from the library")
	}
}

func TestLibraryCanVisitSettingsAndCancelFolderChangeWithoutLosingFocus(t *testing.T) {
	m := scrollModel(t)
	m.screen, m.cursor = screenLibrary, 1
	m.library = libraryState{complete: true, albums: true, all: []engine.LibraryTrack{
		{ID: "first", Path: "First/a.opus"}, {ID: "second", Path: "Second/b.opus"},
	}}
	m = press(m, "O")
	if m.screen != screenSettings {
		t.Fatal("settings inaccessible from library")
	}
	m = press(m, "esc")
	if m.screen != screenLibrary || m.cursor != 1 {
		t.Fatal("settings did not restore library focus")
	}
	m = press(m, "c")
	if m.screen != screenBrowse {
		t.Fatal("folder picker inaccessible from library")
	}
	m = press(m, "esc")
	if m.screen != screenLibrary || m.cursor != 1 {
		t.Fatal("cancel did not restore library focus")
	}
}

func TestLibraryKeepsAlbumFocusAfterSourcesAndArtist(t *testing.T) {
	m := scrollModel(t)
	m.screen, m.cursor = screenLibrary, 1
	m.library = libraryState{complete: true, albums: true, marked: map[string]bool{}, all: []engine.LibraryTrack{
		{ID: "first", Path: "First/a.opus"}, {ID: "second", Artist: "Example Two", Path: "Second/b.opus"},
	}}
	m = press(m, "F", "esc")
	if m.screen != screenLibrary || m.cursor != 1 {
		t.Fatal("artist explorer did not restore library focus")
	}
	m = press(m, "g", "l")
	if m.screen != screenLibrary || !m.library.albums || m.cursor != 1 || !m.library.scanning {
		t.Fatal("returning from Sources did not refresh the focused catalog")
	}
	m.library.cancel()
}

func TestSourcesIsReachableWhileOpeningLibrary(t *testing.T) {
	m := scrollModel(t)
	scan := m.openInitialLibrary()
	m = press(m, "g")
	if m.screen != screenSources {
		t.Fatal("automatic scan blocked access to Sources")
	}
	m.cursor = 4
	next, _ := m.Update(scan())
	if n := next.(model); n.screen != screenSources || n.cursor != 4 || n.library.scanning {
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
	next, _ = m.Update(scan())
	if n := next.(model); n.screen != screenArtist || n.library.scanning {
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
	m := sized(scrollModel(t), 112, 30)
	m.screen, m.cursor = screenLibrary, 1
	m.library = libraryState{complete: true, albums: true, all: []engine.LibraryTrack{
		{ID: "first", Album: "First Night", Artist: "Example One", Path: "First/a.opus"},
		{ID: "second", Album: "Second Night", Artist: "Example Two", Path: "Second/b.opus"},
		{ID: "third", Album: "Second Night", Artist: "Example Two", Path: "Another/c.opus"},
	}}
	view := assertFits(t, m)
	for _, label := range []string{"2 albums", "Selected album", "2 folders", "Example Two"} {
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
	m.run = run{verb: "downloaded", busy: true, total: 5, done: 2}
	view := assertFits(t, m)
	queue, catalog := strings.Index(view, "Download queue"), strings.Index(view, "Saved sources")
	if queue < 0 || catalog < 0 || queue > catalog || !strings.Contains(view, "2/5 processed") {
		t.Fatalf("queue and sources are not separated:\n%s", view)
	}
}
