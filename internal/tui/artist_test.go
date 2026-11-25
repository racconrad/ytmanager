package tui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
	"ytmanager/internal/ytmusic"
)

func testCoverImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 160, G: 90, B: 240, A: 255})
		}
	}
	return img
}

func TestArtistExplorerFilterMarkPreviewAndBounds(t *testing.T) {
	root := t.TempDir()
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	// Searching from Sources opens promptArtist
	m = press(m, "f")
	if m.prompt != promptArtist {
		t.Fatalf("prompt = %v, want promptArtist", m.prompt)
	}
	m = press(m, "esc")

	// Simulate artist search results + selection + loaded discography
	m.screen = screenArtist
	m.artist = artistState{query: "Daft Punk", searching: true, marked: map[string]bool{}}
	next, _ := m.Update(artistSearchMsg{
		query: "Daft Punk",
		results: []ytmusic.ArtistResult{
			{ID: "UC1", Name: "Daft Punk", Subscribers: "4M subscribers", Cover: "https://art.example/artist.jpg", URL: "https://music.youtube.com/channel/UC1"},
			{ID: "UC2", Name: "Daft Punk Tribute", URL: "https://music.youtube.com/channel/UC2"},
		},
	})
	m = next.(model)
	if len(m.artist.results) != 2 {
		t.Fatalf("results = %d, want 2", len(m.artist.results))
	}

	next, _ = m.Update(artistLoadedMsg{
		query: "Daft Punk",
		artist: &ytdlp.ArtistListing{
			Provider: ytdlp.YouTube,
			Name:     "Daft Punk",
			Cover:    "https://art.example/artist.jpg",
			Albums: []ytdlp.ArtistRelease{
				{Type: "album", Title: "Discovery", Artist: "Daft Punk", Year: "2001", Cover: "https://art.example/disc.jpg", URL: "https://music.youtube.com/playlist?list=OLAK5uy_disc"},
			},
			Singles: []ytdlp.ArtistRelease{
				{Type: "single", Title: "Get Lucky", Artist: "Daft Punk", Year: "2013", Cover: "https://art.example/lucky.jpg", URL: "https://music.youtube.com/playlist?list=OLAK5uy_lucky"},
			},
		},
	})
	m = next.(model)
	m.storeCover(coverLoadedMsg{key: "https://art.example/disc.jpg", img: testCoverImage()})
	m = sized(m, 96, 26)
	view := assertFits(t, m)
	if !strings.Contains(view, "Discovery") || !strings.Contains(view, "Get Lucky") || !strings.Contains(view, "▀") {
		t.Fatalf("expected albums, singles and inline cover art:\n%s", view)
	}

	// Switch to Singles tab (3) and mark Get Lucky
	m = press(m, "3", " ")
	picked := m.artistPickedReleases()
	if len(picked) != 1 || picked[0].Title != "Get Lucky" {
		t.Fatalf("picked = %+v", picked)
	}

	// Narrow terminal fits cleanly without cover panel overflow
	m = sized(m, 34, 14)
	_ = assertFits(t, m)

	// Inspecting a release opens Collection in preview mode and returns to Artist on Esc
	m = sized(m, 96, 26)
	next, _ = m.Update(inspectLoadedMsg{
		eng: m.eng, from: screenArtist,
		url: "https://music.youtube.com/playlist?list=OLAK5uy_disc",
		listing: &ytdlp.Listing{
			Type:    "album",
			ID:      "OLAK5uy_disc",
			Title:   "Discovery",
			Creator: "Daft Punk",
			Year:    "2001",
			Cover:   "https://art.example/disc.jpg",
			Entries: []ytdlp.Entry{{ID: "tr1", Title: "One More Time", Duration: 320}},
		},
	})
	m = next.(model)
	if m.screen != screenCollection || m.collection.preview == nil {
		t.Fatalf("expected preview collection screen, got %v", m.screen)
	}
	colView := assertFits(t, m)
	if !strings.Contains(colView, "One More Time") || !strings.Contains(colView, "2001") || !strings.Contains(colView, "▀") {
		t.Fatalf("expected track, year and cover art in preview:\n%s", colView)
	}

	// Pressing m saves metadata to data.music without starting download
	m = press(m, "m")
	if m.run.busy || len(m.eng.Snapshot()) != 1 || m.eng.Snapshot()[0].Title != "Discovery" {
		t.Fatalf("expected saved metadata without download: busy=%v snap=%+v", m.run.busy, m.eng.Snapshot())
	}

	// Esc returns to screenArtist
	m = keyType(m, tea.KeyEsc)
	if m.screen != screenArtist {
		t.Fatalf("expected return to screenArtist, got %v", m.screen)
	}
}

