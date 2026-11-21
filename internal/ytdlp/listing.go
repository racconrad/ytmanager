package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Entry struct {
	creatorMetadata
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	WebpageURL  string  `json:"webpage_url"`
	Duration    float64 `json:"duration"`
	Unavailable bool    `json:"-"` // listed as private or deleted
}

type Listing struct {
	Type, ID, Title, Creator, Cover, Year string
	Entries                               []Entry
}

type rawListing struct {
	creatorMetadata
	artwork
	ExtractorKey string  `json:"extractor_key"`
	AlbumType    string  `json:"album_type"`
	Type         string  `json:"_type"`
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	ReleaseYear  float64 `json:"release_year"`
	ReleaseDate  string  `json:"release_date"`
	Dur          float64 `json:"duration"`
	Entries      []Entry `json:"entries"`
}

// Kind classifies a URL. YT Music albums are OLAK5uy_ playlists or browse/MPREb pages; RD lists
// are generated mixes that change on every read, except RDCLAK, which are curated playlists.
func Kind(url string) string {
	switch {
	case strings.Contains(url, "list=OLAK5uy_"), strings.Contains(url, "/browse/MPREb"):
		return "album"
	case strings.Contains(url, "list=RD") && !strings.Contains(url, "list=RDCLAK"):
		return "mix"
	case strings.Contains(url, "list="):
		return "playlist"
	}
	return "track"
}

func List(ctx context.Context, url, cookies string) (*Listing, error) {
	spec := lookupProvider(Provider(url))
	if spec == nil {
		return nil, fmt.Errorf("use a %s link", SupportedProvidersText())
	}
	out, err := output(ctx, append(base(cookies), "--flat-playlist", "-J", "--", url)...)
	if err != nil {
		return nil, err
	}
	listing, err := parseListing(out, url)
	if err != nil {
		return nil, err
	}
	if spec.fillListing != nil {
		spec.fillListing(ctx, cookies, listing)
	}
	return listing, nil
}

func parseListing(out []byte, raw string) (*Listing, error) {
	var r rawListing
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	spec := lookupProvider(Provider(raw))
	external := spec != nil && spec.external
	l := &Listing{Type: Kind(raw), ID: r.ID, Title: strings.TrimPrefix(r.Title, "Album - "), Cover: r.cover()}
	if r.ReleaseYear > 0 {
		l.Year = strconv.Itoa(int(r.ReleaseYear))
	} else if len(r.ReleaseDate) >= 4 {
		l.Year = r.ReleaseDate[:4]
	}
	if r.Type != "playlist" {
		l.Type = "track"
		l.Creator = r.creatorMetadata.creator()
		l.Entries = []Entry{{ID: r.ID, Title: r.Title, Duration: r.Dur}}
		if external && r.ID != "" {
			l.ID = externalID(spec.name, "track", r.ID)
			l.Entries[0].ID, l.Entries[0].URL = l.ID, raw
		}
		return l, nil
	}
	if spec != nil && spec.validateListing != nil {
		if err := spec.validateListing(&r); err != nil {
			return nil, err
		}
	}
	if external && r.ID != "" {
		l.Type = "playlist"
		if spec.collectionType != nil {
			l.Type = spec.collectionType(raw, &r)
		}
		identity := r.ID
		if spec.collectionID != nil {
			identity = spec.collectionID(raw, &r)
		}
		l.ID = externalID(spec.name, "collection", identity)
	}
	for _, e := range r.Entries {
		if e.ID == "" {
			continue
		}
		e.Unavailable = e.Title == "[Private video]" || e.Title == "[Deleted video]"
		if external {
			e.ID = externalID(spec.name, "track", e.ID)
			if Provider(e.URL) != spec.name {
				e.URL = e.WebpageURL
			}
			if Provider(e.URL) != spec.name {
				e.URL, e.Unavailable = "", true
			}
			if spec.fillEntryTitle != nil {
				spec.fillEntryTitle(&e)
			}
		} else {
			e.URL = ""
		}
		l.Entries = append(l.Entries, e)
	}
	l.Creator = listingCreator(l.Type, r.creatorMetadata, l.Entries)
	return l, nil
}
