package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"ytmanager/internal/ytmusic"
)

type ArtistRelease struct {
	Type       string
	ID         string
	Title      string
	Artist     string
	Year       string
	Cover      string
	URL        string
	TrackCount int
}

type ArtistListing struct {
	Provider string
	ID       string
	Name     string
	Cover    string
	URL      string
	Albums   []ArtistRelease
	Singles  []ArtistRelease
}

// IsArtistURL reports whether raw points to an artist/channel profile rather than a track, album or playlist.
func IsArtistURL(raw string) bool {
	p := Provider(raw)
	u := webURL(raw)
	if u == nil || p == "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}
	switch p {
	case YouTube:
		if u.Query().Get("v") != "" || u.Query().Get("list") != "" || len(parts) == 0 {
			return false
		}
		if strings.HasPrefix(parts[0], "@") {
			return true
		}
		return (parts[0] == "channel" || parts[0] == "c" || parts[0] == "user") && len(parts) >= 2 && parts[1] != ""
	case "soundcloud":
		if len(parts) == 0 {
			return false
		}
		reserved := map[string]bool{"discover": true, "stream": true, "search": true, "you": true, "charts": true, "upload": true}
		if reserved[strings.ToLower(parts[0])] {
			return false
		}
		if len(parts) == 1 {
			return true
		}
		if len(parts) == 2 {
			switch strings.ToLower(parts[1]) {
			case "albums", "sets", "tracks", "popular-tracks", "reposts":
				return true
			}
		}
		return false
	case "bandcamp":
		if len(parts) == 0 {
			return true
		}
		return len(parts) == 1 && (parts[0] == "music" || parts[0] == "releases")
	}
	return false
}

// ListArtist fetches an artist's albums and singles/tracks from YouTube Music, SoundCloud or Bandcamp.
func ListArtist(ctx context.Context, raw, cookies string) (*ArtistListing, error) {
	p := Provider(raw)
	if p == "" {
		return nil, errors.New("unsupported artist link")
	}
	if p == YouTube {
		page, err := ytmusic.Artist(ctx, raw)
		if err != nil {
			return nil, err
		}
		out := &ArtistListing{
			Provider: YouTube,
			ID:       page.ID,
			Name:     page.Name,
			Cover:    page.Cover,
			URL:      page.URL,
		}
		for _, r := range page.Albums {
			out.Albums = append(out.Albums, ArtistRelease{
				Type: r.Type, ID: r.ID, Title: r.Title, Artist: r.Artist, Year: r.Year, Cover: r.Cover, URL: r.URL,
			})
		}
		for _, r := range page.Singles {
			out.Singles = append(out.Singles, ArtistRelease{
				Type: r.Type, ID: r.ID, Title: r.Title, Artist: r.Artist, Year: r.Year, Cover: r.Cover, URL: r.URL,
			})
		}
		return out, nil
	}
	outBytes, err := output(ctx, append(base(cookies), "--flat-playlist", "-J", "--", raw)...)
	if err != nil {
		return nil, err
	}
	return parseExternalArtist(outBytes, raw, p)
}

func parseExternalArtist(out []byte, raw, provider string) (*ArtistListing, error) {
	var r struct {
		creatorMetadata
		artwork
		ID      string  `json:"id"`
		Title   string  `json:"title"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(r.Title)
	if c := r.creatorMetadata.creator(); c != "" && (name == "" || strings.EqualFold(name, "music")) {
		name = c
	}
	res := &ArtistListing{
		Provider: provider,
		ID:       r.ID,
		Name:     name,
		Cover:    r.cover(),
		URL:      raw,
	}
	for _, e := range r.Entries {
		target := e.WebpageURL
		if Provider(target) != provider {
			target = e.URL
		}
		if Provider(target) != provider {
			continue
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = target
		}
		rel := ArtistRelease{
			ID:     e.ID,
			Title:  title,
			Artist: name,
			URL:    target,
		}
		u := webURL(target)
		isAlbum := false
		if u != nil {
			switch provider {
			case "bandcamp":
				isAlbum = strings.HasPrefix(u.Path, "/album/")
			case "soundcloud":
				isAlbum = strings.Contains(u.Path, "/sets/")
			}
		}
		if isAlbum {
			rel.Type = "album"
			res.Albums = append(res.Albums, rel)
		} else {
			rel.Type = "single"
			res.Singles = append(res.Singles, rel)
		}
	}
	return res, nil
}

// ArtistFromSearch converts a YouTube Music search hit to an ArtistListing seed URL.
func ArtistURL(id string) string {
	return "https://music.youtube.com/channel/" + url.PathEscape(id)
}
