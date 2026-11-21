package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUndoKeepsPreexistingOrganizeTargets(t *testing.T) {
	for _, kind := range []string{"audio", "lyrics"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			e, err := Open(t.TempDir(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			audio := writeTestAudio(t, e.Root(), "loose.opus", "track")
			rows, err := e.ScanLibrary(ctx)
			if err != nil || len(rows) != 1 {
				t.Fatalf("scan: %v, %v", rows, err)
			}
			to := filepath.Join("folder", "target.opus")
			if err := os.Mkdir(filepath.Join(e.Root(), "folder"), 0755); err != nil {
				t.Fatal(err)
			}
			dest, contents := filepath.Join(e.Root(), to), readRegressionFile(t, audio)
			if kind == "lyrics" {
				dest = filepath.Join(e.Root(), "folder", "target.lrc")
				contents = []byte("pre-existing lyrics")
				if err := os.WriteFile(filepath.Join(e.Root(), "loose.lrc"), contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(dest, contents, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = e.ApplyLibraryPlan(ctx, LibraryPlan{Changes: []LibraryChange{{Track: rows[0], To: to}}}); err == nil {
				t.Fatal("apply did not report the occupied destination")
			}
			if _, err = e.RestoreTrash(ctx); err != nil {
				t.Fatal(err)
			}
			if got := readRegressionFile(t, dest); !bytes.Equal(got, contents) {
				t.Fatal("undo changed a file this operation did not create")
			}
			readRegressionFile(t, audio)
		})
	}
}

func TestUndoResetDoesNotQueueRestoredTracks(t *testing.T) {
	ctx := context.Background()
	e := libraryFixture(t)
	if _, err := e.ResetTracks(ctx, e.Library()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RestoreTrash(ctx); err != nil {
		t.Fatal(err)
	}
	if reopened.HasPending() {
		t.Fatal("undo restored files but left tracks queued for replacement")
	}
}

func TestRelativeDestinationProtectsAbsolutePlaylistEntries(t *testing.T) {
	root := t.TempDir()
	t.Chdir(filepath.Dir(root))
	e, err := Open(filepath.Base(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	audio := writeTestAudio(t, root, "local.opus", "track")
	if err := os.WriteFile(filepath.Join(root, "custom.m3u"), []byte(audio+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := e.ScanLibrary(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("scan: %v, %v", rows, err)
	}
	if rows[0].Orphan || rows[0].Sources != "Local playlist" {
		t.Fatalf("playlist member is eligible for cleanup: %+v", rows[0])
	}
}

func readRegressionFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestUndoRestoresActualSpellingOfManagedFile(t *testing.T) {
	ctx := context.Background()
	e := libraryFixture(t)
	original := filepath.Join(e.Root(), "a.opus")
	actual := filepath.Join(e.Root(), "A.opus")
	if err := os.Rename(original, actual); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Skip("filesystem does not distinguish the case-only paths")
	}
	before := readRegressionFile(t, actual)
	if _, err := e.TrashFiles(ctx, []LibraryTrack{{ID: "a", Path: "A.opus"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreTrash(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readRegressionFile(t, actual), before) || e.m.Tracks["a"].Path != "A.opus" || e.HasPending() {
		t.Fatalf("undo did not restore both the physical and managed path: %+v", e.m.Tracks["a"])
	}
}

func TestPlaylistProtectsCaseAliasOfSameFile(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	audio := writeTestAudio(t, e.Root(), "Track.opus", "track")
	alias := filepath.Join(e.Root(), "track.opus")
	if _, err := os.Stat(alias); os.IsNotExist(err) {
		if err := os.Link(audio, alias); err != nil {
			t.Skipf("cannot create a second spelling of the same file: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.Root(), "custom.m3u"), []byte("track.opus\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Path == "Track.opus" {
			if row.Orphan {
				t.Fatal("playlist path refers to the same file, but cleanup considers it unlisted")
			}
			return
		}
	}
	t.Fatal("fixture track was not scanned")
}
