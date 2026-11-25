package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupAbandonedStagesOnlyRemovesMarkedStages(t *testing.T) {
	temp := testDownloadTemp(t)
	dst := t.TempDir()
	base := stageDir(dst)
	abandoned := strings.Repeat("a", 64)
	retained := strings.Repeat("b", 64)
	other := strings.Repeat("c", 64)
	unconfirmed := strings.Repeat("f", 64)
	writeStageFixture(t, filepath.Join(base, abandoned+".discard"), "", 0600)
	writeStageFixture(t, filepath.Join(base, abandoned, "track.part"), "abandoned", 0600)
	writeStageFixture(t, filepath.Join(base, unconfirmed+".discard"), "", 0600)
	writeStageFixture(t, filepath.Join(base, unconfirmed+".running"), "", 0600)
	keep := []string{
		filepath.Join(base, retained, "track.part"),
		filepath.Join(base, "custom.part"),
		filepath.Join(base, "unknown", "track.part"),
		filepath.Join(base, "unknown.discard"),
		filepath.Join(base, unconfirmed, "track.part"),
		filepath.Join(dst, "standalone.part"),
		filepath.Join(temp, "ytmanager", "legacy", "track.part"),
		filepath.Join(stageDir(t.TempDir()), other+".discard"),
		filepath.Join(dst, "Song.opus"),
	}
	for _, path := range keep {
		writeStageFixture(t, path, "keep", 0600)
		old := time.Now().AddDate(-1, 0, 0)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := CleanupAbandonedStages(context.Background(), dst)
	if err != nil || n != 1 {
		t.Fatalf("cleanup removed %d stages: %v", n, err)
	}
	requireStageAbsent(t, filepath.Join(base, abandoned))
	requireStageAbsent(t, filepath.Join(base, abandoned+".discard"))
	for _, path := range keep {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "keep" {
			t.Fatalf("cleanup changed %s: %q, %v", path, data, err)
		}
	}
}

func TestCleanupAbandonedStagesSkipsLink(t *testing.T) {
	testDownloadTemp(t)
	dst := t.TempDir()
	base := stageDir(dst)
	key := strings.Repeat("d", 64)
	track := filepath.Join(dst, "Song.opus")
	writeStageFixture(t, track, "keep", 0600)
	writeStageFixture(t, filepath.Join(base, key+".discard"), "", 0600)
	if err := os.Symlink(dst, filepath.Join(base, key)); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	n, err := CleanupAbandonedStages(context.Background(), dst)
	if n != 0 || err == nil {
		t.Fatalf("cleanup accepted a symlink: %d, %v", n, err)
	}
	data, err := os.ReadFile(track)
	if err != nil || string(data) != "keep" {
		t.Fatalf("cleanup changed the target of a symlink: %q, %v", data, err)
	}
}

func TestCleanupAbandonedStagesSkipsStageLockedByAnotherProcess(t *testing.T) {
	ctx := context.Background()
	if dst := os.Getenv("YTM_TEST_CLEANUP_DEST"); dst != "" {
		n, err := CleanupAbandonedStages(ctx, dst)
		if n != 0 || err != nil {
			t.Fatalf("cleanup did not skip a locked stage: %d, %v", n, err)
		}
		return
	}
	testDownloadTemp(t)
	dst := t.TempDir()
	key := strings.Repeat("e", 64)
	base := stageDir(dst)
	marker := filepath.Join(base, key+".discard")
	writeStageFixture(t, marker, "", 0600)
	partial := filepath.Join(base, key, "track.part")
	writeStageFixture(t, partial, "partial", 0600)
	staging, err := openStageRoot(dst, false)
	if err != nil {
		t.Fatal(err)
	}
	defer staging.Close()
	lock, err := openStageLock(staging, key)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if ok, err := tryStageLock(lock); !ok || err != nil {
		t.Fatalf("locking stage: %v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCleanupAbandonedStagesSkipsStageLockedByAnotherProcess$")
	child.Env = append(os.Environ(), "YTM_TEST_CLEANUP_DEST="+dst)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("cleanup subprocess: %v\n%s", err, out)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("locked partial was removed: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := CleanupAbandonedStages(ctx, dst); n != 1 || err != nil {
		t.Fatalf("cleanup after release: %d, %v", n, err)
	}
}
