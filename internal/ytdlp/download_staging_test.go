package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"ytmanager/internal/device"
)

func TestDownloadKeepsTransientPartialForRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	temp := testDownloadTemp(t)
	dest := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ytDLP := filepath.Join(bin, "yt-dlp")
	writeStageFixture(t, ytDLP, "#!/bin/sh\nprintf partial > Song.opus.part\necho 'ERROR: connection timed out' >&2\nexit 1\n", 0700)
	ctx := context.Background()
	if _, err := Download(ctx, "retry", "unused.json", dest, "opus", "", "", func(float64) {}); !Transient(err) {
		t.Fatalf("download error = %v", err)
	}
	writeStageFixture(t, ytDLP, "#!/bin/sh\nmv Song.opus.part Song.opus || exit 1\necho 'FILE Song.opus'\n", 0700)
	path, err := Download(ctx, "retry", "unused.json", dest, "opus", "", "", func(float64) {})
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(data) != "partial" {
		t.Fatalf("could not reuse partial in %s: download %v, read %v, data %q", temp, err, readErr, data)
	}
}

func TestDownloadOnlyDiscardsTerminalStages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, tc := range []struct {
		name, process string
		cancel, keep  bool
		wantError     func(error) bool
	}{
		{"pause", "echo 'PROG 1 2'\nexec sleep 30\n", true, true, func(err error) bool { return errors.Is(err, context.Canceled) }},
		{"full disk", "echo 'ERROR: No space left on device' >&2\nexit 1\n", false, true, device.IsSpaceError},
		{"uncertain exit", "kill -9 $$\n", false, true, func(err error) bool { return err != nil }},
		{"unavailable", "echo 'ERROR: This video is not available' >&2\nexit 1\n", false, false, Unavailable},
		{"copied", "mv Song.opus.part Song.opus\necho 'FILE Song.opus'\n", false, false, func(err error) bool { return err == nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testDownloadTemp(t)
			dst := t.TempDir()
			bin := t.TempDir()
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			writeStageFixture(t, filepath.Join(bin, "yt-dlp"), "#!/bin/sh\nprintf partial > Song.opus.part\n"+tc.process, 0700)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := Download(ctx, "same-ID", "unused.json", dst, "opus", "", "", func(float64) {
				if tc.cancel {
					cancel()
				}
			})
			if !tc.wantError(err) {
				t.Fatalf("unexpected download error: %v", err)
			}
			if n, err := CleanupAbandonedStages(context.Background(), dst); n != 0 || err != nil {
				t.Fatalf("unexpected cleanup: %d, %v", n, err)
			}
			stage := filepath.Join(stageDir(dst), stageKey("same-ID", "opus", ""))
			if !tc.keep {
				requireStageAbsent(t, stage)
				return
			}
			data, err := os.ReadFile(filepath.Join(stage, "Song.opus.part"))
			if err != nil || string(data) != "partial" {
				t.Fatalf("partial was not retained: %q, %v", data, err)
			}
			if tc.name == "pause" || tc.name == "full disk" {
				writeStageFixture(t, filepath.Join(bin, "yt-dlp"), "#!/bin/sh\nmv Song.opus.part Song.opus || exit 1\necho 'FILE Song.opus'\n", 0700)
				path, err := Download(context.Background(), "same-ID", "unused.json", dst, "opus", "", "", func(float64) {})
				data, readErr := os.ReadFile(path)
				if err != nil || readErr != nil || string(data) != "partial" {
					t.Fatalf("could not resume the partial: %v, %v, %q", err, readErr, data)
				}
			}
		})
	}
}

func TestDownloadDoesNotReuseAStageFromAnUnconfirmedExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	testDownloadTemp(t)
	dst := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	key := stageKey("retry", "opus", "")
	base := stageDir(dst)
	oldFile := filepath.Join(base, key, "Song.opus.part")
	writeStageFixture(t, oldFile, "unfinished", 0600)
	writeStageFixture(t, filepath.Join(base, key+".running"), "", 0600)
	writeStageFixture(t, filepath.Join(bin, "yt-dlp"), "#!/bin/sh\nif test -e Song.opus.part; then exit 1; fi\nprintf fresh > Song.opus\necho 'FILE Song.opus'\n", 0700)
	path, err := Download(context.Background(), "retry", "unused.json", dst, "opus", "", "", func(float64) {})
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(data) != "fresh" {
		t.Fatalf("could not start a separate attempt: %v, %v, %q", err, readErr, data)
	}
	data, err = os.ReadFile(oldFile)
	if err != nil || string(data) != "unfinished" {
		t.Fatalf("unconfirmed work was modified: %q, %v", data, err)
	}
}

func testDownloadTemp(t *testing.T) string {
	t.Helper()
	temp := t.TempDir()
	for _, env := range []string{"TMPDIR", "TEMP", "TMP"} {
		t.Setenv(env, temp)
	}
	return temp
}

func writeStageFixture(t *testing.T, name, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func requireStageAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists or cannot be checked: %v", path, err)
	}
}
