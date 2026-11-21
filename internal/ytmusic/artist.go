package ytmusic

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type ArtistResult struct {
	ID          string
	Name        string
	Subscribers string
	Cover       string
	URL         string
}

type Release struct {
	Type       string // album | single | playlist
	ID         string
	PlaylistID string
	Title      string
	Artist     string
	Year       string
	Cover      string
	URL        string
}

type TrackItem struct {
	ID       string
	Title    string
	Artist   string
	Duration float64
	URL      string
}

type ArtistPage struct {
	ID      string
	Name    string
	Cover   string
	URL     string
	Albums  []Release
	Singles []Release
	Songs   []TrackItem
}

type browseTarget struct {
	browseID string
	params   string
}

var (
	yearRe      = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	thumbSizeRe = regexp.MustCompile(`=(w\d+-h\d+|s\d+)([^/?#]*)`)
)

func webRemixContext(ctx context.Context) map[string]any {
	return map[string]any{
		"client": map[string]any{
			"clientName":    "WEB_REMIX",
			"clientVersion": clientVersion(ctx),
			"hl":            "en",
		},
	}
}

// SearchArtists queries YouTube Music for matching artists.
func SearchArtists(ctx context.Context, query string) ([]ArtistResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	const artistParams = "EgWKAQIgAWoKEAoQAxAEEAkQBQ=="
	resp, err := innertube(ctx, "search", map[string]any{
		"context": webRemixContext(ctx),
		"query":   query,
		"params":  artistParams,
	})
	if err != nil {
		return nil, err
	}
	results := parseArtistSearch(resp)
	if len(results) > 0 {
		return results, nil
	}
	// Fallback to unfiltered search if YouTube rotates the artist filter token.
	resp, err = innertube(ctx, "search", map[string]any{
		"context": webRemixContext(ctx),
		"query":   query,
	})
	if err != nil {
		return nil, err
	}
	return parseArtistSearch(resp), nil
}

// Artist fetches an artist's discography (albums, singles, and top songs).
func Artist(ctx context.Context, idOrURL string) (*ArtistPage, error) {
	browseID, err := resolveArtistID(ctx, idOrURL)
	if err != nil {
		return nil, err
	}
	resp, err := innertube(ctx, "browse", map[string]any{
		"context":  webRemixContext(ctx),
		"browseId": browseID,
	})
	if err != nil {
		return nil, err
	}
	page, more := parseArtistPage(resp, browseID)
	for kind, target := range more {
		if target.browseID == "" {
			continue
		}
		body := map[string]any{
			"context":  webRemixContext(ctx),
			"browseId": target.browseID,
		}
		if target.params != "" {
			body["params"] = target.params
		}
		fullResp, err := innertube(ctx, "browse", body)
		if err != nil {
			continue
		}
		items := parseReleaseList(fullResp, kind, page.Name)
		if len(items) == 0 {
			continue
		}
		if kind == "album" {
			page.Albums = items
		} else if kind == "single" {
			page.Singles = items
		}
	}
	return page, nil
}

func resolveArtistID(ctx context.Context, idOrURL string) (string, error) {
	if id, ok := artistBrowseIDFromURL(idOrURL); ok {
		return id, nil
	}
	u, err := url.Parse(strings.TrimSpace(idOrURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid artist link or ID")
	}
	resp, err := innertube(ctx, "navigation/resolve_url", map[string]any{
		"context": webRemixContext(ctx),
		"url":     u.String(),
	})
	if err != nil {
		return "", err
	}
	if id := text(field(resp, "endpoint", "browseEndpoint", "browseId")); strings.HasPrefix(id, "UC") {
		return id, nil
	}
	return "", errors.New("could not resolve YouTube Music artist ID")
}

func artistBrowseIDFromURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "UC") && !strings.ContainsAny(raw, "/?&# \t\n") && len(raw) >= 10 {
		return raw, true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "music.youtube.com", "youtube.com", "m.youtube.com":
	default:
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "channel" && strings.HasPrefix(parts[1], "UC") {
		return parts[1], true
	}
	return "", false
}

