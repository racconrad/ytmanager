package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

func TestSaveSourceMetadataDoesNotStartDownload(t *testing.T) {
	root := t.TempDir()
	eng, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	listing := &ytdlp.Listing{
		Type:    "album",
		ID:      "OLAK5uy_meta",
		Title:   "Homework",
		Creator: "Daft Punk",
		Year:    "1997",
		Cover:   "https://lh3.example.com/hw.jpg",
		Entries: []ytdlp.Entry{
			{ID: "tr1", Title: "Daftendirekt", Duration: 164},
			{ID: "tr2", Title: "Around the World", Duration: 429},
		},
	}
	id, err := eng.SaveSourceMetadata(listing, "https://music.youtube.com/playlist?list=OLAK5uy_meta")
	if err != nil {
		t.Fatal(err)
	}
	col := eng.Collection(id)
	if col.Title != "Homework" || col.Creator != "Daft Punk" || col.Year != "1997" || col.Cover != "https://lh3.example.com/hw.jpg" {
		t.Fatalf("collection metadata mismatch: %+v", col)
	}
	if len(col.Tracks) != 2 || col.Tracks[0].Status != manifest.StatusPending {
		t.Fatalf("tracks mismatch: %+v", col.Tracks)
	}
}

func TestRepairRestoresMissingAlbumCover(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	root := t.TempDir()
	albumDir := filepath.Join(root, "Daft Punk", "Discovery")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	relAudio := filepath.Join("Daft Punk", "Discovery", "01 - One More Time.mp3")
	fullAudio := filepath.Join(root, relAudio)
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono:d=0.3",
		"-f", "lavfi", "-i", "color=c=blue:s=64x64:d=0.3",
		"-map", "0:a:0", "-map", "1:v:0", "-c:a", "libmp3lame", "-c:v", "mjpeg",
		"-disposition:v:0", "attached_pic", "-metadata", "album=Discovery", "-shortest", fullAudio)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture failed: %s: %v", out, err)
	}
	m, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Merge("album", "OLAK5uy_disc", "Discovery", "https://music.youtube.com/playlist?list=OLAK5uy_disc", []manifest.Entry{{ID: "vid1", Title: "One More Time"}})
	m.Tracks["vid1"].Path = relAudio
	m.Tracks["vid1"].Status = manifest.StatusDone
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	eng, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Repair(context.Background(), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(albumDir, "cover.jpg")); err != nil || st.Size() == 0 {
		t.Fatalf("expected Repair to restore cover.jpg: err=%v", err)
	}
}
