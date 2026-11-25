package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func libraryModel(t *testing.T) model {
	t.Helper()
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "album", "Album", "url", []manifest.Entry{{ID: "a", Title: "Song"}, {ID: "b", Title: "Song"}, {ID: "c", Title: "Other"}})
	for id, tr := range data.Tracks {
		tr.Path = id + ".opus"
		tr.Status = manifest.StatusDone
		if err := os.WriteFile(filepath.Join(root, tr.Path), []byte("audio-"+id), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(model)
	if cmd == nil {
		t.Fatal("library did not scan on entry")
	}
	return finishTestLibraryScan(t, m, cmd)
}

func TestLibrarySearchCancelAndFilteredDeletion(t *testing.T) {
	m := press(libraryModel(t), "enter")
	m = press(m, "a", "/", "Song", "enter", "d")
	if len(m.ws.deleteReview) != 2 {
		t.Fatalf("hidden marks included: %+v", m.ws.deleteReview)
	}
	m = press(m, "esc")
	if len(m.eng.Library()) != 3 {
		t.Fatal("cancel removed files")
	}
	m = press(m, "/", "esc")
	if m.ws.query != "Song" {
		t.Fatal("cancel lost prior search")
	}
	m = press(m, "d")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.library.busy || cmd == nil {
		t.Fatal("no async deletion")
	}
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.library.busy || len(m.eng.Library()) != 1 {
		t.Fatal("delete did not complete")
	}
	if m.eng.Library()[0].Title != "Other" {
		t.Fatal("deleted hidden selection")
	}
}

func TestLibraryDuplicateFilterAndBounds(t *testing.T) {
	m := libraryModel(t)
	// Write identical bytes to a.opus and b.opus so scan flags genuine byte-identical duplicates.
	for _, name := range []string{"a.opus", "b.opus"} {
		if err := os.WriteFile(filepath.Join(m.eng.Root(), name), []byte("identical-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(model)
	m = press(finishTestLibraryScan(t, m, cmd), "4", "/", "SHA-256", "enter")
	if len(m.filteredIssues()) != 2 {
		t.Fatalf("expected 2 duplicate issues, got %d", len(m.filteredIssues()))
	}
	for _, size := range [][2]int{{30, 12}, {48, 18}, {80, 24}, {120, 40}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m.width, m.height = size[0], size[1]
			for _, state := range []model{m, press(m, "/"), press(m, "d")} {
				view := state.View()
				lines := strings.Split(view, "\n")
				if len(lines) > m.height {
					t.Fatal("too tall")
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > m.width {
						t.Fatal("too wide")
					}
				}
				if state.ws.deleteReview != nil && !strings.Contains(ansi.Strip(view), "esc cancel") {
					t.Fatal("missing cancellation", view)
				}
			}
		})
	}
}

func TestLibraryBusyEntryIsBlocked(t *testing.T) {
	m := libraryModel(t)
	m.run.busy = true
	m = press(m, "d")
	if m.ws.deleteReview != nil {
		t.Fatal("opened deletion confirm during downloads")
	}
}

func TestLibraryConfirmationScrollsWithLogsAtMinimumSize(t *testing.T) {
	m := libraryModel(t)
	m.width, m.height = 30, 12
	for i := 0; i < 50; i++ {
		m.library.all = append(m.library.all, engine.LibraryTrack{ID: fmt.Sprint(i), Title: fmt.Sprintf("Track %02d", i), Path: fmt.Sprintf("%02d.opus", i)})
	}
	m.refreshWorkspace()
	m = press(m, "2", "enter")
	m.log("Earlier download message")
	logCount := len(m.logs)
	m = press(m, "a", "d")
	for _, key := range []string{"down", "end", "home"} {
		m = press(m, key)
		view := ansi.Strip(m.View())
		row := m.ws.deleteReview[m.ws.deleteCursor]
		for _, want := range []string{row.Title, row.Path, "Cannot be restored", "All managed lists", "esc cancel"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q at %s:\n%s", want, key, view)
			}
		}
	}
	if len(m.logs) != logCount {
		t.Fatal("confirmation discarded logs")
	}
}

func TestAlbumPermanentReviewShowsAllSharedTracksBeforeConfirmation(t *testing.T) {
	m := libraryModel(t)
	for i := range m.library.all {
		m.library.all[i].Album = "Album"
		m.library.all[i].Artist = "Artist"
		m.library.all[i].References = 2
	}
	m.refreshWorkspace()
	m = press(m, "d")
	if len(m.ws.deleteReview) != 3 || !strings.Contains(ansi.Strip(m.View()), "All managed lists") {
		t.Fatalf("review does not show the scope of shared deletion: %+v", m.ws.deleteReview)
	}
	m = press(m, "esc", "enter")
	if m.ws.albumKey == "" || len(m.filteredAlbumTracks(m.ws.albums[0])) != 3 {
		t.Fatal("album navigation failed")
	}
	m = press(m, "esc")
	if m.ws.albumKey != "" || m.ws.section != secAlbums {
		t.Fatal("album back failed")
	}
}

func TestLibraryMutationKeepsExternalInventoryAndReportsPhysicalCounts(t *testing.T) {
	m := libraryModel(t)
	external := filepath.Join(m.eng.Root(), "external.opus")
	os.WriteFile(external, []byte("external"), 0600)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(model)
	m = finishTestLibraryScan(t, m, cmd)
	m = press(m, "2", "enter", "/", "Other", "enter", "d")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(cmd())
	m = next.(model)
	if !strings.Contains(strings.Join(m.logs, "\n"), "1 permanently deleted") {
		t.Fatal("no physical deletion report")
	}
	found := false
	for _, r := range m.library.all {
		if r.Path == "external.opus" {
			found = true
		}
	}
	if !found {
		t.Fatal("external files disappeared after managed deletion")
	}
	m = press(m, "esc", "4", "/", "external", "enter")
	if len(m.filteredIssues()) != 1 {
		t.Fatalf("issues filter missed unlisted external audio: %+v", m.filteredIssues())
	}
	m = press(m, "d", "esc")
	if _, err := os.Stat(external); err != nil {
		t.Fatal("cancel deleted external audio")
	}
}

func TestLibraryToolsAndReportsFitSmallTerminals(t *testing.T) {
	m := libraryModel(t)
	for _, size := range [][2]int{{30, 12}, {48, 18}, {80, 24}} {
		m.width, m.height = size[0], size[1]
		report := engine.FileReport{Items: []engine.FileResult{{Path: strings.Repeat("very/long/", 15), Outcome: "failed", Error: "disk full"}}}
		plan := engine.LibraryPlan{Changes: []engine.LibraryChange{{Track: m.library.all[0], To: strings.Repeat("new/folder/", 10)}}}
		restoreState := m
		restoreState.screen, restoreState.library.restore = screenLibrary, true
		purgeState := m
		purgeState.screen, purgeState.library.purge = screenLibrary, true
		toolsState := press(m, "right", "down", "down", "enter")
		states := []model{press(m, "?"), toolsState, restoreState, purgeState}
		state := m
		state.screen, state.library.report = screenLibrary, &report
		states = append(states, state)
		state = m
		state.screen, state.library.plan = screenLibrary, &plan
		states = append(states, state)
		for _, state := range states {
			view := state.View()
			lines := strings.Split(view, "\n")
			if len(lines) > m.height {
				t.Fatal("view too tall")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("view too wide")
				}
			}
			if !strings.Contains(ansi.Strip(view), "esc") {
				t.Fatalf("missing exit at %v: %s", size, view)
			}
		}
	}
}

