package engine

import (
	"os"
	"path/filepath"
	"testing"

	"ytmanager/internal/manifest"
)

func TestPreviewAndApplyPhysicalSyncCoversAllFourCategories(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "alb1", "Discovery", "https://music.youtube.com/playlist?list=alb1", []manifest.Entry{
		{ID: "t_meta", Title: "Old Title", Artist: "Old Artist"},
		{ID: "t_moved", Title: "Moved Track"},
		{ID: "t_deleted", Title: "Deleted Track"},
	})
	data.Tracks["t_meta"].Status, data.Tracks["t_meta"].Path = manifest.StatusDone, filepath.Join("Daft Punk", "Discovery", "01.opus")
	data.Tracks["t_moved"].Status, data.Tracks["t_moved"].Path = manifest.StatusDone, filepath.Join("Daft Punk", "Discovery", "02.opus")
	data.Tracks["t_deleted"].Status, data.Tracks["t_deleted"].Path = manifest.StatusDone, filepath.Join("Daft Punk", "Discovery", "03.opus")
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	eng, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	rows := []LibraryTrack{
		// Metadata edited on disk
		{ID: "t_meta", Title: "One More Time (Remaster)", Artist: "Daft Punk", Path: filepath.Join("Daft Punk", "Discovery", "01.opus"), Status: "present"},
		// Old path missing, moved file exists with matching SourceID
		{ID: "t_moved", Title: "Moved Track", Path: filepath.Join("Daft Punk", "Discovery", "02.opus"), Missing: true, Status: "missing"},
		{ID: "file:moved", Title: "Moved Track", Path: filepath.Join("Daft Punk", "Discovery", "02 - Aerodynamic.opus"), Status: "present", Unmanaged: true, SourceID: "t_moved"},
		// Deleted on disk
		{ID: "t_deleted", Title: "Deleted Track", Path: filepath.Join("Daft Punk", "Discovery", "03.opus"), Missing: true, Status: "missing"},
		// Brand new local file dropped into album folder
		{ID: "file:bonus", Title: "Bonus Track", Artist: "Daft Punk", Path: filepath.Join("Daft Punk", "Discovery", "04 - Bonus.opus"), Status: "present", Unmanaged: true, Orphan: true, Hash: "abcdef0123456789abcdef"},
	}

	plan := eng.PreviewPhysicalSync(rows)
	if plan.Count(ChangeRelink) != 1 || plan.Count(ChangeMetadata) != 1 || plan.Count(ChangeAdoptLocal) != 1 || plan.Count(ChangeAdoptDelete) != 1 {
		t.Fatalf("unexpected plan counts: %+v", plan.Changes)
	}

	report, err := eng.ApplyPhysicalSync(plan, PhysicalSyncOptions{
		Relink:      true,
		Metadata:    true,
		AdoptLocal:  true,
		AdoptDelete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Relinked != 1 || report.Updated != 1 || report.Adopted != 1 || report.Removed != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(report.Backup); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}

	reloaded, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Tracks["t_meta"].Title != "One More Time (Remaster)" || reloaded.Tracks["t_meta"].Artist != "Daft Punk" {
		t.Fatalf("metadata not updated: %+v", reloaded.Tracks["t_meta"])
	}
	if reloaded.Tracks["t_moved"].Path != filepath.Join("Daft Punk", "Discovery", "02 - Aerodynamic.opus") {
		t.Fatalf("moved track not relinked: %+v", reloaded.Tracks["t_moved"])
	}
	if !reloaded.Tracks["t_deleted"].PermanentlyRemoved || reloaded.Tracks["t_deleted"].Status != manifest.StatusRemoved {
		t.Fatalf("deleted track not adopted as removed: %+v", reloaded.Tracks["t_deleted"])
	}
	localTrack := reloaded.Tracks["local:abcdef0123456789"]
	if localTrack == nil || !localTrack.Local || localTrack.Title != "Bonus Track" {
		t.Fatalf("local track not adopted: %+v", localTrack)
	}
	src := reloaded.Source("alb1")
	if len(reloaded.Sources) != 1 {
		t.Fatalf("physical sync created a source: %+v", reloaded.Sources)
	}
	if len(src.Tracks) != 3 { // t_meta, t_moved, local:abcdef... (t_deleted dropped)
		t.Fatalf("album tracks = %v, want 3", src.Tracks)
	}
}

func TestPhysicalSyncDoesNotCreateSourcesForUnlistedFilesOutsideManagedAlbums(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "album-1", "Existing Album", "https://music.youtube.com/playlist?list=album-1", []manifest.Entry{
		{ID: "known", Title: "Known Track"},
	})
	data.Tracks["known"].Status = manifest.StatusDone
	data.Tracks["known"].Path = filepath.Join("Artist", "Existing Album", "01.opus")
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	eng, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}

	plan := eng.PreviewPhysicalSync([]LibraryTrack{
		{ID: "known", Title: "Known Track", Path: filepath.Join("Artist", "Existing Album", "01.opus"), Status: "present"},
		{ID: "file:loose", Title: "Loose Track", Path: filepath.Join("Loose", "track.opus"), Status: "present", Unmanaged: true, Orphan: true},
	})
	if len(plan.Changes) != 0 {
		t.Fatalf("unlisted file outside a known album was associated: %+v", plan.Changes)
	}
	if len(eng.Snapshot()) != 1 {
		t.Fatalf("physical sync changed source count: %d", len(eng.Snapshot()))
	}
}
