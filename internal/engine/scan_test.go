package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanActualFilesAndIdenticalCopies(t *testing.T) {
	root := t.TempDir()
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "a.opus"), []byte("same bytes"), 0600)
	os.WriteFile(filepath.Join(root, "b.opus"), []byte("same bytes"), 0600)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("same bytes"), 0600)
	t.Setenv("PATH", t.TempDir())
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d", len(rows))
	}
	for _, r := range rows {
		if !r.Unmanaged || !r.Duplicate || r.Size != 10 {
			t.Fatalf("bad row: %+v", r)
		}
	}
	if len(e.m.Tracks) != 0 {
		t.Fatal("scan modified manifest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.ScanLibrary(ctx); err == nil {
		t.Fatal("ignored cancel")
	}
}

func TestMarkDuplicatesIgnoresIncompleteAndDistinctEditions(t *testing.T) {
	rows := []LibraryTrack{
		{ID: "full", Artist: "Artist", Title: "Song", Album: "Album A", Path: "Album A/song.opus", Duration: 210, Size: 1000, Hash: "h1", SourceID: "id1"},
		{ID: "cut", Artist: "Artist", Title: "Song", Album: "Album A", Path: "Extras/song_cut.opus", Duration: 42, Size: 200, Hash: "h2", SourceID: "id2"},
		{ID: "broken", Artist: "Artist", Title: "Song", Album: "Album A", Path: "Broken/song.opus", Invalid: true, Size: 12, Hash: "h3"},
		{ID: "live", Artist: "Artist", Title: "Song", Album: "Live Album", Path: "Live/song.opus", Duration: 214, Size: 1020, Hash: "h4", SourceID: "id_live"},
	}
	markDuplicates(rows)
	for _, r := range rows {
		if r.Duplicate {
			t.Fatalf("false positive duplicate flagged: %+v", r)
		}
	}

	// Same recording in two formats with matching duration IS flagged.
	realDup := []LibraryTrack{
		{ID: "a", Artist: "Artist", Title: "Song", Album: "Album A", Path: "a.opus", Duration: 210, Size: 1000, Hash: "h1"},
		{ID: "b", Artist: "Artist", Title: "Song", Album: "Album A", Path: "b.m4a", Duration: 211, Size: 1400, Hash: "h2"},
	}
	markDuplicates(realDup)
	if !realDup[0].Duplicate || !realDup[1].Duplicate {
		t.Fatalf("true cross-format duplicate not flagged: %+v", realDup)
	}
}

func TestTrashFilesAllowsExplicitPhysicalDeleteEvenWithMovedSourceID(t *testing.T) {
	root := t.TempDir()
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("Extras", "moved.opus")
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := LibraryTrack{ID: "file:" + rel, Path: rel, Unmanaged: true, SourceID: "missingTrackID", Size: 11}
	report, err := e.TrashFiles(context.Background(), []LibraryTrack{row})
	if err != nil || report.Deleted != 1 {
		t.Fatalf("explicit physical trash failed: report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(full); !os.IsNotExist(statErr) {
		t.Fatalf("physical file still exists after trash")
	}
	restored, err := e.RestoreTrash(context.Background())
	if err != nil || restored.Restored != 1 {
		t.Fatalf("restore failed: report=%+v err=%v", restored, err)
	}
}