func TestLibraryMarksAndBrowsesDuringRescan(t *testing.T) {
	m := libraryModel(t)
	m.library.all = append(m.library.all, engine.LibraryTrack{ID: "d", Album: "Album 2", Title: "Song 2", Path: "Album 2/d.opus"})
	m.refreshWorkspace()
	m.library.scanning = true // a slow phone rescan is in progress
	m = press(m, "down", " ")
	if m.ws.listCursor != 1 || len(m.ws.markedAlbums) != 1 {
		t.Fatalf("rescan blocked browsing: cursor=%d marked=%v", m.ws.listCursor, m.ws.markedAlbums)
	}
	if m = press(m, "d"); m.ws.deleteReview != nil {
		t.Fatal("deletion review started before the scan finished")
	}
}

func TestOpeningFolderAnnouncesSavedQueue(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Queue = []manifest.SourceRequest{{URL: "https://music.youtube.com/playlist?list=OLAK5uy_a"}, {URL: "https://music.youtube.com/playlist?list=OLAK5uy_b"}}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	cmd := m.use(root)
	next, _ := m.Update(cmd())
	m = next.(model)
	if len(m.logs) == 0 || !strings.Contains(m.logs[len(m.logs)-1], "2 sources waiting") {
		t.Fatalf("saved queue was not announced: %v", m.logs)
	}
}
