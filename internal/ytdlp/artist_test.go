package ytdlp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsArtistURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://music.youtube.com/channel/UCkWQ0gDrqOCarmUKmppD7GQ", true},
		{"https://www.youtube.com/@daftpunk", true},
		{"https://youtube.com/c/SomeArtist/releases", true},
		{"https://music.youtube.com/playlist?list=OLAK5uy_abc", false},
		{"https://music.youtube.com/watch?v=abc123", false},
		{"https://soundcloud.com/artistname", true},
		{"https://soundcloud.com/artistname/albums", true},
		{"https://soundcloud.com/artistname/sets/my-album", false},
		{"https://soundcloud.com/artistname/some-track", false},
		{"https://artist.bandcamp.com", true},
		{"https://artist.bandcamp.com/music", true},
		{"https://artist.bandcamp.com/album/record", false},
		{"https://artist.bandcamp.com/track/song", false},
		{"https://example.com/artist", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := IsArtistURL(tt.url); got != tt.want {
				t.Fatalf("IsArtistURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseExternalArtist(t *testing.T) {
	bandcampJSON := []byte(`{
		"title": "Artist Name",
		"uploader": "Artist Name",
		"thumbnails": [{"url": "https://f4.bcbits.com/img/artist.jpg"}],
		"entries": [
			{"id": "1", "title": "First LP", "webpage_url": "https://artist.bandcamp.com/album/first-lp"},
			{"id": "2", "title": "Loose Track", "url": "https://artist.bandcamp.com/track/loose-track"}
		]
	}`)
	got, err := parseExternalArtist(bandcampJSON, "https://artist.bandcamp.com", "bandcamp")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Artist Name" || got.Cover != "https://f4.bcbits.com/img/artist.jpg" {
		t.Fatalf("artist header mismatch: %+v", got)
	}
	if len(got.Albums) != 1 || got.Albums[0].Title != "First LP" || got.Albums[0].URL != "https://artist.bandcamp.com/album/first-lp" {
		t.Fatalf("albums mismatch: %+v", got.Albums)
	}
	if len(got.Singles) != 1 || got.Singles[0].Title != "Loose Track" || got.Singles[0].URL != "https://artist.bandcamp.com/track/loose-track" {
		t.Fatalf("singles mismatch: %+v", got.Singles)
	}
}

func TestExtractAndEnsureAlbumCover(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	audio := filepath.Join(dir, "01 - Song.mp3")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono:d=0.3",
		"-f", "lavfi", "-i", "color=c=purple:s=64x64:d=0.3",
		"-map", "0:a:0", "-map", "1:v:0", "-c:a", "libmp3lame", "-c:v", "mjpeg",
		"-disposition:v:0", "attached_pic", "-shortest", audio)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture failed: %s: %v", out, err)
	}
	albumDir := filepath.Join(dir, "Artist", "Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dstAudio := filepath.Join(albumDir, "01 - Song.mp3")
	if err := CopyFile(audio, dstAudio); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAlbumCover(context.Background(), dstAudio, audio); err != nil {
		t.Fatalf("EnsureAlbumCover failed: %v", err)
	}
	coverPath := filepath.Join(albumDir, "cover.jpg")
	info, err := os.Stat(coverPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty cover.jpg: err=%v", err)
	}
	// Existing custom cover must never be overwritten.
	if err := os.WriteFile(coverPath, []byte("custom-cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAlbumCover(context.Background(), dstAudio, audio); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(coverPath)
	if string(got) != "custom-cover" {
		t.Fatalf("existing cover.jpg was overwritten: %q", got)
	}
}
