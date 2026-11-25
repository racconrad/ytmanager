package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
)

func TestStartupShowsRecentSubfolderAndOpensSavedDetails(t *testing.T) {
	root := t.TempDir()
	saved := filepath.Join(root, "My music")
	if err := os.Mkdir(saved, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, err := manifest.Load(saved)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Merge("album", "album", "Remembered album", "", nil)
	if err := catalog.Save(); err != nil {
		t.Fatal(err)
	}

	history := filepath.Join(t.TempDir(), "recent.json")
	m := sized(newModel(), 96, 24)
	m.historyPath = history
	if err := m.openDest(saved); err != nil {
		t.Fatal(err)
	}

	m = sized(newModel(), 96, 24)
	m.startup = true
	m.historyPath = history
	checkScreenContains(t, m, "ytmanager", "Finding destinations", `/\_/\`, "(o)---(o)")
	before := m.View()
	m.cat.frame = 2
	checkScreenContains(t, m, "(O)---(O)")
	if m.View() == before {
		t.Fatal("loading scene did not animate")
	}
	checkScreenContains(t, sized(m, 30, 12), "Finding destinations")
	if m = press(m, "enter"); m.prompt != promptNone {
		t.Fatal("loading must not open an empty path prompt")
	}

	paths := readDestinationHistory(history)
	if len(paths) != 1 || paths[0].Path != saved {
		t.Fatalf("recent path not persisted: %v", paths)
	}
	list := []device.Mount{{Path: root}}
	next, _ := m.Update(mountsMsg{list: list, history: paths, recent: availableRecent(paths, list, nil)})
	m = next.(model)
	checkScreenContains(t, m, "Choose a destination", "Recent folder", "My music", "Other path")
	next, open := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if open == nil || m.eng != nil {
		t.Fatal("opening a recent destination must not block the UI")
	}
	checkScreenContains(t, m, "Opening destination")
	next, _ = m.Update(open())
	m = next.(model)
	if m.eng == nil || m.eng.Root() != saved || m.ws.section != secDetails {
		t.Fatal("recent folder did not open Details directly")
	}
	checkScreenContains(t, m, "Remembered album")
}

func TestUnmountedRecentDoesNotOpenLocalMountDirectory(t *testing.T) {
	mount := t.TempDir()
	saved := recentFolder{Path: mount, Mount: mount}
	if recent := availableRecent([]recentFolder{saved}, nil, []string{mount}); len(recent) != 1 {
		t.Fatal("a mounted drive is missing from available destinations")
	}
	if recent := availableRecent([]recentFolder{saved}, nil, nil); len(recent) != 0 {
		t.Fatal("a mount-point directory appeared as an available drive")
	}
	m := sized(newModel(), 80, 24)
	m.history = []recentFolder{saved}
	m.recent = []device.Mount{{Path: mount}}
	next, open := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.Update(open())
	m = next.(model)
	if m.eng != nil {
		t.Fatal("local mount-point directory was opened as a removable drive")
	}
	checkScreenContains(t, m, "Destination is no longer available")
}

func TestMissingRecentFolderIsNeverRecreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Disconnected")
	m := sized(newModel(), 80, 24)
	m.recent = []device.Mount{{Path: root}}
	next, open := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if open == nil {
		t.Fatal("missing open command")
	}
	next, _ = next.Update(open())
	m = next.(model)
	if m.eng != nil || m.opening != "" {
		t.Fatal("unavailable recent became the active destination")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("unavailable folder was recreated: %v", err)
	}
	checkScreenContains(t, m, "Destination is no longer available")
}
