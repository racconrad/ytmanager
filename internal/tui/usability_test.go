package tui

import (
	"context"
	"strings"
	"testing"

	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

func TestCompactSourcesShowsPrimaryActionsAndShortcutGuide(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height = 44, 14
	m.cursor = 2
	view := assertFits(t, m)
	for _, label := range []string{"add link", "library", "keys"} {
		if !strings.Contains(view, label) {
			t.Fatalf("missing %q in compact Sources:\n%s", label, view)
		}
	}
	m = press(m, "?")
	if view = assertFits(t, m); !strings.Contains(view, "Source shortcuts") || !strings.Contains(view, "close") {
		t.Fatalf("shortcut guide not visible:\n%s", view)
	}
	m = press(m, "down", "esc")
	if m.screen != screenSources || m.cursor != 2 || strings.Contains(assertFits(t, m), "Source shortcuts") {
		t.Fatal("closing help lost the selected source")
	}
}

func TestLibraryShowsLocalActionsAndRefreshKeepsSelection(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height = 44, 14
	m.screen = screenLibrary
	a := engine.LibraryTrack{ID: "a", Title: "Apple", Album: "First", Path: "First/a.opus"}
	b := engine.LibraryTrack{ID: "b", Title: "Banana", Album: "First", Path: "First/b.opus"}
	c := engine.LibraryTrack{ID: "c", Title: "Carrot", Album: "Second", Path: "Second/c.opus"}
	m.library = libraryState{all: []engine.LibraryTrack{a, b}, complete: true, marked: map[string]bool{"b": true}}
	m.cursor = 1
	for _, label := range []string{"scan folder", "trash", "restore", "albums"} {
		if view := assertFits(t, m); !strings.Contains(view, label) {
			t.Fatalf("missing %q in compact Library:\n%s", label, view)
		}
	}

	m = press(m, "s")
	n, _ := m.Update(libraryScannedMsg{eng: m.eng, tracks: []engine.LibraryTrack{c, a, b}})
	m = n.(model)
	if m.cursor != 2 || !m.library.marked["b"] {
		t.Fatalf("rescan lost the selected track or its mark: cursor=%d, marked=%v", m.cursor, m.library.marked)
	}
	if view := assertFits(t, m); !strings.Contains(view, "+1 file") || !strings.Contains(view, "+1 album") {
		t.Fatalf("new music was not reported:\n%s", view)
	}
}

func TestCancelledLibraryRefreshKeepsLastInventory(t *testing.T) {
	m := scrollModel(t)
	m.screen = screenLibrary
	old := engine.LibraryTrack{ID: "a", Title: "Apple", Path: "a.opus"}
	m.library = libraryState{all: []engine.LibraryTrack{old}, complete: true}
	m = press(m, "s")
	n, _ := m.Update(libraryScannedMsg{eng: m.eng, err: context.Canceled})
	m = n.(model)
	view := assertFits(t, m)
	if !strings.Contains(view, "Apple") || !strings.Contains(view, "Scan cancelled") ||
		strings.Contains(view, "Scanned 0") || m.library.complete {
		t.Fatalf("cancelled scan misrepresents the library:\n%s", view)
	}
}

func TestIncompleteFirstScanDoesNotClaimFolderIsEmpty(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height = 30, 12
	m.screen = screenLibrary
	m = press(m, "s")
	n, _ := m.Update(libraryScannedMsg{eng: m.eng, err: context.Canceled})
	view := assertFits(t, n.(model))
	if !strings.Contains(view, "Scan cancelled") || strings.Contains(view, "No music in this folder") {
		t.Fatalf("incomplete inventory presented as empty:\n%s", view)
	}
}

func TestRescanDropsUnverifiedMarksAndClosesAnAlbumThatVanished(t *testing.T) {
	m := scrollModel(t)
	m.screen = screenLibrary
	a := engine.LibraryTrack{ID: "a", Path: "a.opus", Title: "Apple", Album: "First", Hash: "old"}
	b := engine.LibraryTrack{ID: "b", Path: "b.opus", Title: "Banana", Album: "Second", Hash: "same"}
	c := engine.LibraryTrack{ID: "c", Path: "c.opus", Title: "Carrot", Album: "Third"}
	m.library = libraryState{all: []engine.LibraryTrack{a, b, c}, complete: true, albums: true, album: c.AlbumKey(), marked: map[string]bool{"a": true, "b": true}}
	a.Hash, a.Status, b.Hash = "", "not scanned", ""
	m = press(m, "s")
	n, _ := m.Update(libraryScannedMsg{eng: m.eng, tracks: []engine.LibraryTrack{a, b}})
	m = n.(model)
	if m.library.album != "" || m.library.marked["a"] || m.library.marked["b"] {
		t.Fatalf("stale album or marks after rescan: album=%q marks=%v", m.library.album, m.library.marked)
	}
}

func TestLibraryGuidePreservesSelectedTrack(t *testing.T) {
	m := scrollModel(t)
	m.screen, m.cursor = screenLibrary, 1
	m.library.all = []engine.LibraryTrack{{ID: "a"}, {ID: "b"}}
	m = press(m, "?", "down", "esc")
	if m.cursor != 1 || m.library.help {
		t.Fatalf("help changed the active track: cursor=%d", m.cursor)
	}
}

func TestCompactContextHelpKeepsAnEntryPoint(t *testing.T) {
	base := scrollModel(t)
	base.width, base.height = 30, 12
	for _, target := range []screen{screenBrowse, screenSettings, screenCollection, screenArtist, screenMissing, screenDevices} {
		m := base
		m.screen = target
		m.collection.preview = &ytdlp.Listing{Title: "An album"}
		if target == screenSettings && !strings.Contains(assertFits(t, m), "Format: opus") {
			t.Fatal("compact settings hide the current value")
		}
		if view := assertFits(t, m); !strings.Contains(view, "? keys") {
			t.Fatalf("screen %d: help disappeared:\n%s", target, view)
		}
		m = press(m, "?")
		if !strings.Contains(assertFits(t, m), "shortcuts") {
			t.Fatalf("screen %d: guide did not open", target)
		}
		m = press(m, "esc")
		if m.screen != target || m.shortcutHelp {
			t.Fatalf("screen %d: guide closed to the wrong screen", target)
		}
	}
}

func TestLibraryGuideFromSettingsDoesNotWaitForDisk(t *testing.T) {
	m := scrollModel(t)
	m.screen, m.cursor = screenSettings, len(settings)-1
	m = press(m, "enter")
	if !m.library.help || m.library.scanning {
		t.Fatal("help depends on a filesystem scan")
	}
	m = press(m, "down")
	if m.cursor != 1 {
		t.Fatal("guide cannot scroll")
	}
	m = press(m, "esc")
	if m.screen != screenSettings || m.cursor != len(settings)-1 {
		t.Fatal("closing guide should return to the selected setting")
	}
}

func TestDownloadStatusDoesNotShowCompletedWithoutATotal(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height = 44, 14
	m.run.busy, m.run.verb = true, "downloaded"
	if view := assertFits(t, m); !strings.Contains(view, "Preparing downloads") || strings.Contains(view, "0/0 · 0 active") {
		t.Fatalf("unclear preparation status:\n%s", view)
	}
	m.run.total, m.run.done, m.run.failed = 4, 1, 1
	if view := assertFits(t, m); !strings.Contains(view, "2/4 processed") {
		t.Fatalf("progress is not visible:\n%s", view)
	}
}

func TestCompactLibraryReportsShowTheActualResult(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height = 44, 14
	m.screen = screenLibrary
	m.library.report = &engine.FileReport{Restored: 1, Bytes: 30000}
	view := assertFits(t, m)
	if !strings.Contains(view, "1 restored") || strings.Contains(view, "0 moved to trash") {
		t.Fatalf("zero totals hide the restore result:\n%s", view)
	}
}
