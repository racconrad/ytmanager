package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"ytmanager/internal/manifest"
)

func TestReaddExcludedTrackReusesAudio(t *testing.T) {
	e := libraryFixture(t)
	entries := []manifest.Entry{{ID: "a"}, {ID: "b"}}
	e.m.Merge("playlist", "first", "First", "", entries)
	e.m.Merge("playlist", "second", "Second", "", entries)
	if _, err := e.ExcludeSourceTracks("first", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(e.Root(), "Second.m3u8")
	if err := os.WriteFile(other, []byte("#EXTM3U\n# ytmanager source: second\n# do not rewrite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := e.RestorableTracks("first"); len(got) != 2 {
		t.Fatalf("candidates = %+v", got)
	}
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Error("download requested despite existing audio")
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", "a", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if got := e.Collection("first").Tracks; len(got) != 1 || got[0].ID != "a" || !got[0].Synced {
		t.Fatalf("first = %+v", got)
	}
	if got := e.Collection("second").Tracks; len(got) != 2 {
		t.Fatalf("second = %+v", got)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "#EXTM3U\n# ytmanager source: second\n# do not rewrite\n" {
		t.Fatalf("other export changed: %q, %v", data, err)
	}
	if got := e.RestorableTracks("first"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("remaining candidates = %+v", got)
	}
}

func TestReaddPermanentTrackDoesNotResumeOrReattachOtherTracks(t *testing.T) {
	e := libraryFixture(t)
	entries := []manifest.Entry{{ID: "a"}, {ID: "pending"}}
	e.m.Merge("playlist", "first", "First", "", entries)
	e.m.Merge("playlist", "second", "Second", "", entries)
	if _, err := e.DeletePermanently(t.Context(), []LibraryTrack{deleteFixtureRow(t, e, "a")}); err != nil {
		t.Fatal(err)
	}
	// A different file has taken the old path. Re-adding must not claim it.
	if err := os.WriteFile(filepath.Join(e.Root(), "a.opus"), []byte("different audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	var downloaded []string
	e.trackWork = func(_ context.Context, id string, _ func(Event)) bool {
		downloaded = append(downloaded, id)
		if e.m.Tracks[id].Path != "" {
			t.Error("old unverified path retained")
		}
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", "a", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(downloaded, []string{"a"}) {
		t.Fatalf("downloads = %v", downloaded)
	}
	opened, err := manifest.Load(e.Root())
	if err != nil {
		t.Fatal(err)
	}
	if got := opened.Source("first").Tracks; !slices.Contains(got, "a") {
		t.Fatalf("first lost restored entry on load: %v", got)
	}
	if got := opened.Merge("playlist", "first", "First", "", entries[1:]).Tracks; !slices.Contains(got, "a") {
		t.Fatalf("refresh lost explicit membership: %v", got)
	}
	if got := opened.Merge("playlist", "second", "Second", "", entries).Tracks; !slices.Equal(got, []string{"pending"}) {
		t.Fatalf("second silently reattached song: %v", got)
	}
	if _, err := e.DeletePermanently(t.Context(), []LibraryTrack{{ID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if got := e.Collection("first").Tracks; len(got) != 1 || got[0].ID != "pending" {
		t.Fatalf("second delete left the re-added membership: %+v", got)
	}
}

func TestReaddSaveFailureKeepsTrackRemoved(t *testing.T) {
	e := libraryFixture(t)
	e.m.Merge("playlist", "first", "First", "", []manifest.Entry{{ID: "a"}})
	if _, err := e.ExcludeSourceTracks("first", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	e.m.Tracks["a"].Status = manifest.StatusRemoved
	e.m.Root = filepath.Join(e.Root(), "missing")
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Error("download started before the catalog was saved")
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", "a", func(Event) {}); err == nil {
		t.Fatal("expected catalog write failure")
	}
	s := e.m.Source("first")
	if len(s.Tracks) != 0 || !s.Excluded["a"] || s.Readded["a"] || e.m.Tracks["a"].Status != manifest.StatusRemoved {
		t.Fatalf("save failure changed catalog: %+v, %+v", s, e.m.Tracks["a"])
	}
}

func TestReaddStandaloneSourceDoesNotOfferOtherTracks(t *testing.T) {
	e := libraryFixture(t)
	e.m.Merge("track", "a", "A", "", []manifest.Entry{{ID: "a"}})
	if _, err := e.DeletePermanently(t.Context(), []LibraryTrack{
		deleteFixtureRow(t, e, "a"), deleteFixtureRow(t, e, "b"),
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.RestorableTracks("a"); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("standalone candidates = %+v", got)
	}
}

func TestReaddDoesNotRestoreSharedTrashInOtherLists(t *testing.T) {
	e := libraryFixture(t)
	entries := []manifest.Entry{{ID: "a"}}
	e.m.Merge("playlist", "first", "First", "", entries)
	e.m.Merge("playlist", "second", "Second", "", entries)
	if _, err := e.TrashFiles(t.Context(), []LibraryTrack{deleteFixtureRow(t, e, "a")}); err != nil {
		t.Fatal(err)
	}
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Error("attempted to download shared trash")
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", "a", func(Event) {}); err == nil {
		t.Fatal("re-adding shared trash should not change another list")
	}
	if got := e.Collection("second").Tracks; len(got) != 1 || got[0].Status != manifest.StatusRemoved {
		t.Fatalf("second = %+v", got)
	}
}

func TestReaddedTrackCanBeTrashedAndRestoredNormally(t *testing.T) {
	e := libraryFixture(t)
	e.m.Merge("playlist", "first", "First", "", []manifest.Entry{{ID: "a"}})
	if _, err := e.DeletePermanently(t.Context(), []LibraryTrack{deleteFixtureRow(t, e, "a")}); err != nil {
		t.Fatal(err)
	}
	e.trackWork = func(_ context.Context, id string, _ func(Event)) bool {
		if err := os.WriteFile(filepath.Join(e.Root(), "a.opus"), []byte("re-downloaded"), 0o600); err != nil {
			t.Error(err)
		}
		e.m.Tracks[id].Path, e.m.Tracks[id].Status = "a.opus", manifest.StatusDone
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", "a", func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.TrashFiles(t.Context(), []LibraryTrack{deleteFixtureRow(t, e, "a")}); err != nil {
		t.Fatal(err)
	}
	e, err := Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Collection("first").Tracks; len(got) != 1 || got[0].Status != manifest.StatusRemoved {
		t.Fatalf("reopening lost the ordinary trashed entry: %+v", got)
	}
	if report, err := e.RestoreTrash(t.Context()); err != nil || report.Restored != 1 {
		t.Fatalf("new trash restore: %+v, %v", report, err)
	}
}

func TestReaddRelinksMovedAudioBeforeDownload(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	e := libraryFixture(t)
	const id = "dQw4w9WgXcQ"
	e.m.Merge("playlist", "first", "First", "", []manifest.Entry{{ID: id}})
	e.m.Tracks[id].Status, e.m.Tracks[id].Path = manifest.StatusDone, "original.opus"
	if _, err := e.ExcludeSourceTracks("first", []string{id}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Root(), "renamed.opus")
	if output, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc", "-t", "0.1", "-metadata",
		"purl=https://music.youtube.com/watch?v="+id, path).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, output)
	}
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Error("download attempted despite a matching local file")
		return true
	}
	if err := e.ReaddTrack(t.Context(), "first", id, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if got := e.Collection("first").Tracks; len(got) != 1 || got[0].Path != "renamed.opus" || !got[0].Synced {
		t.Fatalf("collection = %+v", got)
	}
}
