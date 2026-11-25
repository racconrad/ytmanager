package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytmanager/internal/manifest"
)

func TestWorkspaceSidebarFoldersSourcesIssuesAndSidePanel(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	syncedRel := filepath.Join("Daft Punk", "Discovery", "01 - One More Time.opus")
	unlistedRel := filepath.Join("Extras", "bonus.opus")
	corruptRel := filepath.Join("Daft Punk", "Discovery", "broken.opus")
	for _, rel := range []string{syncedRel, unlistedRel, corruptRel} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("not-real-opus-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := data.Merge("album", "OLAK_ws", "Discovery", "https://music.youtube.com/playlist?list=OLAK_ws", []manifest.Entry{
		{ID: "tr1", Title: "One More Time"},
		{ID: "tr2", Title: "Aerodynamic"},
		{ID: "tr3", Title: "Digital Love"},
	})
	s.Creator = "Daft Punk"
	data.Tracks["tr1"].Status = manifest.StatusDone
	data.Tracks["tr1"].Path = syncedRel
	data.Tracks["tr2"].Status = manifest.StatusFailed
	data.Tracks["tr2"].Error = "403 Forbidden"
	data.Tracks["tr3"].Status = manifest.StatusDone
	data.Tracks["tr3"].Path = filepath.Join("Daft Punk", "Discovery", "03 - Digital Love.opus")
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 104, 30
	cmd := m.scanLibrary()
	m = finishTestLibraryScan(t, m, cmd)

	wide := assertFits(t, m)
	for _, want := range []string{"Albums", "Folders", "Sources", "Issues", "Artists", "Settings", "Selected album", "Actions", "Discovery"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide workspace missing %q:\n%s", want, wide)
		}
	}
	// Extra physical file inside Daft Punk/Discovery folds into Discovery instead of creating a duplicate album.
	if len(m.ws.albums) != 2 {
		t.Fatalf("expected 2 unified albums (Discovery + Extras), got %d: %+v", len(m.ws.albums), m.ws.albums)
	}
	// Top Menu navigation (← focuses Top Menu, → moves to Folders, ↓ enters list).
	m = press(m, "left", "right", "down")
	if m.ws.section != secFolders || m.ws.focus != paneList {
		t.Fatalf("top menu navigation failed: section=%v focus=%v", m.ws.section, m.ws.focus)
	}
	// Tab toggles directly between the 2 columns (List <-> Actions).
	m = press(m, "tab")
	if m.ws.focus != paneSide {
		t.Fatalf("tab did not focus right Actions column: focus=%v", m.ws.focus)
	}
	m = press(m, "tab")
	if m.ws.focus != paneList {
		t.Fatalf("second tab did not return to left List column: focus=%v", m.ws.focus)
	}

	// Switch to Physical Folders via menu (2) and drill into Extras to find bonus.opus
	m = press(m, "2")
	flds := assertFits(t, m)
	if !strings.Contains(flds, "Extras") || !strings.Contains(flds, "unlisted") {
		t.Fatalf("folders view missing Extras / unlisted badge:\n%s", flds)
	}
	m = press(m, "down", "enter")
	drill := assertFits(t, m)
	if !strings.Contains(drill, "bonus.opus") || !strings.Contains(drill, "Unlisted on sources") {
		t.Fatalf("drilled folder missing physical file details:\n%s", drill)
	}

	// The permanent-delete review contains only the selected physical file.
	m = press(m, "right", "down", "down", "down", "enter")
	if len(m.ws.deleteReview) != 1 || m.ws.deleteReview[0].Path != unlistedRel {
		t.Fatalf("expected permanent delete review for %s, got %+v", unlistedRel, m.ws.deleteReview)
	}
	m = press(m, "esc")

	// Issues section unifies failed downloads, missing files, corrupt audio, and unlisted files
	m = press(m, "4")
	issView := assertFits(t, m)
	for _, want := range []string{"Download error", "Missing on disk", "Corrupt", "Unlisted", "403 Forbidden"} {
		if !strings.Contains(issView, want) {
			t.Fatalf("issues view missing %q:\n%s", want, issView)
		}
	}
	m = press(m, "/", "bonus", "enter")
	if len(m.filteredIssues()) != 1 || m.filteredIssues()[0].Path != unlistedRel {
		t.Fatalf("filtered issues = %+v, want %s", m.filteredIssues(), unlistedRel)
	}

	// Compact terminal (30x12) switches cleanly between list, menu, and side actions
	m = sized(m, 30, 12)
	_ = assertFits(t, m)
	m = press(m, "left")
	if menu := assertFits(t, m); !strings.Contains(menu, "Menu") || m.ws.focus != paneMenu {
		t.Fatalf("compact menu focus failed:\n%s", menu)
	}
	m = press(m, "right", "right")
	if side := assertFits(t, m); !strings.Contains(side, "Actions") || m.ws.focus != paneSide {
		t.Fatalf("compact side focus failed:\n%s", side)
	}
}
