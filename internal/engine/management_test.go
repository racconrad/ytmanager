package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ytmanager/internal/manifest"
)

func writeTestAudio(t *testing.T, root, name, id string) string {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "libopus", "-metadata", "title=Song", "-metadata", "artist=Artist", "-metadata", "album_artist=Artist", "-metadata", "album=Album", "-metadata", "track=1", "-metadata", "purl=https://www.youtube.com/watch?v=" + id, path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("audio fixture: %s %v", out, err)
	}
	return path
}

func TestInventoryIncludesMissingAndProtectsLocalPlaylists(t *testing.T) {
	e := libraryFixture(t)
	if err := os.Remove(filepath.Join(e.Root(), "a.opus")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(e.Root(), "external.opus"), []byte("external"), 0600)
	os.WriteFile(filepath.Join(e.Root(), "unlisted.opus"), []byte("unlisted"), 0600)
	os.WriteFile(filepath.Join(e.Root(), "custom.m3u8"), []byte("\ufeff#EXTM3U\nexternal.opus\n"), 0600)
	os.Mkdir(filepath.Join(e.Root(), trashDir), 0700)
	os.WriteFile(filepath.Join(e.Root(), trashDir, "hidden.opus"), []byte("hidden"), 0600)
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]LibraryTrack{}
	for _, r := range rows {
		byPath[r.Path] = r
	}
	if len(rows) != 5 || !byPath["a.opus"].Missing || byPath["external.opus"].Orphan || !byPath["unlisted.opus"].Orphan {
		t.Fatalf("bad inventory: %+v", rows)
	}
	if !e.HasPending() || e.Snapshot()[0].Done != 2 {
		t.Fatal("missing physical file still settled")
	}
	calls := map[string]bool{}
	e.trackWork = func(_ context.Context, id string, _ func(Event)) bool {
		calls[id] = true
		e.mu.Lock()
		e.m.Tracks[id].Status = manifest.StatusUnavailable
		e.mu.Unlock()
		return true
	}
	if err = e.Download(context.Background(), nil, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !calls["a"] || calls["b"] || calls["c"] {
		t.Fatalf("resume work: %v", calls)
	}
}

