package ytdlp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const YouTube = "youtube"

type providerSpec struct {
	name            string
	label           string
	external        bool
	matchHost       func(host string) bool
	normalizeRefURL func(u *url.URL)
	sanitizeInfo    func(map[string]any) error
	validateListing func(r *rawListing) error
	collectionType  func(raw string, r *rawListing) string
	collectionID    func(raw string, r *rawListing) string
	fillEntryTitle  func(e *Entry)
	fillListing     func(ctx context.Context, cookies string, l *Listing)
}

var providers []providerSpec

func init() {
	providers = []providerSpec{
		{
			name:  YouTube,
			label: "YouTube Music",
			matchHost: func(host string) bool {
				switch host {
				case "youtube.com", "www.youtube.com", "music.youtube.com", "m.youtube.com", "youtu.be":
					return true
				}
				return false
			},
		},
		{
			name:     "soundcloud",
			label:    "SoundCloud",
			external: true,
			matchHost: func(host string) bool {
				switch host {
				case "soundcloud.com", "www.soundcloud.com", "m.soundcloud.com", "on.soundcloud.com", "soundcloud.app.goo.gl", "api.soundcloud.com", "api-v2.soundcloud.com":
					return true
				}
				return false
			},
			normalizeRefURL: func(u *url.URL) {
				switch strings.ToLower(u.Hostname()) {
				case "soundcloud.com", "www.soundcloud.com", "m.soundcloud.com", "api.soundcloud.com", "api-v2.soundcloud.com":
					parts := strings.Split(strings.Trim(u.Path, "/"), "/")
					u.Path, u.RawPath = "/"+strings.Join(parts[:min(2, len(parts))], "/"), ""
				default:
					u.Host, u.Path, u.RawPath = "soundcloud.com", "/", ""
				}
			},
			sanitizeInfo: omitSoundCloudPreviews,
			validateListing: func(r *rawListing) error {
				if r.ExtractorKey != "" && r.ExtractorKey != "SoundcloudSet" && r.ExtractorKey != "SoundcloudPlaylist" {
					return errors.New("use a SoundCloud track or playlist link, not a profile or feed")
				}
				return nil
			},
			collectionType: func(_ string, r *rawListing) string {
				if r.AlbumType == "album" || r.AlbumType == "ep" || r.AlbumType == "compilation" {
					return "album"
				}
				return "playlist"
			},
			fillEntryTitle: func(e *Entry) {
				if e.Title != "" || e.URL == "" {
					return
				}
				u := webURL(e.URL)
				if u == nil {
					return
				}
				switch strings.ToLower(u.Hostname()) {
				case "soundcloud.com", "www.soundcloud.com", "m.soundcloud.com":
					parts := strings.Split(strings.Trim(u.Path, "/"), "/")
					if len(parts) >= 2 {
						e.Title = strings.ReplaceAll(parts[1], "-", " ")
					}
				}
			},
		},
		{
			name:     "bandcamp",
			label:    "Bandcamp",
			external: true,
			matchHost: func(host string) bool {
				return host == "bandcamp.com" || strings.HasSuffix(host, ".bandcamp.com")
			},
			collectionType: func(raw string, _ *rawListing) string {
				if u := webURL(raw); u != nil && strings.HasPrefix(u.Path, "/album/") {
					return "album"
				}
				return "playlist"
			},
			collectionID: func(raw string, r *rawListing) string {
				if u := webURL(raw); u != nil {
					return strings.ToLower(u.Hostname()) + "/" + r.ID
				}
				return r.ID
			},
			fillListing: func(ctx context.Context, cookies string, l *Listing) {
				if l.Type != "album" || l.Cover != "" {
					return
				}
				for _, entry := range l.Entries {
					if entry.Unavailable {
						continue
					}
					// Bandcamp exposes album artwork on track pages, not in its flat album result.
					coverCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
					info, _ := Info(coverCtx, entry.URL, cookies)
					cancel()
					cover, _ := info["thumbnail"].(string)
					l.Cover = (artwork{Thumbnail: cover}).cover()
					break
				}
			},
		},
	}
}

