package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

type CollectionTrack struct {
	ID, Title, Status string
	Duration          float64
}

type Collection struct {
	Title, Creator, URL, Cover, Year string
	Tracks                           []CollectionTrack
}

func (e *Engine) Collection(id string) Collection {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.m.Source(id)
	if s == nil {
		return Collection{}
	}
	out := Collection{Title: s.Title, Creator: s.Creator, URL: s.URL, Cover: s.Cover, Year: s.Year}
	for _, id := range s.TrackOrder() {
		t := e.m.Tracks[id]
		if t == nil {
			continue
		}
		status := t.Status
		if status == manifest.StatusDone && !e.trackPresent(t) {
			status = "missing"
		}
		out.Tracks = append(out.Tracks, CollectionTrack{id, t.Title, status, t.Duration})
	}
	return out
}

// Inspect reads a collection or track listing without writing to the manifest or downloading audio.
func (e *Engine) Inspect(ctx context.Context, rawURL string) (*ytdlp.Listing, error) {
	if e.listSource != nil {
		return e.listSource(ctx, rawURL, e.opt.Cookies)
	}
	return ytdlp.List(ctx, rawURL, e.opt.Cookies)
}

// SaveSourceMetadata persists a listing's metadata and pending track entries into data.music
// without triggering audio downloads.
func (e *Engine) SaveSourceMetadata(listing *ytdlp.Listing, rawURL string) (string, error) {
	if listing == nil || listing.ID == "" || len(listing.Entries) == 0 {
		return "", fmt.Errorf("no tracks or metadata found")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	source := e.mergeSourceListing(listing, rawURL)
	if err := e.m.Save(); err != nil {
		return "", fmt.Errorf("could not save data.music: %w", err)
	}
	return source.ID, nil
}

// InstalledCatalog returns normalized downloaded album titles and unique saved artist names
// from the in-memory manifest without scanning disk.
func (e *Engine) InstalledCatalog() (albums map[string]bool, artists []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	albums = map[string]bool{}
	seenArtist := map[string]bool{}
	addArtist := func(name string) {
		name = strings.TrimSpace(strings.TrimSuffix(name, " - Topic"))
		key := strings.ToLower(name)
		if name != "" && !strings.EqualFold(name, "Unknown") && !strings.EqualFold(name, "Singles") && !seenArtist[key] {
			seenArtist[key] = true
			artists = append(artists, name)
		}
	}
	for _, s := range e.m.Sources {
		if s.Creator != "" {
			addArtist(s.Creator)
		}
		if s.Type == "album" {
			for _, tid := range s.Tracks {
				if t := e.m.Tracks[tid]; t != nil && t.Status == manifest.StatusDone && e.trackPresent(t) {
					albums[strings.ToLower(strings.TrimSpace(s.Title))] = true
					break
				}
			}
		}
	}
	for _, t := range e.m.Tracks {
		if t.Status != manifest.StatusDone || !e.trackPresent(t) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(filepath.Clean(t.Path)), "/")
		if len(parts) >= 3 {
			addArtist(parts[0])
			if !strings.EqualFold(parts[1], "Singles") {
				albums[strings.ToLower(strings.TrimSpace(parts[1]))] = true
			}
		}
	}
	sort.Strings(artists)
	return albums, artists
}

// Some album listings (e.g. Bandcamp) omit artwork that is present on their track pages.
func (e *Engine) rememberAlbumCover(id, cover string) {
	if cover == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.m.Sources {
		if s.Cover == "" && s.Type == "album" && slices.Contains(s.Tracks, id) {
			s.Cover = cover
		}
	}
}
