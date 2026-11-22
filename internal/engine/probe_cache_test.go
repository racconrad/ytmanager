package engine

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestReopenedFolderReusesProbesUntilFileChanges(t *testing.T) {
	root := t.TempDir()
	path := writeTestAudio(t, root, "Artist/Song.opus", "abcdefghijk")
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := e.ScanLibrary(context.Background()); err != nil || rows[0].SourceID != "abcdefghijk" {
		t.Fatalf("first scan: %+v %v", rows, err)
	}

	// Without ffprobe, only cached results can still report the tags.
	t.Setenv("PATH", t.TempDir())
	reopened, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reopened.ScanLibrary(context.Background())
	if err != nil || rows[0].SourceID != "abcdefghijk" || rows[0].Status != "present" || rows[0].Hash == "" {
		t.Fatalf("cached scan: %+v %v", rows, err)
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if rows, _ = reopened.ScanLibrary(context.Background()); rows[0].Status == "present" {
		t.Fatalf("changed file used a stale probe: %+v", rows[0])
	}
}
