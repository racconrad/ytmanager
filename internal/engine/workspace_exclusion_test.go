package engine

import (
	"testing"
)

func TestAlbumDoesNotReattachExcludedAudioFromTheAlbumFolder(t *testing.T) {
	e := libraryFixture(t)
	e.m.Tracks["a"].Path = "Album/a.opus"
	e.m.Tracks["b"].Path = "Album/b.opus"
	if _, err := e.ExcludeSourceTracks("album", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	albums := e.UnifiedAlbums([]LibraryTrack{
		{ID: "file:Album/a.opus", Path: "Album/a.opus", Title: "Excluded"},
		{ID: "b", Path: "Album/b.opus", Title: "Kept"},
	})
	havePhysical := false
	for _, album := range albums {
		for _, track := range album.Tracks {
			if track.Path != "Album/a.opus" {
				continue
			}
			if album.SourceID == "album" {
				t.Fatal("excluded audio reappeared in the managed album")
			}
			havePhysical = true
		}
	}
	if !havePhysical {
		t.Fatal("excluded audio disappeared from the physical library")
	}
}
