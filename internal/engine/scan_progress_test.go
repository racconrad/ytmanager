package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ytmanager/internal/manifest"
)

func TestLibraryReportsDiscoveryBeforeVerifying(t *testing.T) {
	root := t.TempDir()
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for i := range 80 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("track-%d.opus", i)), []byte("audio fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "mix.m3u"), []byte("track-0.opus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	batches, found := 0, 0
	rows, err := e.RefreshLibraryProgress(context.Background(), func(batch []LibraryTrack) {
		batches++
		found += len(batch)
		if done, _ := e.ScanProgress(); done != 0 {
			t.Errorf("discovery waited for %d files to be verified", done)
		}
		for _, row := range batch {
			if row.Status != "not scanned" || row.Hash != "" || row.Orphan || row.Size == 0 {
				t.Errorf("discovery reported unsupported conclusions: %+v", row)
			}
		}
	})
	if err != nil || batches < 2 || batches >= found || found != 80 || len(rows) != found {
		t.Fatalf("discovery did not publish in batches: batches=%d found=%d verified=%d err=%v", batches, found, len(rows), err)
	}
	for _, row := range rows {
		if row.Hash == "" || (row.Path == "track-0.opus" && (row.Orphan || row.References != 1)) {
			t.Fatalf("final scan lost hash or playlist evidence: %+v", row)
		}
	}
}

func TestDiscoveredTracksAreNotCountedAsSyncedOrUnlisted(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := []LibraryTrack{
		{ID: "managed", Path: "Album/managed.opus", Title: "Managed", Status: "not scanned"},
		{ID: "file:Album/new.opus", Path: "Album/new.opus", Title: "New", Status: "not scanned", Unmanaged: true},
	}
	folders := BuildPhysicalFolders(rows)
	albums := e.UnifiedAlbums(rows)
	if len(folders) != 1 || folders[0].Synced != 0 || folders[0].Orphans != 0 {
		t.Fatalf("provisional rows counted as verified in folders: %+v", folders)
	}
	if len(albums) != 1 || albums[0].Synced != 0 || albums[0].ExtraDisk != 0 || albums[0].Tracks[0].Status != "checking" {
		t.Fatalf("provisional rows counted as verified in albums: %+v", albums)
	}
}

func TestWorkspaceCatalogReadsDoNotCheckFilePresenceUntilScan(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const trackID = "abcdefghijk"
	s := e.m.Merge("album", "saved", "Saved album", "", []manifest.Entry{{ID: trackID, Title: "Saved track"}})
	e.m.Tracks[trackID].Status, e.m.Tracks[trackID].Path = manifest.StatusDone, "missing.opus"

	rows := e.ManifestLibrary()
	if sources := e.CatalogSources(); len(sources) != 1 || sources[0].Done != 1 {
		t.Fatalf("saved sources unavailable before scan: %+v", sources)
	}
	if collection := e.CatalogCollection(s.ID); collection.Synced != 1 {
		t.Fatalf("saved tracks unavailable before scan: %+v", collection)
	}
	if albums, _ := e.InstalledCatalog(); !albums["saved album"] {
		t.Fatalf("saved artists view unavailable before scan: %+v", albums)
	}
	if issues := e.WorkspaceIssues(rows); len(issues) != 0 {
		t.Fatalf("missing file reported before verification: %+v", issues)
	}
	if albums := e.UnifiedAlbums(rows); len(albums) != 1 || albums[0].Missing != 0 || e.presence != nil {
		t.Fatalf("workspace read storage presence before verification: albums=%+v presence=%v", albums, e.presence)
	}

	if _, err := e.ScanLibrary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if collection := e.CatalogCollection(s.ID); collection.Missing != 1 {
		t.Fatalf("catalog ignored verified presence: %+v", collection)
	}
	if albums, _ := e.InstalledCatalog(); albums["saved album"] {
		t.Fatalf("artists catalog ignored verified presence: %+v", albums)
	}
}

func TestLibraryRefreshCancelledDuringDiscoveryLeavesPresenceUnchanged(t *testing.T) {
	root := t.TempDir()
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.opus"), []byte("audio fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.presence = map[string]bool{"old.opus": true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = e.RefreshLibraryProgress(ctx, func([]LibraryTrack) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel during discovery: %v", err)
	}
	if done, _ := e.ScanProgress(); done != 0 || len(e.presence) != 1 || !e.presence["old.opus"] {
		t.Fatalf("cancelled scan published verification: done=%d presence=%v", done, e.presence)
	}
}
