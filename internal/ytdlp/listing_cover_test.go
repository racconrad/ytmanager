package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBandcampAlbumGetsArtWithoutDownloading(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp uses a POSIX shell")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case " $* " in
*' --flat-playlist '*) printf '%s\n' '{"_type":"playlist","id":"ep","entries":[{"id":"42","title":"Song","url":"https://artist.bandcamp.com/track/song"}]}' ;;
*' --no-playlist '*) printf '%s\n' '{"id":"42","thumbnail":"https://f4.bcbits.com/img/example.jpg"}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	list, err := List(context.Background(), "https://artist.bandcamp.com/album/ep", "")
	if err != nil {
		t.Fatal(err)
	}
	if list.Type != "album" || list.Cover != "https://f4.bcbits.com/img/example.jpg" {
		t.Fatalf("album metadata: %+v", list)
	}
}
