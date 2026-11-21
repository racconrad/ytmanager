package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ytmanager/internal/manifest"
)

func TestExternalPlaylistRoutesTrackToItsProviderAndPersistsURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp uses a POSIX shell")
	}
	bin := t.TempDir()
	trackURL := "https://soundcloud.com/artist/song"
	script := `#!/bin/sh
case " $* " in
*' --flat-playlist '*) printf '%s\n' '{"_type":"playlist","id":"42","title":"Set","entries":[{"id":"42","url":"` + trackURL + `","title":"Song"}]}' ;;
*) printf 'ERROR: requested %s\n' "$*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	e, err := Open(t.TempDir(), Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	e.resolveSong = func(context.Context, string) (string, error) {
		t.Error("external track went through YouTube Music")
		return "", nil
	}
	if err := e.Download(context.Background(), []string{"https://soundcloud.com/artist/sets/set"}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	saved, err := manifest.Load(e.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Sources) != 1 || len(saved.Sources[0].Tracks) != 1 {
		t.Fatalf("source not saved: %+v", saved.Sources)
	}
	id := saved.Sources[0].Tracks[0]
	track := saved.Tracks[id]
	if id == saved.Sources[0].ID || track.MediaURL != trackURL || !strings.Contains(track.Error, "requested") || !strings.Contains(track.Error, trackURL) {
		t.Fatalf("provider URL not used or saved: %q %+v", id, track)
	}
}
