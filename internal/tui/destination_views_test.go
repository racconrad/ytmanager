package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
)

func TestRedesignDevicePickerKeepsFreeSpaceAndReconnectVisible(t *testing.T) {
	m := newModel(device.Mount{Path: "/media/Portable", Free: 2 << 30})
	m.width, m.height = 30, 12
	m.screen = screenDevices
	m.lost = &lost{path: "/media/Detached"}
	m.log("Disconnected")
	m = press(m, "e")
	checkScreenContains(t, m, "1 of 2", "Choose a device", "› /media/Portable", "Drive", "2.0 GB free", "Waiting for", "Disconnected", "esc cancel")

	m = press(m, "esc", "down")
	checkScreenContains(t, m, "› Other path", "x stop waiting")
	m.mounts = []device.Mount{{Path: "/run/user/1000/gvfs/mtp:host=Phone/Storage/Music"}, {Path: "/media/Portable"}}
	m.cursor = 0
	checkScreenContains(t, m, "MTP · Space unknown")
	m = press(m, "down")
	checkScreenContains(t, m, "Drive · Space unknown")
}

func TestRedesignFolderPickerKeepsUseActionAndCurrentPath(t *testing.T) {
	m := newModel()
	m.width, m.height, m.screen = 30, 12, screenBrowse
	m.browse = filepath.Join(t.TempDir(), strings.Repeat("界", 35), "Active music")
	m.dirs = []string{"First", "Unusually long artist collection", "Current"}
	m.cursor = 4
	m.log("Keep this log")
	m = press(m, "e")
	checkScreenContains(t, m, "2 of 2", "Choose a folder", "Active music", "Use this folder", "› Current", "Keep this log")

	m = press(m, "esc")
	m.dirs, m.cursor = nil, 0
	checkScreenContains(t, m, "› Use this folder", "Parent folder", "No subfolders", "u use folder")
}

func TestFolderPickerShowsStorageStatsBeforeLoading(t *testing.T) {
	parent := t.TempDir()
	libDir := filepath.Join(parent, "MusicLib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib, err := manifest.Load(libDir)
	if err != nil {
		t.Fatal(err)
	}
	lib.Format = "m4a"
	lib.Merge("album", "a1", "Discovery", "https://example.com/a1", []manifest.Entry{
		{ID: "t1", Title: "One"},
		{ID: "t2", Title: "Two"},
	})
	lib.Tracks["t1"].Status = manifest.StatusDone
	if err := lib.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	m.width, m.height = 80, 24
	m.openBrowser(parent)
	// Move cursor to MusicLib subfolder (0=Use this folder, 1=Parent folder, 2=MusicLib)
	m = press(m, "down", "down")
	checkScreenContains(t, m, "MusicLib/", "m4a · 1 album · 1 saved · 1 pending")

	// Enter MusicLib: stats show right below "Use this folder"
	m = press(m, "enter")
	checkScreenContains(t, m, "Saved storage · m4a · 1 album · 1 saved · 1 pending")

	// Also fits safely at 30x12
	m.width, m.height = 30, 12
	_ = assertFits(t, m)
}

func TestRedesignSettingsShowsSectionsAndFocusedEffect(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height, m.screen = 80, 30, screenSettings
	checkScreenContains(t, m, "Settings", "Downloads", "Files and folders", "Format", "opus", "data.music")

	m.width, m.height = 30, 12
	m.cursor = 2
	m.opt.Cookies = "chromium"
	m.log("Keep this log")
	checkScreenContains(t, m, "Downloads", "chromium", "Private playlists", "Keep this log")
	m = press(m, "down", "down")
	checkScreenContains(t, m, "Folders: Artist/Album", "Folder layout")
	m.cursor = 10
	checkScreenContains(t, m, "Files and folders", "Originals go to trash", "enter run")
}

func TestRedesignUnavailableTracksShowsSelectedReasonAndActions(t *testing.T) {
	m := scrollModel(t)
	m.width, m.height, m.screen = 30, 12, screenMissing
	checkScreenContains(t, m, "Unavailable tracks", "No unavailable tracks")

	root := t.TempDir()
	library, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	library.Merge("album", "album", "Glass Hours", "https://example.com/album", []manifest.Entry{
		{ID: "first", Title: "Opening", Unavailable: true},
		{ID: "second", Title: "Faint Lines", Unavailable: true},
	})
	library.Tracks["second"].Alt = "old-alternative"
	library.Tracks["second"].Error = "Region restricted"
	if err := library.Save(); err != nil {
		t.Fatal(err)
	}
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.screen = screenMissing
	m = press(m, "down")
	m.log("Keep this log")
	checkScreenContains(t, m, "› Faint Lines", "Glass Hours", "Alternative unavailable", "Region restricted", "enter set alternative", "r retry")
	m = press(m, "enter")
	if m.altFor != "second" {
		t.Fatal("alternative prompt opened for the wrong track")
	}
	checkScreenContains(t, m, "› Faint Lines", "Region restricted", "esc cancel")
	m = press(m, "esc")
	m.width, m.height = 72, 22
	checkScreenContains(t, m, "Opening", "› Faint Lines", "Glass Hours", "Region restricted", "Use a YouTube Music link")
}

func checkScreenContains(t *testing.T, m model, want ...string) {
	t.Helper()
	view := assertFits(t, m)
	for _, label := range want {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q in:\n%s", label, view)
		}
	}
}