func TestTrashExternalRestoreAcrossRestartAndEmpty(t *testing.T) {
	e := libraryFixture(t)
	external := LibraryTrack{ID: "file:external.opus", Path: "external.opus", Title: "External", Unmanaged: true}
	os.WriteFile(filepath.Join(e.Root(), external.Path), []byte("external"), 0600)
	os.WriteFile(filepath.Join(e.Root(), "external.lrc"), []byte("lyrics"), 0600)
	report, err := e.TrashFiles(context.Background(), []LibraryTrack{external})
	if err != nil {
		t.Fatal(err)
	}
	if report.Deleted != 2 || report.Missing != 0 || report.Bytes != 14 {
		t.Fatalf("bad report: %+v", report)
	}
	if _, err = os.Stat(filepath.Join(e.Root(), external.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file remained")
	}
	reopened, err := Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	report, err = reopened.RestoreTrash(context.Background())
	if err != nil || report.Restored != 2 {
		t.Fatalf("restore: %+v %v", report, err)
	}
	for _, p := range []string{external.Path, "external.lrc"} {
		if _, err = os.Stat(filepath.Join(e.Root(), p)); err != nil {
			t.Fatal(err)
		}
	}
	report, err = reopened.TrashFiles(context.Background(), []LibraryTrack{external})
	if err != nil {
		t.Fatal(err)
	}
	report, err = reopened.EmptyTrash(context.Background())
	if err != nil || report.Deleted != 2 || !report.Permanent {
		t.Fatalf("purge: %+v %v", report, err)
	}
	report, err = reopened.RestoreTrash(context.Background())
	if err != nil || report.Restored != 0 {
		t.Fatalf("purged file restored: %+v %v", report, err)
	}
}

func TestTrashReportsAbsentAndRefusesChangedOrSharedFiles(t *testing.T) {
	e := libraryFixture(t)
	rows := e.Library()
	var a LibraryTrack
	for _, r := range rows {
		if r.ID == "a" {
			a = r
		}
	}
	os.WriteFile(filepath.Join(e.Root(), a.Path), []byte("different content"), 0600)
	report, err := e.TrashFiles(context.Background(), []LibraryTrack{a})
	if err == nil || report.Failed != 1 {
		t.Fatalf("changed file removed: %+v %v", report, err)
	}
	os.Remove(filepath.Join(e.Root(), a.Path))
	report, err = e.TrashFiles(context.Background(), []LibraryTrack{{ID: "a", Path: a.Path}})
	if err != nil || report.Deleted != 0 || report.Missing != 1 {
		t.Fatalf("absent report: %+v %v", report, err)
	}
	e.m.Tracks["b"].Path = "c.opus"
	if _, err = e.TrashFiles(context.Background(), []LibraryTrack{{ID: "c", Path: "c.opus"}}); err == nil {
		t.Fatal("unselected shared path removed")
	}
}

func TestCleanupRevalidatesMembershipAndPreservesCustomPlaylists(t *testing.T) {
	e := libraryFixture(t)
	os.WriteFile(filepath.Join(e.Root(), "orphan.opus"), []byte("audio"), 0600)
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var selected []LibraryTrack
	for _, r := range rows {
		if r.Path == "orphan.opus" {
			selected = append(selected, r)
		}
	}
	os.WriteFile(filepath.Join(e.Root(), "new.m3u"), []byte("#EXTM3U\norphan.opus\n"), 0600)
	if _, err = e.CleanOrphans(context.Background(), selected); err == nil {
		t.Fatal("deleted newly referenced file")
	}
	if err = e.m.WritePlaylists(); err != nil {
		t.Fatal(err)
	}
	if err = e.Remove("album"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(e.Root(), "Album.m3u8")); !os.IsNotExist(err) {
		t.Fatal("stale generated playlist remained")
	}
	if _, err = os.Stat(filepath.Join(e.Root(), "new.m3u")); err != nil {
		t.Fatal("custom playlist removed")
	}
	if _, err = os.Stat(filepath.Join(e.Root(), "a.opus")); err != nil {
		t.Fatal("source removal deleted music")
	}
}

func TestRepairFindsRenamedAudioAndRefusesAmbiguousIDs(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "unique", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			e, err := Open(t.TempDir(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			e.m.Merge("album", "album", "Album", "url", []manifest.Entry{{ID: "song", Title: "Song"}})
			e.m.Tracks["song"].Path, e.m.Tracks["song"].Status = "old.opus", manifest.StatusDone
			writeTestAudio(t, e.Root(), "new/renamed.opus", "song")
			if ambiguous {
				writeTestAudio(t, e.Root(), "other.opus", "song")
			}
			if err = e.m.Save(); err != nil {
				t.Fatal(err)
			}
			if err = e.Repair(context.Background(), func(Event) {}); err != nil {
				t.Fatal(err)
			}
			if ambiguous {
				if e.m.Tracks["song"].Path != "old.opus" {
					t.Fatal("ambiguous identity recovered")
				}
			} else if e.m.Tracks["song"].Path != filepath.Join("new", "renamed.opus") || e.m.Tracks["song"].Status != manifest.StatusDone {
				t.Fatalf("not relinked: %+v", e.m.Tracks["song"])
			}
		})
	}
}

