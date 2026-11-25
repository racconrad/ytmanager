package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
)

func TestLibraryDiscoveryIsVisibleWithoutReplacingVerifiedInventory(t *testing.T) {
	m := newModel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "New Music.opus"), []byte("audio fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.screen, m.width, m.height = screenLibrary, 96, 28
	m.library.all = []engine.LibraryTrack{{ID: "old", Title: "Old file", Path: "Old file.opus"}}
	m.library.complete = true
	scan := m.scanLibrary()
	t.Cleanup(m.library.cancel)

	msg := scan()
	next, wait := m.Update(msg)
	m = next.(model)
	m = press(m, "2", "enter")
	if !m.library.scanning || m.library.complete || len(m.library.all) != 1 || m.library.all[0].ID != "old" || wait == nil {
		t.Fatalf("discovery replaced the verified inventory or ended the scan: %+v", m.library)
	}
	if view := assertFits(t, m); !strings.Contains(view, "New Music") || !strings.Contains(view, "Old file") {
		t.Fatalf("files not browsable during verification:\n%s", view)
	}
	m = finishTestLibraryScan(t, m, wait)
	if !m.library.complete || len(m.library.all) != 1 || m.library.all[0].Title != "New Music" || m.library.all[0].Hash == "" {
		t.Fatalf("successful scan did not replace discovery and old inventory: %+v", m.library)
	}
}

func TestLibraryScanIgnoresCancelledAndSupersededMessages(t *testing.T) {
	m := newModel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "preview.opus"), []byte("audio fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.screen = screenLibrary
	scan := m.scanLibrary()
	t.Cleanup(m.library.cancel)
	first := scan()
	next, wait := m.Update(first)
	m = next.(model)
	if wait == nil || len(m.libraryDisplayRows()) != 1 {
		t.Fatal("initial discovery did not reach the UI")
	}
	final := wait()
	m = press(m, "x")
	next, _ = m.Update(final)
	m = next.(model)
	if m.library.complete || m.library.scanning || len(m.libraryDisplayRows()) != 0 || m.library.scanNotice != "Scan cancelled" {
		t.Fatalf("cancelled scan published a preview or verification: %+v", m.library)
	}

	active := m.scanLibrary()
	t.Cleanup(m.library.cancel)
	for _, stale := range []tea.Msg{first, final} {
		next, cmd := m.Update(stale)
		m = next.(model)
		if cmd != nil || !m.library.scanning || len(m.libraryDisplayRows()) != 0 {
			t.Fatalf("stale message %T changed a new scan of the same folder", stale)
		}
	}
	m = finishTestLibraryScan(t, m, active)
	if !m.library.complete {
		t.Fatal("current scan did not complete")
	}

	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.screen = screenLibrary
	active = m.scanLibrary()
	t.Cleanup(m.library.cancel)
	for _, stale := range []tea.Msg{first, final} {
		next, cmd := m.Update(stale)
		m = next.(model)
		if cmd != nil || !m.library.scanning || len(m.libraryDisplayRows()) != 0 {
			t.Fatalf("message from previous destination %T changed the active scan", stale)
		}
	}
	m = finishTestLibraryScan(t, m, active)
	if !m.library.complete || len(m.library.all) != 0 {
		t.Fatal("new destination inherited the previous inventory")
	}
}

func TestLibraryDiscoveryKeepsKeyboardFocusWhenRowsReorder(t *testing.T) {
	for _, tt := range []struct {
		name    string
		section navSection
		folder  string
		path    string
	}{
		{"albums", secAlbums, "", "Alpha/New.opus"},
		{"folders", secFolders, "", "Alpha/New.opus"},
		{"folder files", secFolders, "Zulu", "Zulu/New.opus"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			if err := m.openDest(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			m.screen = screenLibrary
			m.ws.section, m.ws.folderDir = tt.section, tt.folder
			m.library.all = []engine.LibraryTrack{{ID: "old", Title: "Old", Path: "Zulu/Old.opus"}}
			m.library.complete = true
			m.refreshWorkspace()
			m.scanLibrary()
			t.Cleanup(m.library.cancel)
			next, _ := m.Update(libraryDiscoveredMsg{m.library.scan, []engine.LibraryTrack{{
				ID: "new", Title: "New", Path: tt.path, Status: "not scanned",
			}}})
			if m = next.(model); m.ws.listCursor != 1 {
				t.Fatalf("new row stole focus in %s: cursor=%d", tt.name, m.ws.listCursor)
			}
		})
	}
}

func TestLibraryDiscoveryKeepsIssueFocusWhenAnIssueMoves(t *testing.T) {
	m := newModel()
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.screen, m.ws.section = screenMissing, secIssues
	m.library.complete = true
	m.library.all = []engine.LibraryTrack{
		{ID: "a", Path: "a.opus", Title: "Alpha", Invalid: true, Size: 10},
		{ID: "b", Path: "b.opus", Title: "Beta", Invalid: true},
		{ID: "c", Path: "c.opus", Title: "Charlie", Invalid: true},
	}
	m.refreshWorkspace()
	selected := m.ws.issues[1].TrackID
	m.ws.listCursor = 1
	m.scanLibrary()
	t.Cleanup(m.library.cancel)
	next, _ := m.Update(libraryDiscoveredMsg{m.library.scan, []engine.LibraryTrack{{
		ID: "a", Path: "a.opus", Title: "Alpha", Size: 20, Status: "not scanned",
	}}})
	m = next.(model)
	if len(m.ws.issues) != 2 || m.ws.issues[m.ws.listCursor].TrackID != selected {
		t.Fatalf("issue focus lost after discovery: %+v", m.ws.issues)
	}
}

func finishTestLibraryScan(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	for cmd != nil && m.library.scanning {
		next, follow := m.Update(cmd())
		m, cmd = next.(model), follow
	}
	if m.library.scanning {
		t.Fatal("scan still active without a follow-up command")
	}
	return m
}
