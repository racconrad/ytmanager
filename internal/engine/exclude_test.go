package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

func TestExcludeSourceTracksPreservesAudioAndOtherLists(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://music.youtube.com/playlist?list=selected"
	entries := []manifest.Entry{{ID: "keep"}, {ID: "shared"}, {ID: "keep"}, {ID: "pending"}, {ID: "shared"}}
	e.m.Merge("playlist", "selected", "Selected", url, entries)
	e.m.Merge("playlist", "other", "Other", "other", []manifest.Entry{{ID: "shared"}})
	for _, id := range []string{"keep", "shared"} {
		path := id + ".opus"
		if err := os.WriteFile(filepath.Join(e.Root(), path), []byte(id+" audio"), 0o600); err != nil {
			t.Fatal(err)
		}
		e.m.Tracks[id].Path, e.m.Tracks[id].Status = path, manifest.StatusDone
	}
	if err := e.m.Save(); err != nil {
		t.Fatal(err)
	}
	if err := e.ExportM3U(t.Context(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.ExcludeSourceTracks("selected", []string{"shared", "pending", "shared", "unknown"}); n != 2 || err != nil {
		t.Fatalf("excluded %d, err = %v", n, err)
	}
	if n, err := e.ExcludeSourceTracks("selected", []string{"shared"}); n != 0 || err != nil {
		t.Fatalf("repeated exclusion: excluded %d, err = %v", n, err)
	}
	check := func(e *Engine) {
		t.Helper()
		var ids []string
		for _, tr := range e.Collection("selected").Tracks {
			ids = append(ids, tr.ID)
		}
		if !slices.Equal(ids, []string{"keep", "keep"}) {
			t.Errorf("selected order = %v, want [keep keep]", ids)
		}
		if got := e.Collection("other").Tracks; len(got) != 1 || got[0].ID != "shared" || !got[0].Synced {
			t.Errorf("other collection = %+v", got)
		}
		if got := e.Snapshot()[0]; got.Total != 1 || got.Done != 1 || got.Pending != 0 {
			t.Errorf("selected counts = %+v", got)
		}
		if got := e.Health(); got.Pending != 0 || got.Done != 2 {
			t.Errorf("library counts = %+v", got)
		}
		if got, ok := manifest.Summarize(e.Root()); !ok || got.Pending != 0 || got.Saved != 2 {
			t.Errorf("destination counts = %+v, loaded = %v", got, ok)
		}
		for _, ext := range []string{".m3u8", ".m3u"} {
			selected, err := os.ReadFile(filepath.Join(e.Root(), "Selected"+ext))
			if err != nil || !strings.Contains(string(selected), "keep.opus") || strings.Contains(string(selected), "shared.opus") {
				t.Errorf("selected %s = %q, err = %v", ext, selected, err)
			}
			other, err := os.ReadFile(filepath.Join(e.Root(), "Other"+ext))
			if err != nil || !strings.Contains(string(other), "shared.opus") {
				t.Errorf("other %s = %q, err = %v", ext, other, err)
			}
		}
		if audio, err := os.ReadFile(filepath.Join(e.Root(), "shared.opus")); err != nil || string(audio) != "shared audio" {
			t.Errorf("shared audio = %q, err = %v", audio, err)
		}
	}
	check(e)
	e, err = Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.listSource = func(context.Context, string, string) (*ytdlp.Listing, error) {
		return &ytdlp.Listing{Type: "playlist", ID: "selected", Title: "Selected", Entries: []ytdlp.Entry{
			{ID: "shared"}, {ID: "keep"}, {ID: "pending"}, {ID: "keep"},
		}}, nil
	}
	e.trackWork = func(_ context.Context, id string, _ func(Event)) bool {
		t.Errorf("scheduled excluded track %s", id)
		return true
	}
	if err := e.Sync(t.Context(), []string{url}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	check(e)
	if e.HasPending() {
		t.Error("excluded track remains in the pending queue")
	}
}

func TestExcludeSourceTracksKeepsIndependentPlaylistAfterEmptyingSource(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const url, custom = "https://music.youtube.com/playlist?list=selected", "#EXTM3U\n"
	e.m.Merge("playlist", "selected", "Selected", url, []manifest.Entry{{ID: "only"}})
	e.m.Tracks["only"].Path, e.m.Tracks["only"].Status = "only.opus", manifest.StatusDone
	customPath := filepath.Join(e.Root(), "Selected.m3u8")
	if err := os.WriteFile(customPath, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.m.WritePlaylists(); err != nil {
		t.Fatal(err)
	}
	if n, err := e.ExcludeSourceTracks("selected", []string{"only"}); n != 1 || err != nil {
		t.Fatalf("excluded %d, err = %v", n, err)
	}
	if e.HasPending() {
		t.Error("source with no active tracks remains pending")
	}
	e.listSource = func(context.Context, string, string) (*ytdlp.Listing, error) {
		t.Error("non-refresh download re-read the excluded source")
		return &ytdlp.Listing{Type: "playlist", ID: "selected", Title: "Selected", Entries: []ytdlp.Entry{{ID: "only"}}}, nil
	}
	if err := e.Download(t.Context(), []string{url}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(customPath); err != nil || string(got) != custom {
		t.Errorf("custom playlist = %q, err = %v", got, err)
	}
	ownedPath := filepath.Join(e.Root(), "Selected [selected-1].m3u8")
	if got, err := os.ReadFile(ownedPath); err != nil || string(got) != "#EXTM3U\n# ytmanager source: selected\n" {
		t.Errorf("empty owned playlist = %q, err = %v", got, err)
	}
}

func TestExcludeSourceTracksLeavesMembershipOnBlockedMutation(t *testing.T) {
	for _, blocked := range []string{"active operation", "manifest write failure"} {
		t.Run(blocked, func(t *testing.T) {
			e, err := Open(t.TempDir(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			e.m.Merge("mix", "selected", "Selected", "url", []manifest.Entry{{ID: "song"}})
			if blocked == "active operation" {
				e.running = true
			} else if err := os.Mkdir(filepath.Join(e.Root(), manifest.FileName+".tmp"), 0o700); err != nil {
				t.Fatal(err)
			}
			if n, err := e.ExcludeSourceTracks("selected", []string{"song"}); n != 0 || err == nil {
				t.Fatalf("excluded %d, err = %v", n, err)
			}
			if got := e.Collection("selected").Tracks; len(got) != 1 || got[0].ID != "song" {
				t.Errorf("membership changed despite error: %+v", got)
			}
		})
	}
}