func parseArtistSearch(response any) []ArtistResult {
	var out []ArtistResult
	seen := map[string]bool{}
	add := func(r ArtistResult) {
		if r.ID == "" || r.Name == "" || seen[r.ID] {
			return
		}
		seen[r.ID] = true
		r.URL = "https://music.youtube.com/channel/" + r.ID
		out = append(out, r)
	}
	var visit func(any)
	visit = func(v any) {
		switch node := v.(type) {
		case []any:
			for _, child := range node {
				visit(child)
			}
		case map[string]any:
			if card, ok := node["musicCardShelfRenderer"]; ok {
				id := text(field(card, "onTap", "browseEndpoint", "browseId"))
				if strings.HasPrefix(id, "UC") {
					add(ArtistResult{
						ID:          id,
						Name:        runsText(field(card, "title", "runs")),
						Subscribers: extractSubscribers(runsText(field(card, "subtitle", "runs"))),
						Cover:       bestThumbnailURL(field(card, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails")),
					})
				}
			}
			if item, ok := node["musicResponsiveListItemRenderer"]; ok {
				ep := field(item, "navigationEndpoint", "browseEndpoint")
				id := text(field(ep, "browseId"))
				pageType := text(field(ep, "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType"))
				if pageType == "MUSIC_PAGE_TYPE_ARTIST" || strings.HasPrefix(id, "UC") {
					name := flexColumnText(item, 0)
					sub := extractSubscribers(flexColumnText(item, 1))
					add(ArtistResult{
						ID:          id,
						Name:        name,
						Subscribers: sub,
						Cover:       bestThumbnailURL(field(item, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails")),
					})
				}
				return
			}
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(response)
	return out
}

func parseArtistPage(response any, browseID string) (*ArtistPage, map[string]browseTarget) {
	page := &ArtistPage{
		ID:  browseID,
		URL: "https://music.youtube.com/channel/" + browseID,
	}
	more := map[string]browseTarget{}
	for _, key := range []string{"musicImmersiveHeaderRenderer", "musicVisualHeaderRenderer", "musicDetailHeaderRenderer"} {
		if h := field(response, "header", key); h != nil {
			page.Name = runsText(field(h, "title", "runs"))
			page.Cover = bestThumbnailURL(field(h, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails"))
			break
		}
	}
	var visit func(any)
	visit = func(v any) {
		switch node := v.(type) {
		case []any:
			for _, child := range node {
				visit(child)
			}
		case map[string]any:
			if shelf, ok := node["musicCarouselShelfRenderer"]; ok {
				hdr := field(shelf, "header", "musicCarouselShelfBasicHeaderRenderer")
				title := strings.ToLower(runsText(field(hdr, "title", "runs")))
				kind := classifyArtistShelf(title)
				if kind != "" {
					if ep := field(hdr, "moreContentButton", "buttonRenderer", "navigationEndpoint", "browseEndpoint"); ep != nil {
						more[kind] = browseTarget{
							browseID: text(field(ep, "browseId")),
							params:   text(field(ep, "params")),
						}
					}
					items := parseReleaseList(field(shelf, "contents"), kind, page.Name)
					if kind == "album" {
						page.Albums = append(page.Albums, items...)
					} else {
						page.Singles = append(page.Singles, items...)
					}
				}
				return
			}
			if shelf, ok := node["musicShelfRenderer"]; ok {
				title := strings.ToLower(runsText(field(shelf, "title", "runs")))
				if strings.Contains(title, "song") || title == "" {
					items := parseTrackList(field(shelf, "contents"), page.Name)
					page.Songs = append(page.Songs, items...)
				}
				return
			}
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(response)
	return page, more
}

func classifyArtistShelf(title string) string {
	switch {
	case strings.Contains(title, "album"):
		return "album"
	case strings.Contains(title, "single") || strings.Contains(title, "ep"):
		return "single"
	}
	return ""
}

func parseReleaseList(v any, defaultKind, artist string) []Release {
	var out []Release
	seen := map[string]bool{}
	var visit func(any)
	visit = func(cur any) {
		switch node := cur.(type) {
		case []any:
			for _, child := range node {
				visit(child)
			}
		case map[string]any:
			if card, ok := node["musicTwoRowItemRenderer"]; ok {
				title := runsText(field(card, "title", "runs"))
				browseID := text(field(card, "navigationEndpoint", "browseEndpoint", "browseId"))
				playlistID := findPlaylistID(card)
				sub := runsText(field(card, "subtitle", "runs"))
				kind := defaultKind
				if strings.Contains(strings.ToLower(sub), "single") || strings.Contains(strings.ToLower(sub), "ep") {
					kind = "single"
				} else if strings.Contains(strings.ToLower(sub), "album") {
					kind = "album"
				}
				relURL := ""
				if playlistID != "" {
					relURL = "https://music.youtube.com/playlist?list=" + playlistID
				} else if browseID != "" {
					relURL = "https://music.youtube.com/browse/" + browseID
				}
				key := browseID
				if key == "" {
					key = playlistID
				}
				if title != "" && relURL != "" && !seen[key] {
					seen[key] = true
					out = append(out, Release{
						Type:       kind,
						ID:         key,
						PlaylistID: playlistID,
						Title:      title,
						Artist:     artist,
						Year:       extractYear(sub),
						Cover:      bestThumbnailURL(field(card, "thumbnailRenderer", "musicThumbnailRenderer", "thumbnail", "thumbnails")),
						URL:        relURL,
					})
				}
				return
			}
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(v)
	return out
}

func findPlaylistID(v any) string {
	if id := text(field(v, "thumbnailOverlay", "musicItemThumbnailOverlayRenderer", "content", "musicPlayButtonRenderer", "playNavigationEndpoint", "watchPlaylistEndpoint", "playlistId")); id != "" {
		return id
	}
	if items, ok := field(v, "menu", "menuRenderer", "items").([]any); ok {
		for _, item := range items {
			if id := text(field(item, "menuNavigationItemRenderer", "navigationEndpoint", "watchPlaylistEndpoint", "playlistId")); id != "" {
				return id
			}
		}
	}
	return ""
}

func parseTrackList(v any, defaultArtist string) []TrackItem {
	items, _ := v.([]any)
	var out []TrackItem
	for _, raw := range items {
		m, _ := raw.(map[string]any)
		if item, ok := parseTrackItem(m["musicResponsiveListItemRenderer"], defaultArtist); ok {
			out = append(out, item)
		}
	}
	return out
}

func parseTrackItem(v any, defaultArtist string) (TrackItem, bool) {
	if v == nil {
		return TrackItem{}, false
	}
	id := text(field(v, "playlistItemData", "videoId"))
	if id == "" {
		id = text(field(v, "overlay", "musicItemThumbnailOverlayRenderer", "content", "musicPlayButtonRenderer", "playNavigationEndpoint", "watchEndpoint", "videoId"))
	}
	title := flexColumnText(v, 0)
	if title == "" {
		return TrackItem{}, false
	}
	artist := flexColumnText(v, 1)
	if artist == "" {
		artist = defaultArtist
	}
	dur := parseDurationSeconds(fixedColumnText(v, 0))
	trackURL := ""
	if id != "" {
		trackURL = "https://music.youtube.com/watch?v=" + id
	}
	return TrackItem{ID: id, Title: title, Artist: artist, Duration: dur, URL: trackURL}, true
}

func runsText(v any) string {
	runs, _ := v.([]any)
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(text(field(r, "text")))
	}
	return strings.TrimSpace(b.String())
}

func flexColumnText(item any, idx int) string {
	cols, _ := field(item, "flexColumns").([]any)
	if idx < 0 || idx >= len(cols) {
		return ""
	}
	return runsText(field(cols[idx], "musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
}

func fixedColumnText(item any, idx int) string {
	cols, _ := field(item, "fixedColumns").([]any)
	if idx < 0 || idx >= len(cols) {
		return ""
	}
	return runsText(field(cols[idx], "musicResponsiveListItemFixedColumnRenderer", "text", "runs"))
}

func bestThumbnailURL(v any) string {
	list, _ := v.([]any)
	for i := len(list) - 1; i >= 0; i-- {
		if u := text(field(list[i], "url")); strings.HasPrefix(u, "https://") {
			return hiResCoverURL(u)
		}
	}
	return ""
}

func hiResCoverURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, "googleusercontent.com") && !strings.Contains(host, "ggpht.com") {
		return raw
	}
	return thumbSizeRe.ReplaceAllStringFunc(raw, func(m string) string {
		sub := thumbSizeRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		if strings.HasPrefix(sub[1], "w") {
			return "=w600-h600" + sub[2]
		}
		return "=s600" + sub[2]
	})
}

func extractSubscribers(s string) string {
	for _, part := range strings.Split(s, "•") {
		p := strings.TrimSpace(part)
		if strings.Contains(strings.ToLower(p), "subscriber") {
			return p
		}
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Artist • "))
}

func extractYear(s string) string {
	return yearRe.FindString(s)
}

func parseDurationSeconds(s string) float64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return float64(total)
}
