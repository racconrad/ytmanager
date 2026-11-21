package ytdlp

import "testing"

func TestExternalListingsKeepProviderTrackIdentityAndArtwork(t *testing.T) {
	for _, tt := range []struct{ source, track, provider, meta string }{
		{"https://artist.bandcamp.com/album/record", "https://artist.bandcamp.com/track/song", "bandcamp", ""},
		{"https://soundcloud.com/artist/sets/record", "https://api-v2.soundcloud.com/tracks/42", "soundcloud", `"album_type":"ep",`},
	} {
		data := `{"_type":"playlist","id":"42","title":"Record",` + tt.meta +
			`"thumbnails":[{"url":"https://art.example/cover.jpg"}],"entries":[{"id":"42","url":"` + tt.track + `","title":"Song"}]}`
		got, err := parseListing([]byte(data), tt.source)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != "album" || got.ID != externalID(tt.provider, "collection", map[string]string{"bandcamp": "artist.bandcamp.com/42", "soundcloud": "42"}[tt.provider]) || got.Cover != "https://art.example/cover.jpg" {
			t.Fatalf("listing: %+v", got)
		}
		if len(got.Entries) != 1 || got.Entries[0].URL != tt.track || got.Entries[0].ID != externalID(tt.provider, "track", "42") {
			t.Fatalf("entries: %+v", got.Entries)
		}
	}
}

func TestSupportedProvidersRejectLookalikesAndNonWebURLs(t *testing.T) {
	for _, raw := range []string{"file://soundcloud.com/a", "https://soundcloud.com.evil.test/a", "https://evil.test/soundcloud.com", "https://a@music.youtube.com/watch?v=x"} {
		if got := Provider(raw); got != "" {
			t.Errorf("Provider(%q) = %q", raw, got)
		}
	}
	if got := Provider("https://on.soundcloud.com/slug"); got != "soundcloud" {
		t.Fatalf("share link provider: %q", got)
	}
}

func TestTrackReferenceRoundTripDoesNotEmbedPrivateQuery(t *testing.T) {
	id := externalID("soundcloud", "track", "42")
	ref := TrackReference("https://api.soundcloud.com/tracks/42?secret_token=private", id)
	if ref != "https://api.soundcloud.com/tracks/42#ytmanager="+id || TrackReferenceID(ref) != id {
		t.Fatalf("invalid embedded reference: %q", ref)
	}
	if got := TrackReferenceID(TrackReference(MusicURL("audio"), "video")); got != "video" {
		t.Fatalf("original slot lost in reference: %q", got)
	}
}

func TestSoundCloudDirectSharePreservesTokenForLookupOnly(t *testing.T) {
	const raw = "https://soundcloud.com/artist/song/s-secret"
	list, err := parseListing([]byte(`{"id":"42","webpage_url":"https://soundcloud.com/artist/song"}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	if list.Entries[0].URL != raw {
		t.Fatalf("lookup lost private address: %s", list.Entries[0].URL)
	}
	ref := TrackReference(raw, list.Entries[0].ID)
	if ref != "https://soundcloud.com/artist/song#ytmanager="+list.Entries[0].ID {
		t.Fatalf("private address leaked to tags: %q", ref)
	}
}

func TestSoundCloudIgnoresPreviewsAndRejectsUserFeeds(t *testing.T) {
	preview := map[string]any{"format_id": "hls_mp3_preview"}
	full := map[string]any{"format_id": "http_mp3"}
	info := map[string]any{"formats": []any{preview, full}}
	if err := omitSoundCloudPreviews(info); err != nil || len(info["formats"].([]any)) != 1 {
		t.Fatalf("could not retain just the full recording: %v (%v)", info, err)
	}
	if err := omitSoundCloudPreviews(map[string]any{"formats": []any{preview}}); !Unavailable(err) {
		t.Fatalf("preview-only recording was not unavailable: %v", err)
	}
	_, err := parseListing([]byte(`{"_type":"playlist","extractor_key":"SoundcloudUser","id":"42"}`), "https://soundcloud.com/artist/likes")
	if err == nil {
		t.Fatal("unsupported feed accepted")
	}
}

func TestProviderRegistryHelpers(t *testing.T) {
	if got := TrackProvider("video123", ""); got != YouTube {
		t.Fatalf("TrackProvider(youtube) = %q", got)
	}
	extID := externalID("bandcamp", "track", "42")
	if got := ExternalProvider(extID); got != "bandcamp" {
		t.Fatalf("ExternalProvider = %q", got)
	}
	if got := TrackProvider(extID, ""); got != "bandcamp" {
		t.Fatalf("TrackProvider(external) = %q", got)
	}
	if ProviderLabel("soundcloud") != "SoundCloud" || !IsExternalProvider("soundcloud") || IsExternalProvider(YouTube) {
		t.Fatalf("unexpected provider metadata")
	}
}
