//go:build linux || darwin

package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelDownloadStopsItsDetachedWriter(t *testing.T) {
	testDownloadTemp(t)
	dst := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	release := filepath.Join(t.TempDir(), "release")
	wrote := filepath.Join(t.TempDir(), "wrote")
	t.Setenv("YTM_TEST_RELEASE_WRITER", release)
	t.Setenv("YTM_TEST_WRITER_WROTE", wrote)
	fixture := `#!/bin/sh
printf partial > Song.opus.part
(
  tries=0
  while [ ! -e "$YTM_TEST_RELEASE_WRITER" ]; do
    [ "$tries" -lt 100 ] || exit 1
    tries=$((tries + 1))
    sleep 0.01
  done
  printf bad >> Song.opus.part
  printf wrote > "$YTM_TEST_WRITER_WROTE"
) </dev/null >/dev/null 2>&1 &
echo 'PROG 1 2'
exec sleep 30
`
	writeStageFixture(t, filepath.Join(bin, "yt-dlp"), fixture, 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Download(ctx, "cancel", "unused.json", dst, "opus", "", "", func(float64) { cancel() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download error = %v", err)
	}
	writeStageFixture(t, release, "", 0600)
	deadline := time.Now().Add(350 * time.Millisecond)
	for time.Now().Before(deadline) {
		requireStageAbsent(t, wrote)
		time.Sleep(10 * time.Millisecond)
	}
	partial := filepath.Join(stageDir(dst), stageKey("cancel", "opus", ""), "Song.opus.part")
	data, err := os.ReadFile(partial)
	if err != nil || string(data) != "partial" {
		t.Fatalf("cancelled stage changed: %q, %v", data, err)
	}
}