func TestConvertAndOrganizePreviewUndoAndConflict(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.m.Merge("album", "album", "Album", "url", []manifest.Entry{{ID: "song", Title: "Song"}})
	original := writeTestAudio(t, e.Root(), "original.opus", "song")
	originalBytes, _ := os.ReadFile(original)
	e.m.Tracks["song"].Path, e.m.Tracks["song"].Status = "original.opus", manifest.StatusDone
	if err = e.m.Save(); err != nil {
		t.Fatal(err)
	}
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.PlanLibrary(context.Background(), rows, "mp3", "")
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("preview %+v %v", plan, err)
	}
	if !plan.Changes[0].Convert || !strings.HasSuffix(plan.Changes[0].To, filepath.Join("Artist", "Album", "01 - Song.mp3")) {
		t.Fatalf("wrong destination: %+v", plan)
	}
	if _, err = os.Stat(original); err != nil {
		t.Fatal("preview mutated original")
	}
	report, err := e.ApplyLibraryPlan(context.Background(), plan)
	if err != nil || report.Changed != 1 {
		t.Fatalf("apply: %+v %v", report, err)
	}
	if _, err = os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("original was not backed up")
	}
	if e.m.Tracks["song"].Path != plan.Changes[0].To {
		t.Fatal("new path not saved")
	}
	report, err = e.RestoreTrash(context.Background())
	if err != nil || report.Restored != 1 {
		t.Fatalf("undo conversion: %+v %v", report, err)
	}
	restored, _ := os.ReadFile(original)
	if string(restored) != string(originalBytes) || e.m.Tracks["song"].Path != "original.opus" {
		t.Fatal("original audio/state not restored")
	}
	rows, err = e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err = e.PlanLibrary(context.Background(), rows, "", "artist")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Convert {
		t.Fatalf("folder-only plan: %+v", plan)
	}
	os.MkdirAll(filepath.Dir(filepath.Join(e.Root(), plan.Changes[0].To)), 0755)
	os.WriteFile(filepath.Join(e.Root(), plan.Changes[0].To), []byte("keep me"), 0600)
	if _, err = e.ApplyLibraryPlan(context.Background(), plan); err == nil {
		t.Fatal("new collision overwritten")
	}
	b, _ := os.ReadFile(filepath.Join(e.Root(), plan.Changes[0].To))
	if string(b) != "keep me" {
		t.Fatal("collision changed")
	}
}

func TestRefreshQueuesInvalidButNotToolFailures(t *testing.T) {
	e := libraryFixture(t)
	rows, err := e.RefreshLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.Invalid || e.m.Tracks[r.ID].Status != manifest.StatusPending {
			t.Fatalf("corrupt audio not scheduled: %+v", r)
		}
	}
	for _, tr := range e.m.Tracks {
		tr.Status = manifest.StatusDone
	}
	t.Setenv("PATH", t.TempDir())
	if _, err = e.RefreshLibrary(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tr := range e.m.Tracks {
		if tr.Status != manifest.StatusDone {
			t.Fatal("missing ffprobe scheduled a redownload")
		}
	}
}

func TestRestoreRefusesConflictsAndTamperedTrash(t *testing.T) {
	for _, kind := range []string{"conflict", "modified-trash"} {
		t.Run(kind, func(t *testing.T) {
			e := libraryFixture(t)
			report, err := e.TrashFiles(context.Background(), []LibraryTrack{{ID: "a", Path: "a.opus"}})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "conflict" {
				os.WriteFile(filepath.Join(e.Root(), "a.opus"), []byte("keep new file"), 0600)
			} else {
				os.WriteFile(filepath.Join(e.Root(), trashDir, report.Batch, "files", "a.opus"), []byte("changed"), 0600)
			}
			restored, err := e.RestoreTrash(context.Background())
			if err == nil || restored.Failed != 1 || restored.Restored != 0 {
				t.Fatalf("unsafe restore: %+v %v", restored, err)
			}
			if kind == "conflict" {
				b, _ := os.ReadFile(filepath.Join(e.Root(), "a.opus"))
				if string(b) != "keep new file" {
					t.Fatal("conflict overwritten")
				}
			}
			if e.m.Tracks["a"].Status != manifest.StatusRemoved {
				t.Fatal("failed restore changed status")
			}
		})
	}
}

