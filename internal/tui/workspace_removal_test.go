package tui

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func TestWorkspaceScopedRemovalKeepsOtherPlaylists(t *testing.T) {
	m, _ := workspaceRemovalModel(t)
	m = press(m, "3", "enter", "/", "one", "enter", " ")
	if got := assertFits(t, m); !strings.Contains(got, "1 marked") {
		t.Fatalf("marked selection not visible:\n%s", got)
	}
	m = finishRemovalKey(t, m, "-")
	one, two := m.eng.Collection("first"), m.eng.Collection("second")
	if len(one.Tracks) != 1 || one.Tracks[0].ID != "two" || len(two.Tracks) != 2 {
		t.Fatalf("wrong scope: first=%+v, second=%+v", one, two)
	}
}

func TestWorkspaceCompactSourcesShowsMarksAndScopeShortcuts(t *testing.T) {
	m, _ := workspaceRemovalModel(t)
	m = press(m, "3", "enter", "a")
	m.ws.sourceDetail.Title = "A very long source title for a compact terminal"
	got := assertFits(t, sized(m, 30, 12))
	for _, text := range []string{"2 marked", "d delete", "- from list"} {
		if !strings.Contains(got, text) {
			t.Fatalf("compact list hides %q:\n%s", text, got)
		}
	}
}

func TestWorkspacePermanentRemovalConfirmsBatchAndKeepsUnmarkedAudio(t *testing.T) {
	m, root := workspaceRemovalModel(t)
	m = press(m, "3", "enter", " ", "down", " ")
	m = press(m, "d")
	check := assertFits(t, sized(m, 30, 12))
	if !strings.Contains(check, "Delete 2 tracks?") || !strings.Contains(check, "Cannot be restored") {
		t.Fatalf("missing irreversible confirmation:\n%s", check)
	}
	if m = press(m, "esc"); len(m.eng.Collection("first").Tracks) != 2 {
		t.Fatal("cancel modified the playlist")
	}
	m = finishRemovalKey(t, press(m, "d"), "enter")
	if len(m.eng.Collection("first").Tracks) != 0 || len(m.eng.Collection("second").Tracks) != 0 {
		t.Fatal("permanent deletion did not clear both lists")
	}
	for _, p := range []string{"One.opus", "Two.opus"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("selected audio %s still exists: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Unmarked.opus")); err != nil {
		t.Fatalf("unmarked audio changed: %v", err)
	}
}

func TestPhysicalActionDoesNotFallBackWhenMarkedTrackHasNoFile(t *testing.T) {
	m := scrollModel(t)
	m.ws.albums = []engine.UnifiedAlbum{{
		Key: "source:album", Tracks: []engine.UnifiedTrack{
			{ID: "downloaded", LibraryRow: &engine.LibraryTrack{ID: "downloaded", Path: "downloaded.opus"}},
			{ID: "pending"},
		},
	}}
	m.ws.albumKey = "source:album"
	m.ws.markedTracks[m.ws.albumKey+"\x00pending"] = true
	if rows := m.selectedPhysicalRows(); len(rows) != 0 {
		t.Fatalf("non-physical mark redirected operation to %+v", rows)
	}
}

func TestWholeManagedAlbumDeletionKeepsNonmemberFilesInSameFolder(t *testing.T) {
	m, root := workspaceRemovalModel(t)
	st, err := os.Stat(filepath.Join(root, "Unmarked.opus"))
	if err != nil {
		t.Fatal(err)
	}
	m.library.all = append(m.library.all, engine.LibraryTrack{
		ID: "file:Unmarked.opus", Title: "Unmarked", Path: "Unmarked.opus",
		Status: "present", Size: st.Size(), Modified: st.ModTime().UnixNano(),
	})
	m.refreshWorkspace()
	if len(m.ws.albums) != 1 || m.ws.albums[0].ExtraDisk != 1 {
		t.Fatalf("fixture should attach a nonmember file: %+v", m.ws.albums)
	}
	m = press(m, "d")
	if len(m.ws.deleteReview) != 2 {
		t.Fatalf("whole album would delete nonmember files: %+v", m.ws.deleteReview)
	}
	m = finishRemovalKey(t, m, "enter")
	if _, err := os.Stat(filepath.Join(root, "Unmarked.opus")); err != nil {
		t.Fatalf("unrelated file changed: %v", err)
	}
}

func workspaceRemovalModel(t *testing.T) (model, string) {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"One.opus", "Two.opus", "Unmarked.opus"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}
	data.Merge("album", "first", "First", "", entries)
	data.Merge("playlist", "second", "Second", "", entries)
	for _, e := range entries {
		data.Tracks[e.ID].Path, data.Tracks[e.ID].Status = e.Title+".opus", manifest.StatusDone
	}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	m.width, m.height = 105, 28
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.library.all = m.eng.Library()
	for i := range m.library.all {
		m.library.all[i].Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(m.library.all[i].Path)))
	}
	m.library.complete = true
	m.refreshWorkspace()
	return m, root
}

func finishRemovalKey(t *testing.T, m model, key string) model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	if key == "enter" {
		next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	if cmd == nil {
		t.Fatalf("key %s did not start an operation", key)
	}
	next, _ = next.(model).Update(cmd())
	return next.(model)
}
