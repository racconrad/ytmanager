package tui

import (
	"fmt"
	"strings"
	"testing"

	"ytmanager/internal/ytdlp"
	"ytmanager/internal/ytmusic"
)

func TestCatalogArtistSavedAndSearchScopes(t *testing.T) {
	base := sized(newModel(), 80, 24)
	base.screen = screenArtist
	for _, tc := range []struct {
		name   string
		artist artistState
		want   []string
	}{
		{"saved", artistState{saved: []string{"Broadcast", "Stereolab"}}, []string{"Saved artists", "Broadcast", "enter search artist"}},
		{"results", artistState{query: "Broadcast", results: []ytmusic.ArtistResult{{Name: "Broadcast", Subscribers: "12K subscribers"}}}, []string{"Artist search", "Results for", "12K subscribers", "enter open artist"}},
		{"no matches", artistState{query: "Missing", saved: []string{"Broadcast"}}, []string{"No matching artists", "Saved artists", "Broadcast", "f new search"}},
		{"empty", artistState{}, []string{"Saved artists", "No saved artists yet", "f find an artist"}},
		{"loading", artistState{query: "Broadcast", loading: true}, []string{"Artist search", "Loading discography"}},
		{"error", artistState{query: "Broadcast", err: "Provider unavailable"}, []string{"Artist search", "Provider unavailable", "f search"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.artist = tc.artist
			assertCatalogText(t, assertFits(t, m), tc.want...)
		})
	}
}

func TestCatalogDiscographyKeepsActiveScopeAndReleaseAtMinimumSize(t *testing.T) {
	m := sized(newModel(), 30, 12)
	m.screen = screenArtist
	m.artist = artistState{
		artist: &ytdlp.ArtistListing{
			Name: "Broadcast",
			Albums: []ytdlp.ArtistRelease{
				{Title: "Tender Buttons", Year: "2005", Type: "album", URL: "album"},
			},
			Singles: []ytdlp.ArtistRelease{
				{Title: "Come On Let's Go", Year: "2000", Type: "single", URL: "single"},
			},
		},
		marked:          map[string]bool{},
		installedAlbums: map[string]bool{"tender buttons": true},
	}
	m.log("Library ready")
	m = press(m, "2")
	assertCatalogText(t, assertFits(t, m), "Broadcast", "Discography", "[2 Albums]", "›", "Tender Buttons", "✓ Saved", "v preview")
	m = press(m, "3", " ")
	view := assertFits(t, m)
	assertCatalogText(t, view, "[3 Singles]", "●", "Come On Let's Go", "Not saved", "enter download")
	if strings.Contains(view, "Tender Buttons") {
		t.Fatalf("albums still visible in the Singles scope:\n%s", view)
	}
}

func TestCatalogCollectionKeepsTrackTitleAndStatusAtMinimumSize(t *testing.T) {
	m := sized(newModel(), 30, 12)
	m.screen = screenCollection
	m.collection.preview = &ytdlp.Listing{
		Title:   "Ghost Echoes",
		Creator: "Mira",
		Year:    "2021",
		Cover:   "https://example.com/cover.jpg",
		Entries: []ytdlp.Entry{
			{Title: "First Entry"},
			{Title: "Chosen Title 音楽", Unavailable: true},
		},
	}
	m.cursor = 1
	m.log("Library ready")
	assertCatalogText(t, assertFits(t, m), "Ghost Echoes", "Mira", "2021", "2 tracks", "02", "Chosen Title", "Unavailable", "c cover", "enter download")
}

func TestCatalogInspectorKeepsLastReleaseVisibleOnShortTerminals(t *testing.T) {
	const cover = "https://example.com/cover.jpg"
	m := newModel()
	if err := m.openDest(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m = sized(m, 80, 18)
	m.screen = screenArtist
	m.artist.artist = &ytdlp.ArtistListing{Name: "Broadcast", Cover: cover}
	for i := range 21 {
		m.artist.artist.Albums = append(m.artist.artist.Albums, ytdlp.ArtistRelease{
			Title: fmt.Sprintf("Release %02d", i+1), Type: "album",
		})
	}
	m.storeCover(coverLoadedMsg{key: cover, img: testCoverImage()})
	m.log("Library ready")
	m = press(m, "end")
	view := assertFits(t, m)
	assertCatalogText(t, view, "› ○ Release 21", "Not saved", "▀", "v preview", "esc back")
}

func assertCatalogText(t *testing.T, view string, want ...string) {
	t.Helper()
	for _, text := range want {
		if !strings.Contains(view, text) {
			t.Errorf("missing %q in view:\n%s", text, view)
		}
	}
}