func lookupProvider(name string) *providerSpec {
	for i := range providers {
		if providers[i].name == name {
			return &providers[i]
		}
	}
	return nil
}

func webURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return u
}

func Provider(raw string) string {
	u := webURL(raw)
	if u == nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range providers {
		if p.matchHost(host) {
			return p.name
		}
	}
	return ""
}

func ProviderLabel(name string) string {
	if p := lookupProvider(name); p != nil {
		return p.label
	}
	return ""
}

func IsExternalProvider(name string) bool {
	p := lookupProvider(name)
	return p != nil && p.external
}

func SupportedProvidersText() string {
	labels := make([]string, 0, len(providers)+1)
	for _, p := range providers {
		if p.name == YouTube {
			labels = append(labels, "YouTube")
		}
		labels = append(labels, p.label)
	}
	if len(labels) <= 1 {
		return strings.Join(labels, "")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " or " + labels[len(labels)-1]
}

func ExternalProvider(id string) string {
	for _, p := range providers {
		if !p.external {
			continue
		}
		if hash, ok := strings.CutPrefix(id, p.name+"-track-"); ok && len(hash) == 64 {
			if strings.Trim(hash, "0123456789abcdef") == "" {
				return p.name
			}
		}
	}
	return ""
}

func TrackProvider(id, target string) string {
	if p := Provider(target); p != "" {
		return p
	}
	if ext := ExternalProvider(id); ext != "" {
		return ext
	}
	return YouTube
}

func externalID(provider, kind, id string) string {
	// Extractor IDs are not necessarily portable filenames or globally unique.
	return fmt.Sprintf("%s-%s-%x", provider, kind, sha256.Sum256([]byte(id)))
}

type artwork struct {
	Thumbnail  string `json:"thumbnail"`
	Thumbnails []struct {
		URL string `json:"url"`
	} `json:"thumbnails"`
}

func (a artwork) cover() string {
	valid := func(raw string) bool {
		u := webURL(raw)
		return u != nil && u.Scheme == "https"
	}
	if valid(a.Thumbnail) {
		return a.Thumbnail
	}
	for i := len(a.Thumbnails) - 1; i >= 0; i-- {
		if raw := a.Thumbnails[i].URL; valid(raw) {
			return raw
		}
	}
	return ""
}

func TrackReference(target, id string) string {
	if ExternalProvider(id) == "" {
		return MusicURL(id)
	}
	u := webURL(target)
	if u == nil {
		return ""
	}
	u.RawQuery, u.ForceQuery, u.Fragment = "", false, "ytmanager="+id
	if p := lookupProvider(Provider(target)); p != nil && p.normalizeRefURL != nil {
		p.normalizeRefURL(u)
	}
	return u.String()
}

func TrackReferenceID(raw string) string {
	u := webURL(raw)
	if u == nil || Provider(raw) == "" {
		return ""
	}
	if id, ok := strings.CutPrefix(u.Fragment, "ytmanager="); ok && ExternalProvider(id) != "" {
		return id
	}
	if Provider(raw) != YouTube {
		return ""
	}
	if u.Hostname() == "youtu.be" {
		return strings.Trim(u.Path, "/")
	}
	return u.Query().Get("v")
}

var errPreviewOnly = errors.New("only a SoundCloud preview is available")

func omitSoundCloudPreviews(info map[string]any) error {
	isPreview := func(format any) bool {
		row, _ := format.(map[string]any)
		id, _ := row["format_id"].(string)
		return strings.HasSuffix(id, "_preview")
	}
	if formats, ok := info["formats"].([]any); ok {
		full := slices.DeleteFunc(formats, isPreview)
		if len(full) == 0 && len(formats) != 0 {
			return errPreviewOnly
		}
		info["formats"] = full
		return nil
	}
	if isPreview(info) {
		return errPreviewOnly
	}
	return nil
}
