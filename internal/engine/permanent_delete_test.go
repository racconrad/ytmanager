package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ytmanager/internal/manifest"
)

func TestPermanentDeletionRemovesAudioAndPreventsResync(t *testing.T) {
	e := libraryFixture(t)
	path := filepath.Join("Album", "a.opus")
	if err := os.Mkdir(filepath.Join(e.Root(), "Album"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.Root(), "a.opus"), filepath.Join(e.Root(), path)); err != nil {
		t.Fatal(err)
	}
	e.m.Tracks["a"].Path = path
	e.m.Merge("playlist", "second", "Second", "", []manifest.Entry{{ID: "a", Title: "A"}})
	e.m.Tracks["alias"] = &manifest.Track{Title: "Alias", Path: path, Status: manifest.StatusDone}
	e.m.Merge("album", "alias-album", "Alias", "", []manifest.Entry{{ID: "alias", Title: "Alias"}})
	row := deleteFixtureRow(t, e, "a")
	report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row})
	if err != nil || report.Deleted != 1 || !report.Permanent {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	for _, name := range []string{path, trashDir} {
		if _, err := os.Lstat(filepath.Join(e.Root(), name)); !os.IsNotExist(err) {
			t.Fatalf("%s should not exist, stat error: %v", name, err)
		}
	}
	opened, err := manifest.Load(e.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Source("second").TrackOrder()) != 0 || len(opened.Source("alias-album").TrackOrder()) != 0 {
		t.Fatalf("deleted tracks survived in another source: %+v", opened.Sources)
	}
	s := opened.Merge("playlist", "second", "Second", "", []manifest.Entry{{ID: "a", Title: "A"}})
	if len(s.TrackOrder()) != 0 {
		t.Fatal("refresh restored the permanently deleted track")
	}
}