func TestFolderMoveAndUndoPreserveAudio(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	original := writeTestAudio(t, e.Root(), "loose.opus", "song")
	before, _ := os.ReadFile(original)
	os.WriteFile(filepath.Join(e.Root(), "loose.lrc"), []byte("lyrics"), 0600)
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.PlanLibrary(context.Background(), rows, "", "artist")
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	report, err := e.ApplyLibraryPlan(context.Background(), plan)
	if err != nil || report.Changed != 1 {
		t.Fatalf("move: %+v %v", report, err)
	}
	after, _ := os.ReadFile(filepath.Join(e.Root(), plan.Changes[0].To))
	if string(before) != string(after) {
		t.Fatal("folder move changed audio")
	}
	reopened, err := Open(e.Root(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	report, err = reopened.RestoreTrash(context.Background())
	if err != nil || report.Restored != 2 {
		t.Fatalf("undo move: %+v %v", report, err)
	}
	after, _ = os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("undo move lost audio")
	}
	if b, _ := os.ReadFile(filepath.Join(e.Root(), "loose.lrc")); string(b) != "lyrics" {
		t.Fatal("undo move lost lyrics")
	}
}

func TestSyncReusesExternalPhysicalAudio(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestAudio(t, e.Root(), "already-here/song.opus", "song")
	e.m.Merge("album", "album", "Album", "url", []manifest.Entry{{ID: "song", Title: "Song"}})
	e.trackWork = func(context.Context, string, func(Event)) bool {
		t.Error("existing audio scheduled for download")
		return true
	}
	if err = e.Download(context.Background(), nil, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if tr := e.m.Tracks["song"]; tr.Status != manifest.StatusDone || tr.Path != filepath.Join("already-here", "song.opus") {
		t.Fatalf("physical file not adopted: %+v", tr)
	}
}

func TestCancelledCopyPreservesSourceAndVerifiedMTPFallback(t *testing.T) {
	path := t.TempDir()
	os.WriteFile(filepath.Join(path, "from.opus"), []byte("original"), 0600)
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = moveRoot(ctx, root, "from.opus", "target.opus", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("copy cancellation: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(path, "from.opus")); string(b) != "original" {
		t.Fatal("cancel lost original")
	}
	if err = moveRoot(context.Background(), root, "from.opus", "target.opus", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(path, "target.opus")); string(b) != "original" {
		t.Fatal("copy differs")
	}
	if _, err = os.Stat(filepath.Join(path, "from.opus")); !os.IsNotExist(err) {
		t.Fatal("verified copy did not remove old path")
	}
}

func TestDetectActualCodecAndCrossFormatCandidates(t *testing.T) {
	e, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	input := writeTestAudio(t, e.Root(), "source.opus", "song")
	b, _ := os.ReadFile(input)
	os.WriteFile(filepath.Join(e.Root(), "misleading.mp3"), b, 0600)
	rows, err := e.ScanLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: %v", rows)
	}
	for _, r := range rows {
		if !r.Duplicate || r.Codec != "opus" || r.Duration == 0 {
			t.Fatalf("bad inspection: %+v", r)
		}
		if r.Path == "misleading.mp3" && !r.FormatMismatch {
			t.Fatal("trusted extension instead of actual codec")
		}
	}
}

func TestResetTrashesFilesAndQueuesCleanDownload(t *testing.T) {
	e := libraryFixture(t)
	report, err := e.ResetTracks(context.Background(), e.Library())
	if err != nil || report.Deleted != 3 {
		t.Fatalf("reset: %+v %v", report, err)
	}
	for id, tr := range e.m.Tracks {
		if tr.Status != manifest.StatusPending {
			t.Fatalf("%s not queued: %s", id, tr.Status)
		}
		if _, err := os.Stat(filepath.Join(e.Root(), tr.Path)); !os.IsNotExist(err) {
			t.Fatalf("%s still on disk", id)
		}
	}
	if !e.HasPending() {
		t.Fatal("sync would not download reset tracks")
	}
}
