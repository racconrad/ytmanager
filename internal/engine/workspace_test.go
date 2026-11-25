package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ytmanager/internal/manifest"
)

func TestWorkspaceFoldersIssuesAndUnifiedAlbums(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	root := t.TempDir()
	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	syncedRel := filepath.Join("Artist", "Record", "01 - Synced.opus")
	unlistedRel := filepath.Join("PhoneDownloads", "Loose.opus")
	writeTestAudio(t, root, syncedRel, "t1")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, unlistedRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000", "-t", "0.3", "-c:a", "libopus",
		"-metadata", "title=Loose", "-metadata", "artist=Other", filepath.Join(root, unlistedRel)).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg loose: %s %v", out, err)
	}
	alb := e.m.Merge("album", "OLAK1", "Record", "https://music.youtube.com/playlist?list=OLAK1", []manifest.Entry{
		{ID: "t1", Title: "Synced"},
		{ID: "t2", Title: "FailedTrack"},
		{ID: "t3", Title: "MissingTrack"},
	})
	alb.AlbumArtist = "Artist"
	e.m.Tracks["t1"].Path = syncedRel
	e.m.Tracks["t1"].Status = manifest.StatusDone
	e.m.Tracks["t2"].Status = manifest.StatusFailed
	e.m.Tracks["t2"].Error = "403 Forbidden"
	e.m.Tracks["t3"].Path = filepath.Join("Artist", "Record", "03 - MissingTrack.opus")
	e.m.Tracks["t3"].Status = manifest.StatusDone
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}

	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	folders := BuildPhysicalFolders(rows)
	if len(folders) != 2 {
		t.Fatalf("folders = %d (%+v), want 2", len(folders), folders)
	}
	if folders[0].Dir != "Artist/Record" || folders[1].Dir != "PhoneDownloads" || folders[1].Orphans != 1 {
		t.Fatalf("unexpected folders: %+v", folders)
	}

	issues := e.WorkspaceIssues(rows)
	kinds := map[string]bool{}
	for _, iss := range issues {
		kinds[iss.Kind] = true
	}
	for _, want := range []string{"download_failed", "missing_file", "unlisted"} {
		if !kinds[want] {
			t.Fatalf("missing issue kind %q in %+v", want, issues)
		}
	}

	albums := e.UnifiedAlbums(rows)
	if len(albums) != 2 || !albums[0].HasSource || albums[0].Synced != 1 || albums[0].Failed != 1 || albums[0].Missing != 1 {
		t.Fatalf("unexpected unified albums: %+v", albums)
	}
}
