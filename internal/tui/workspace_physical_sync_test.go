package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func TestWorkspacePhysicalSyncPreviewToggleAndApply(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "alb1", "Discovery", "https://music.youtube.com/playlist?list=alb1", []manifest.Entry{
		{ID: "t1", Title: "Old Title", Artist: "Old Artist"},
		{ID: "t2", Title: "Gone Track"},
		{ID: "t3", Title: "Other Folder"},
	})
	data.Tracks["t1"].Status, data.Tracks["t1"].Path = manifest.StatusDone, filepath.Join("Daft Punk", "Discovery", "01.opus")
	data.Tracks["t2"].Status, data.Tracks["t2"].Path = manifest.StatusDone, filepath.Join("Daft Punk", "Discovery", "02.opus")
	data.Tracks["t3"].Status, data.Tracks["t3"].Path = manifest.StatusDone, filepath.Join("Other", "03.opus")
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 112, 30
	m.library.all = []engine.LibraryTrack{
		{ID: "t1", Title: "New Title", Artist: "Daft Punk", Path: filepath.Join("Daft Punk", "Discovery", "01.opus"), Status: "present"},
		{ID: "t2", Title: "Gone Track", Path: filepath.Join("Daft Punk", "Discovery", "02.opus"), Missing: true, Status: "missing"},
		{ID: "t3", Title: "Changed Elsewhere", Path: filepath.Join("Other", "03.opus"), Status: "present"},
	}
	m.library.complete = true
	m.refreshWorkspace()
	m.ws.section = secFolders
	m.ws.listCursor = 0
	if !strings.Contains(m.workspaceHelp(), "P sync selected folder to sources") {
		t.Fatal("selected-folder sync shortcut is not discoverable")
	}

	// Press P to open physical sync preview.
	m = press(m, "P")
	if m.ws.syncPlan == nil {
		t.Fatal("P did not open physical sync preview")
	}
	if m.ws.syncPlan.opts.AdoptDelete {
		t.Fatal("physical deletions were enabled without explicit confirmation")
	}
	if m.ws.syncPlan.plan.Count(engine.ChangeMetadata) != 1 {
		t.Fatalf("sync included changes outside the selected folder: %+v", m.ws.syncPlan.plan.Changes)
	}
	wide := assertFits(t, m)
	for _, want := range []string{"Apply disk changes (1)", "Tags (1)", "Deleted (1)", "New Title"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("preview missing %q:\n%s", want, wide)
		}
	}
	// Compact 30x12 must also fit cleanly.
	_ = assertFits(t, sized(m, 30, 12))
	m = sized(m, 112, 30)

	// Toggle 4 (Deleted) ON -> now 2 changes enabled.
	m = press(m, "4")
	if got := assertFits(t, m); !strings.Contains(got, "Apply disk changes (2)") {
		t.Fatalf("toggling category 4 did not enable deletion adoption:\n%s", got)
	}

	// Apply with Enter.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not start ApplyPhysicalSync")
	}
	next, _ = next.(model).Update(cmd())
	m = next.(model)
	if m.ws.syncPlan != nil {
		t.Fatal("sync modal stayed open after apply")
	}
	reloaded, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Tracks["t1"].Title != "New Title" {
		t.Fatalf("tag edit not applied: %+v", reloaded.Tracks["t1"])
	}
	if !reloaded.Tracks["t2"].PermanentlyRemoved {
		t.Fatalf("physical deletion not adopted: %+v", reloaded.Tracks["t2"])
	}
}

func TestPhysicalSyncRequiresSelectedFolder(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "alb1", "Discovery", "https://music.youtube.com/playlist?list=alb1", []manifest.Entry{
		{ID: "t1", Title: "Old Title"},
	})
	data.Tracks["t1"].Status, data.Tracks["t1"].Path = "done", filepath.Join("Artist", "Discovery", "01.opus")
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.library.all = []engine.LibraryTrack{{ID: "t1", Title: "New Title", Path: filepath.Join("Artist", "Discovery", "01.opus"), Status: "present"}}
	m.library.complete = true
	m.refreshWorkspace()
	m.ws.section = secDetails
	m.openPhysicalSync()
	if m.ws.syncPlan != nil {
		t.Fatal("physical sync opened without an active folder selection")
	}
}
