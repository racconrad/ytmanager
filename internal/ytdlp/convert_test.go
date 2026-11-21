package ytdlp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConvertPreservesTagsAndArtwork(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "source.m4a")
	commands := [][]string{
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.3", "-c:a", "aac", "-metadata", "title=Test Song", "-metadata", "artist=Artist", "-metadata", "album=Album", "-metadata", "comment=https://www.youtube.com/watch?v=audio-id", filepath.Join(dir, "audio.m4a")},
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=16x16", "-frames:v", "1", filepath.Join(dir, "image.jpg")},
		{"-nostdin", "-v", "error", "-i", filepath.Join(dir, "audio.m4a"), "-i", filepath.Join(dir, "image.jpg"), "-map", "0:a", "-map", "1:v", "-c", "copy", "-disposition:v", "attached_pic", input},
	}
	for _, args := range commands {
		if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %s: %v", out, err)
		}
	}
	sourceInfo, err := Probe(context.Background(), input)
	if err != nil || !sourceInfo.Cover {
		t.Fatalf("source cover missing: %+v %v", sourceInfo, err)
	}
	for _, format := range []string{"opus", "m4a", "mp3"} {
		t.Run(format, func(t *testing.T) {
			output := filepath.Join(dir, "converted."+format)
			if err := Convert(context.Background(), input, output, format); err != nil {
				t.Fatal(err)
			}
			info, err := Probe(context.Background(), output)
			if err != nil {
				t.Fatal(err)
			}
			if info.Tags["title"] != "Test Song" || info.Tags["artist"] != "Artist" || info.Tags["album"] != "Album" || info.Duration < 0.25 || !info.Cover {
				t.Fatalf("lost media metadata: %+v", info)
			}
		})
	}
}
