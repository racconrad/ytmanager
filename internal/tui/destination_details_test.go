package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
)

func TestDestinationDetailsShowsSavedCatalogWhileDiskIsChecking(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Existing Music")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Format = "mp3"
	data.Merge("album", "album", "Existing Album", "", []manifest.Entry{
		{ID: "saved", Title: "Saved"}, {ID: "failed", Title: "Failed"},
	})
	data.Merge("playlist", "playlist", "Existing Playlist", "", []manifest.Entry{
		{ID: "saved", Title: "Saved"}, {ID: "pending", Title: "Pending"},
	})
	data.Tracks["saved"].Path, data.Tracks["saved"].Status = "unverified.opus", manifest.StatusDone
	data.Tracks["failed"].Status = manifest.StatusFailed
	data.Queue = []manifest.SourceRequest{{URL: "https://music.youtube.com/playlist?list=example"}}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	m.width, m.height = 112, 30
	m.openBrowser(root)
	next, scan := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = next.(model)
	m.spaceKnown, m.space = true, device.SpaceInfo{Available: 25 << 30, Total: 100 << 30}
	if m.eng == nil || m.eng.Root() != root || !m.library.scanning || m.library.complete || scan == nil {
		t.Fatal("choosing a destination did not start a background scan")
	}
	view := assertFits(t, m)
	for _, want := range []string{
		"Destination details", "Existing Music", "MP3", "1 album", "1 playlist", "1 saved", "1 pending",
		"1 queued", "1 failed", "Existing Album", "Existing Playlist", "Checking disk", "75% used", "25.0 GB free",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q before disk check:\n%s", want, view)
		}
	}
	m = finishTestLibraryScan(t, m, scan)
	if view := assertFits(t, m); !strings.Contains(view, "Destination details") || !strings.Contains(view, "Disk checked") {
		t.Fatalf("scan did not update details in place:\n%s", view)
	}
}

func TestDestinationDetailsKeepsDirectShortcutsAtMinimumSize(t *testing.T) {
	m := sized(newModel(), 30, 12)
	cmd := m.use(t.TempDir())
	if cmd == nil {
		t.Fatal("missing initial scan")
	}
	view := assertFits(t, m)
	for _, want := range []string{"Destination details", "No saved catalog", "1 albums", "3 sources", "4 review"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact details hides %q:\n%s", want, view)
		}
	}
	m = press(m, "down", "down", "down", "down", "down")
	if view := assertFits(t, m); !strings.Contains(view, "0 failed") {
		t.Fatalf("compact details cannot reach the last rows:\n%s", view)
	}
	for _, pair := range [][2]string{{"1", "Albums"}, {"3", "Sources"}, {"4", "Review problems"}} {
		m = press(m, pair[0])
		if !strings.Contains(assertFits(t, m), pair[1]) {
			t.Fatalf("shortcut %s did not open %s", pair[0], pair[1])
		}
		m = press(m, "0")
		if !strings.Contains(assertFits(t, m), "Destination details") {
			t.Fatal("0 did not return to details")
		}
	}
}

func TestProblemReviewIsNotAMainTab(t *testing.T) {
	m := sized(scrollModel(t), 112, 30)
	if tabs := m.renderTopTabs(112); strings.Contains(tabs, "Issues") || strings.Contains(tabs, "Review") {
		t.Fatalf("problem review is still in the main tabs: %s", tabs)
	}
	if m = press(m, "3", "]"); m.ws.section != secArtists {
		t.Fatalf("Sources must cycle directly to Artists, got %v", m.ws.section)
	}
	m = press(m, "4")
	checkScreenContains(t, m, "Review problems", "esc details")
	if m = press(m, "esc"); m.ws.section != secDetails {
		t.Fatalf("Esc must return to Details, got %v", m.ws.section)
	}
}
