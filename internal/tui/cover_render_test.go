package tui

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ytmanager/internal/ytdlp"
)

var _ interface {
	io.ReadWriteCloser
	Fd() uintptr
} = (*coverOverlay)(nil)

func TestKittyOverlayTransmitPlaceAndClear(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: 120, G: 60, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	ov := &coverOverlay{out: &buf, enabled: true}
	ov.setPlacement(&coverPlacement{key: "https://art.example/cover.jpg", img: img, row: 4, col: 42, cols: 28, rows: 14})
	if _, err := ov.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b_Ga=t,f=100,i=7741,q=2") || !strings.Contains(out, "\x1b[4;42H\x1b_Ga=p,i=7741,p=1,c=28,r=14,C=1,q=2\x1b\\") {
		t.Fatalf("missing kitty transmit/place sequences: %q", out)
	}
	// Second frame with same key and coords must NOT re-transmit PNG bytes, only place.
	buf.Reset()
	if _, err := ov.Write([]byte("frame2")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "a=t,") || !strings.Contains(buf.String(), "a=p,") {
		t.Fatalf("unexpected re-transmit on same cover key: %q", buf.String())
	}
	// Clearing deletes all placements BEFORE writing frame3.
	buf.Reset()
	ov.setPlacement(nil)
	if _, err := ov.Write([]byte("frame3")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	delIdx := strings.Index(got, kittyDeleteAllSeq())
	frameIdx := strings.Index(got, "frame3")
	if delIdx < 0 || frameIdx < delIdx {
		t.Fatalf("expected delete sequence before frame3: %q", got)
	}
}

func TestViewSetsAndClearsKittyOverlayAcrossScreens(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	ov := &coverOverlay{out: &bytes.Buffer{}, enabled: true}
	m := newModel()
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.overlay = ov
	m.width, m.height = 100, 30
	m.screen = screenCollection
	m.collection = collectionState{previewURL: "u"}
	m.storeCover(coverLoadedMsg{key: "https://art.example/c.jpg", img: img})
	m.collection.preview = nil
	// Render screenSources (no cover) -> overlay cleared
	m.screen = screenSources
	_ = m.View()
	if ov.want.key != "" {
		t.Fatalf("expected empty overlay on screenSources, got %+v", ov.want)
	}
	// Render screenArtist with cover -> overlay set within terminal bounds
	m.screen = screenArtist
	m.artist = artistState{
		marked: map[string]bool{},
		artist: &ytdlp.ArtistListing{
			Name:   "Daft Punk",
			Cover:  "https://art.example/c.jpg",
			Albums: []ytdlp.ArtistRelease{{Type: "album", Title: "Discovery", Cover: "https://art.example/c.jpg", URL: "u"}},
		},
	}
	_ = m.View()
	if ov.want.key != "https://art.example/c.jpg" || ov.want.col+ov.want.cols > m.width {
		t.Fatalf("unexpected overlay placement: %+v (width=%d)", ov.want, m.width)
	}
	// Switch back to screenSources -> overlay cleared again
	m.screen = screenSources
	_ = m.View()
	if ov.want.key != "" {
		t.Fatalf("expected overlay cleared after leaving screenArtist, got %+v", ov.want)
	}
}

func TestRenderCoverArtDimensionsAndColors(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			c := color.RGBA{R: 200, G: 40, B: 80, A: 255}
			if y >= 8 {
				c = color.RGBA{R: 20, G: 180, B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	lines := renderCoverArt(img, 10, 5)
	if len(lines) != 5 {
		t.Fatalf("len(lines) = %d, want 5", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != 10 {
			t.Fatalf("line %d width = %d, want 10", i, w)
		}
		if !strings.Contains(line, "▀") || !strings.Contains(line, "\x1b[38;2;") {
			t.Fatalf("line %d missing truecolor half-block ANSI: %q", i, line)
		}
	}
}

func TestDownsampleCoverAndRenderCache(t *testing.T) {
	large := image.NewRGBA(image.Rect(0, 0, 400, 300))
	ds := downsampleCover(large, 192)
	if ds.Bounds().Dx() != 192 || ds.Bounds().Dy() != 192 {
		t.Fatalf("downsampled size = %dx%d, want 192x192", ds.Bounds().Dx(), ds.Bounds().Dy())
	}
	m := newModel()
	first := m.renderedCover("k1", ds, 12, 6)
	second := m.renderedCover("k1", ds, 12, 6)
	if len(first) != 6 || len(second) != 6 || &first[0] != &second[0] {
		t.Fatal("expected cached rendered cover slice to be reused")
	}
}
