package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"ytmanager/internal/manifest"
)

func TestRepairOpaqueFailuresAndCancellation(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.m.Merge("playlist", "p", "Playlist", "url", []manifest.Entry{{ID: "x", Title: "Song"}})
	e.m.Tracks["x"].Status = manifest.StatusUnavailable
	e.m.Tracks["x"].Error = "exit status 1"
	if err = e.m.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e.Repair(ctx, func(Event) {}) == nil {
		t.Fatal("ignored cancellation")
	}
	if e.m.Tracks["x"].Status != manifest.StatusUnavailable {
		t.Fatal("cancel changed state")
	}
	if err = e.Repair(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if e.m.Tracks["x"].Status != manifest.StatusPending {
		t.Fatal("did not recover retry")
	}
	backups, _ := filepath.Glob(filepath.Join(e.Root(), "data.music.repair-*"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	if _, err = os.Stat(filepath.Join(e.Root(), "Playlist.m3u8")); err != nil {
		t.Fatal(err)
	}
}
func TestExportPreservesCustomAndRefreshesOwned(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.m.Merge("playlist", "p", "Playlist", "url", []manifest.Entry{{ID: "x", Title: "Song"}})
	if err = e.ExportM3U(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	e.m.Tracks["x"].Status = manifest.StatusDone
	e.m.Tracks["x"].Path = "song.opus"
	if err = e.ExportM3U(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(e.Root(), "Playlist.m3u")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "song.opus") {
		t.Fatal("export not refreshed")
	}
	os.WriteFile(p, []byte("custom"), 0600)
	if err = e.ExportM3U(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if string(b) != "custom" {
		t.Fatal("custom overwritten")
	}
}

func TestRepairAndRecoverConsolidateUntaggedAlbumFiles(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	root := t.TempDir()
	rel := filepath.Join("Artist", "Album", "01 - Song.opus")
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "libopus",
		"-metadata", "title=Song (feat. Guest)", "-metadata", "artist=Artist", "-metadata", "album_artist=Artist", "-metadata", "album=Album", full)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %s %v", out, err)
	}

	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := e.m.Merge("album", "OLAK1", "Album", "https://music.youtube.com/playlist?list=OLAK1", []manifest.Entry{{ID: "vid1", Title: "Song"}})
	s.AlbumArtist = "Artist"
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}
	if err := e.Repair(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if tr := e.m.Tracks["vid1"]; tr.Status != manifest.StatusDone || tr.Path != rel {
		t.Fatalf("Repair did not consolidate physical album file: %+v", tr)
	}
	pl, err := os.ReadFile(filepath.Join(root, "Album.m3u8"))
	if err != nil || !strings.Contains(string(pl), filepath.ToSlash(rel)) {
		t.Fatalf("playlist missing consolidated file: %v %s", err, pl)
	}

	// Verify recoverExisting also reuses the untagged physical file on Download without calling trackWork.
	e.m.Tracks["vid1"].Path = ""
	e.m.Tracks["vid1"].Status = manifest.StatusPending
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Fatal("trackWork called despite matching physical file")
		return true
	}
	if err := e.Download(context.Background(), nil, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if tr := e.m.Tracks["vid1"]; tr.Status != manifest.StatusDone || tr.Path != rel {
		t.Fatalf("recoverExisting did not consolidate physical file: %+v", tr)
	}
}

func TestRepairPromotesSinglesAndResolvesCanonicalDuplicateAndEditionTags(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe", "yt-dlp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	root := t.TempDir()

	// 1. Single placed in Singles/ that now belongs to an Album + Playlist.
	singleRel := filepath.Join("Artist", "Singles", "First Song.opus")
	writeTestAudio(t, root, singleRel, "t1")
	// Clear album/track metadata on singleRel to mimic a playlist-first download.
	noAlbum := filepath.Join(root, "tmp_single.opus")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "libopus",
		"-metadata", "title=First Song", "-metadata", "artist=Artist", "-metadata", "album_artist=Artist", "-metadata", "purl=https://www.youtube.com/watch?v=t1", noAlbum).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg single: %s %v", out, err)
	}
	if err := os.Rename(noAlbum, filepath.Join(root, singleRel)); err != nil {
		t.Fatal(err)
	}

	// 2. Duplicate copies with same SourceID where one is at the canonical album path.
	canonicalRel := filepath.Join("Artist", "Record", "02 - Second Song.opus")
	strayRel := filepath.Join("Guest", "Record", "02 - Second Song (feat. Artist).opus")
	writeTestAudio(t, root, canonicalRel, "t2")
	writeTestAudio(t, root, strayRel, "t2")

	// 3. Edition-suffixed metadata without purl.
	editionRel := filepath.Join("Artist", "Record (Deluxe)", "03 - Third Song (Remastered 2020).opus")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, editionRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "libopus",
		"-metadata", "title=Third Song (Remastered 2020)", "-metadata", "artist=Artist", "-metadata", "album_artist=Artist", "-metadata", "album=Record (Deluxe)", filepath.Join(root, editionRel)).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg edition: %s %v", out, err)
	}

	// 4. Tagless audio file identified via path fallback (04 - Fourth Song.opus).
	taglessRel := filepath.Join("Artist", "Record", "04 - Fourth Song.opus")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "libopus",
		filepath.Join(root, taglessRel)).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg tagless: %s %v", out, err)
	}

	e, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	alb := e.m.Merge("album", "OLAK_rec", "Record", "https://music.youtube.com/playlist?list=OLAK_rec", []manifest.Entry{
		{ID: "t1", Title: "First Song", Artist: "Artist"},
		{ID: "t2", Title: "Second Song", Artist: "Artist"},
		{ID: "t3", Title: "Third Song", Artist: "Artist"},
		{ID: "t4", Title: "Fourth Song", Artist: "Artist"},
	})
	alb.AlbumArtist = "Artist"
	e.m.Merge("playlist", "PL_mix", "Mix", "https://music.youtube.com/playlist?list=PL_mix", []manifest.Entry{
		{ID: "t1", Title: "First Song", Artist: "Artist"},
	})
	e.m.Tracks["t1"].Path = singleRel
	e.m.Tracks["t1"].Status = manifest.StatusDone
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}

	if err := e.Repair(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}

	wantT1 := filepath.Join("Artist", "Record", "01 - First Song.opus")
	if e.m.Tracks["t1"].Path != wantT1 {
		t.Fatalf("t1 not promoted to album folder: got %q want %q", e.m.Tracks["t1"].Path, wantT1)
	}
	if _, err := os.Stat(filepath.Join(root, wantT1)); err != nil {
		t.Fatalf("promoted file missing: %v", err)
	}
	if e.m.Tracks["t2"].Path != canonicalRel {
		t.Fatalf("t2 did not pick canonical copy: got %q want %q", e.m.Tracks["t2"].Path, canonicalRel)
	}
	if e.m.Tracks["t3"].Path != editionRel {
		t.Fatalf("t3 edition fallback failed: got %q want %q", e.m.Tracks["t3"].Path, editionRel)
	}
	if e.m.Tracks["t4"].Path != taglessRel {
		t.Fatalf("t4 path fallback failed: got %q want %q", e.m.Tracks["t4"].Path, taglessRel)
	}
	mixM3U, err := os.ReadFile(filepath.Join(root, "Mix.m3u8"))
	if err != nil || !strings.Contains(string(mixM3U), filepath.ToSlash(wantT1)) {
		t.Fatalf("playlist m3u8 not updated after promotion: %v %s", err, mixM3U)
	}
}