func TestPermanentDeletionKeepsChangedAudioAndDeletesVerifiedSelection(t *testing.T) {
	e := libraryFixture(t)
	a, b := deleteFixtureRow(t, e, "a"), deleteFixtureRow(t, e, "b")
	if err := os.WriteFile(filepath.Join(e.Root(), a.Path), []byte("replacement that was not reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := e.DeletePermanently(context.Background(), []LibraryTrack{a, b})
	if err == nil || report.Deleted != 1 || report.Failed != 1 {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	if got, err := os.ReadFile(filepath.Join(e.Root(), a.Path)); err != nil || string(got) != "replacement that was not reviewed" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
	if e.m.Tracks[a.ID].Status != manifest.StatusDone || e.m.Tracks[b.ID].Status != manifest.StatusRemoved {
		t.Fatal("manifest does not reflect the partial result")
	}
}

func TestPermanentDeletionSuppressesTracksWithoutAudio(t *testing.T) {
	e := libraryFixture(t)
	e.m.Merge("playlist", "pending-list", "Pending", "", []manifest.Entry{{ID: "pending", Title: "Pending"}})
	row := LibraryTrack{ID: "pending"}
	report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row})
	if err != nil || report.Tracks != 1 || len(e.Collection("pending-list").Tracks) != 0 {
		t.Fatalf("delete = %+v, %v; collection %+v", report, err, e.Collection("pending-list"))
	}
}

func TestPermanentDeletionCannotBeReversedByOlderTrashBatch(t *testing.T) {
	e := libraryFixture(t)
	row := deleteFixtureRow(t, e, "a")
	if _, err := e.TrashFiles(context.Background(), []LibraryTrack{row}); err != nil {
		t.Fatal(err)
	}
	row.Missing = true
	if _, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err != nil {
		t.Fatal(err)
	}
	report, err := e.RestoreTrash(context.Background())
	if err != nil || report.Restored != 0 {
		t.Fatalf("restored permanently deleted audio: %+v, %v", report, err)
	}
	if _, err := os.Lstat(filepath.Join(e.Root(), row.Path)); !os.IsNotExist(err) {
		t.Fatalf("deleted file was restored: %v", err)
	}
}

func TestInterruptedPermanentDeletionDoesNotReappearInLists(t *testing.T) {
	e := libraryFixture(t)
	e.m.Tracks["a"].Status = manifest.StatusRemoved
	e.m.Tracks["a"].PermanentlyRemoved = true
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Collection("album").Tracks; len(got) != 2 {
		t.Fatalf("interrupted deletion reappeared in source: %+v", got)
	}
	if _, err := os.Lstat(filepath.Join(e.Root(), "a.opus")); err != nil {
		t.Fatalf("loading the catalog must not silently delete unverified audio: %v", err)
	}
}

func TestPermanentDeletionRequiresRelinkingMovedManagedAudio(t *testing.T) {
	e := libraryFixture(t)
	path := filepath.Join(e.Root(), "moved.opus")
	if err := os.Rename(filepath.Join(e.Root(), "a.opus"), path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	row := LibraryTrack{
		ID: "file:moved.opus", Path: "moved.opus", Unmanaged: true, SourceID: "a",
		Size: st.Size(), Modified: st.ModTime().UnixNano(),
	}
	if report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err == nil || report.Deleted != 0 {
		t.Fatalf("managed move deleted without relinking: %+v, %v", report, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("moved audio changed: %v", err)
	}
}

func TestPermanentDeletionRejectsUnverifiedAndCaseOnlyMatches(t *testing.T) {
	t.Run("without content evidence", func(t *testing.T) {
		e := libraryFixture(t)
		row := deleteFixtureRow(t, e, "a")
		row.Hash = ""
		if report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err == nil || report.Deleted != 0 {
			t.Fatalf("unverified audio deleted: %+v, %v", report, err)
		}
	})
	t.Run("case-only path", func(t *testing.T) {
		e := libraryFixture(t)
		if err := os.Remove(filepath.Join(e.Root(), "a.opus")); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(e.Root(), "A.opus")
		if err := os.WriteFile(path, []byte("independent audio"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(e.Root(), "a.opus")); !os.IsNotExist(err) {
			t.Skip("requires a case-sensitive filesystem")
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := hashFile(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		row := LibraryTrack{ID: "a", Path: "A.opus", Size: st.Size(), Modified: st.ModTime().UnixNano(), Hash: digest}
		if report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err == nil || report.Deleted != 0 {
			t.Fatalf("case-only match deleted: %+v, %v", report, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("independent file changed: %v", err)
		}
	})
}

func TestPermanentDeletionCannotFollowAnAlbumFolderReplacedByASymlink(t *testing.T) {
	e := libraryFixture(t)
	for _, dir := range []string{"Album", "Backup"} {
		if err := os.Mkdir(filepath.Join(e.Root(), dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rel := filepath.Join("Album", "a.opus")
	if err := os.Rename(filepath.Join(e.Root(), "a.opus"), filepath.Join(e.Root(), rel)); err != nil {
		t.Fatal(err)
	}
	e.m.Tracks["a"].Path = rel
	row := deleteFixtureRow(t, e, "a")
	backup := filepath.Join(e.Root(), "Backup", "a.opus")
	if err := os.WriteFile(backup, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Unix(0, row.Modified)
	if err := os.Chtimes(backup, ts, ts); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.Root(), "Album"), filepath.Join(e.Root(), "Original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Backup", filepath.Join(e.Root(), "Album")); err != nil {
		t.Skipf("directory symlink not available: %v", err)
	}
	if report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err == nil || report.Deleted != 0 {
		t.Fatalf("symlink redirected deletion: %+v, %v", report, err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("independent backup changed: %v", err)
	}
}

func TestPermanentDeletionRejectsDestinationReplacedByASymlink(t *testing.T) {
	base := t.TempDir()
	dest, backup := filepath.Join(base, "music"), filepath.Join(base, "backup")
	for _, dir := range []string{dest, backup} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Open(dest, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(backup, "a.opus")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dest, filepath.Join(base, "previous")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backup, dest); err != nil {
		t.Skipf("directory symlink not available: %v", err)
	}
	row := LibraryTrack{ID: "file:a.opus", Path: "a.opus", Hash: hash, Modified: st.ModTime().UnixNano(), Size: st.Size()}
	if report, err := e.DeletePermanently(context.Background(), []LibraryTrack{row}); err == nil || report.Deleted != 0 {
		t.Fatalf("redirected destination passed: %+v, %v", report, err)
	}
}

func deleteFixtureRow(t *testing.T, e *Engine, id string) LibraryTrack {
	t.Helper()
	for _, row := range e.Library() {
		if row.ID == id {
			hash, err := hashFile(context.Background(), filepath.Join(e.Root(), row.Path))
			if err != nil {
				t.Fatal(err)
			}
			row.Hash = hash
			return row
		}
	}
	t.Fatalf("missing fixture track %s", id)
	return LibraryTrack{}
}