func TestPasteArtistURLOpensArtistScreen(t *testing.T) {
	m := newModel()
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m = paste(m, "https://music.youtube.com/channel/UCkWQ0gDrqOCarmUKmppD7GQ")
	if m.screen != screenArtist || !m.artist.loading {
		t.Fatalf("expected pasting artist URL to open screenArtist loading, got screen=%v loading=%v", m.screen, m.artist.loading)
	}
}

func TestArtistMarksInstalledAndFindsDownloadedArtists(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := data.Merge("album", "OLAK5uy_disc", "Discovery", "https://music.youtube.com/playlist?list=OLAK5uy_disc", []manifest.Entry{
		{ID: "tr1", Title: "One More Time"},
	})
	s.Creator = "Daft Punk"
	data.Tracks["tr1"].Status = manifest.StatusDone
	data.Tracks["tr1"].Path = "Daft Punk/Discovery/01 - One More Time.opus"
	if err := os.MkdirAll(filepath.Join(root, "Daft Punk", "Discovery"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, data.Tracks["tr1"].Path), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}

	// Empty artist prompt opens downloaded artists picker
	m = press(m, "f", "enter")
	if m.screen != screenArtist || len(m.artist.saved) != 1 || m.artist.saved[0] != "Daft Punk" {
		t.Fatalf("expected saved artists picker with Daft Punk, got screen=%v saved=%v", m.screen, m.artist.saved)
	}
	m = press(m, "3")

	// Shift+F on Sources jumps straight to searching the source's creator
	m = press(m, "F")
	if m.screen != screenArtist || m.artist.query != "Daft Punk" || !m.artist.searching {
		t.Fatalf("expected F on Sources to search Daft Punk, got screen=%v query=%q searching=%v", m.screen, m.artist.query, m.artist.searching)
	}

	// Simulate search hit followed by loaded discography marking Discovery as saved
	next, _ := m.Update(artistSearchMsg{
		query:   "Daft Punk",
		results: []ytmusic.ArtistResult{{ID: "UC1", Name: "Daft Punk", URL: "https://music.youtube.com/channel/UC1"}},
	})
	m = next.(model)
	next, _ = m.Update(artistLoadedMsg{
		query: "Daft Punk",
		artist: &ytdlp.ArtistListing{
			Provider: ytdlp.YouTube,
			Name:     "Daft Punk",
			Albums: []ytdlp.ArtistRelease{
				{Type: "album", Title: "Discovery", Artist: "Daft Punk", Year: "2001", URL: "https://music.youtube.com/playlist?list=OLAK5uy_disc"},
				{Type: "album", Title: "Homework", Artist: "Daft Punk", Year: "1997", URL: "https://music.youtube.com/playlist?list=OLAK5uy_hw"},
			},
		},
	})
	m = sized(next.(model), 96, 26)
	view := assertFits(t, m)
	if !strings.Contains(view, "✓ 1/1") {
		t.Fatalf("expected installed badge for Discovery in artistView:\n%s", view)
	}

	// Shift+F in Library also jumps to searching the track/album's artist
	m = press(m, "esc")
	next, libCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = next.(model)
	if m.screen != screenLibrary {
		t.Fatalf("expected library screen, got %v", m.screen)
	}
	if libCmd != nil {
		next, _ = m.Update(libCmd())
		m = next.(model)
	}
	m = press(m, "F")
	if m.screen != screenArtist || m.artist.query != "Daft Punk" {
		t.Fatalf("expected F in Library to search Daft Punk, got screen=%v query=%q", m.screen, m.artist.query)
	}
}
