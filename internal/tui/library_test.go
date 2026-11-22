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
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = next.(model)
	if cmd == nil {
		t.Fatal("library did not scan on entry")
	}
	next, _ = m.Update(cmd())
	return press(next.(model), "b")
}

func TestLibrarySearchCancelAndFilteredDeletion(t *testing.T) {
	m := libraryModel(t)
	m = press(m, "a", "/", "Song", "enter", "d")
	if len(m.library.confirm) != 2 {
		t.Fatalf("hidden marks included: %v", m.library.confirm)
	}
	m = press(m, "esc")
	if len(m.eng.Library()) != 3 {
		t.Fatal("cancel removed files")
	}
	m = press(m, "/", "esc")
	if m.library.query != "Song" {
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
	m = press(m, "f")
	if len(m.libraryRows()) != 2 {
		t.Fatal("duplicate filter")
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
				if state.library.confirm != nil && !strings.Contains(ansi.Strip(view), "esc cancel") {
					t.Fatal("missing cancellation", view)
				}
			}
		})
	}
}

func TestLibraryBusyEntryIsBlocked(t *testing.T) {
	m := libraryModel(t)
	m.screen = screenSources
	m.run.busy = true
	m = press(m, "l")
	if m.screen != screenSources {
		t.Fatal("opened during downloads")
	}
}

func TestLibraryConfirmationScrollsWithLogsAtMinimumSize(t *testing.T) {
	m := libraryModel(t)
	m.width, m.height = 30, 12
	for i := 0; i < 50; i++ {
		m.library.all = append(m.library.all, engine.LibraryTrack{ID: fmt.Sprint(i), Title: fmt.Sprintf("Track %02d", i), Path: fmt.Sprintf("%02d.opus", i)})
	}
	m.log("Earlier download message")
	logCount := len(m.logs)
	m = press(m, "a", "d")
	for _, key := range []string{"down", "end", "home"} {
		m = press(m, key)
		view := ansi.Strip(m.View())
		target := m.library.confirm[m.cursor]
		var row engine.LibraryTrack
		for _, r := range m.library.all {
			if r.ID == target {
				row = r
			}
		}
		for _, want := range []string{row.Title, row.Path, "u restores.", "Skipped by future syncs.", "esc cancel"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q at %s:\n%s", want, key, view)
			}
		}
	}
	if len(m.logs) != logCount {
		t.Fatal("confirmation discarded logs")
	}
}

func TestAlbumReviewProtectsSharedTracksAndCanIncludeThem(t *testing.T) {
	m := libraryModel(t)
	for i := range m.library.all {
		m.library.all[i].Album = "Album"
		m.library.all[i].Artist = "Artist"
		m.library.all[i].References = 2
	}
	m = press(m, "b", "d")
	if len(m.library.candidates) != 3 || len(m.library.confirm) != 0 {
		t.Fatalf("shared tracks not excluded: %+v", m.library)
	}
	m = press(m, "h")
	if len(m.library.confirm) != 3 {
		t.Fatal("cannot explicitly include shared tracks")
	}
	m = press(m, "esc", "enter")
	if m.library.album == "" || len(m.libraryRows()) != 3 {
		t.Fatal("album navigation failed")
	}
	m = press(m, "esc")
	if m.library.album != "" || m.screen != screenLibrary {
		t.Fatal("album back failed")
	}
}

func TestLibraryMutationKeepsExternalInventoryAndReportsPhysicalCounts(t *testing.T) {
	m := libraryModel(t)
	external := filepath.Join(m.eng.Root(), "external.opus")
	os.WriteFile(external, []byte("external"), 0600)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(model)
	next, _ = m.Update(cmd())
	m = next.(model)
	m = press(m, "/", "Other", "enter", "d")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.library.report == nil || m.library.report.Deleted != 1 {
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
	m = press(m, "esc", "/")
	m.input.SetValue("external")
	m = press(m, "enter", "o")
	if len(m.libraryRows()) != 1 {
		t.Fatal("cleanup filter missed unlisted external audio")
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
		states := []model{press(m, "?"), press(m, "t"), press(m, "i"), press(m, "u"), press(m, "X")}
		state := m
		state.library.report = &report
		states = append(states, state)
		state = m
		state.library.plan = &plan
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
	m.library.scanning = true // a slow phone rescan is in progress
	m = press(m, "down", " ")
	if m.cursor != 1 || len(m.library.marked) != 1 {
		t.Fatalf("rescan blocked browsing: cursor=%d marked=%v", m.cursor, m.library.marked)
	}
	if m = press(m, "d"); m.library.candidates != nil {
		t.Fatal("deletion review started before the scan finished")
	}
}
