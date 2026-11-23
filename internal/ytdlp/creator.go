package ytdlp

import "strings"

type creatorMetadata struct {
	AlbumArtist string   `json:"album_artist"`
	Artists     []string `json:"artists"`
	Artist      string   `json:"artist"`
	Channel     string   `json:"channel"`
	Uploader    string   `json:"uploader"`
}

func (m creatorMetadata) artist() string {
	if s := strings.TrimSpace(m.AlbumArtist); s != "" {
		return s
	}
	if s := uniqueNames(m.Artists); s != "" {
		return s
	}
	return strings.TrimSpace(m.Artist)
}

func (m creatorMetadata) creator() string {
	if s := m.artist(); s != "" {
		return s
	}
	return m.owner()
}

func (m creatorMetadata) owner() string {
	if s := strings.TrimSpace(m.Channel); s != "" {
		return s
	}
	return strings.TrimSpace(m.Uploader)
}

func listingCreator(kind string, meta creatorMetadata, entries []Entry) string {
	if kind != "album" {
		// A playlist's owner is distinct from the artists whose songs it contains.
		if s := meta.owner(); s != "" {
			return s
		}
		return meta.artist()
	}
	if s := meta.artist(); s != "" {
		return s
	}
	var artists []string
	for _, e := range entries {
		if !e.Unavailable {
			artists = append(artists, e.creatorMetadata.creator())
		}
	}
	if s := uniqueNames(artists); s != "" {
		return s
	}
	return meta.owner()
}

func listingAlbumArtist(kind string, meta creatorMetadata, entries []Entry) string {
	if kind != "album" {
		return ""
	}
	if s := cleanCreatorName(meta.AlbumArtist); s != "" {
		return s
	}
	if len(meta.Artists) > 0 {
		for _, a := range meta.Artists {
			if s := cleanCreatorName(a); s != "" {
				return s
			}
		}
	}
	if s := cleanCreatorName(meta.Artist); s != "" {
		return s
	}
	counts := map[string]int{}
	display := map[string]string{}
	var order []string
	for _, e := range entries {
		if e.Unavailable {
			continue
		}
		seenEntry := map[string]bool{}
		for _, name := range e.creatorMetadata.entryArtists() {
			key := strings.ToLower(name)
			if seenEntry[key] {
				continue
			}
			seenEntry[key] = true
			if counts[key] == 0 {
				display[key] = name
				order = append(order, key)
			}
			counts[key]++
		}
	}
	bestKey := ""
	bestCount := 0
	for _, key := range order {
		if counts[key] > bestCount {
			bestCount = counts[key]
			bestKey = key
		}
	}
	if bestKey != "" {
		return display[bestKey]
	}
	if owner := meta.owner(); strings.HasSuffix(owner, " - Topic") {
		return cleanCreatorName(owner)
	}
	return ""
}

func (m creatorMetadata) entryArtists() []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		name := cleanCreatorName(raw)
		key := strings.ToLower(name)
		if name != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	for _, a := range m.Artists {
		add(a)
	}
	if len(out) > 0 {
		return out
	}
	if s := strings.TrimSpace(m.Artist); s != "" {
		add(s)
		return out
	}
	if s := strings.TrimSpace(m.AlbumArtist); s != "" {
		add(s)
		return out
	}
	if owner := m.owner(); strings.HasSuffix(owner, " - Topic") {
		add(owner)
	}
	return out
}

func (m creatorMetadata) PrimaryArtist() string {
	artists := m.entryArtists()
	if len(artists) > 0 {
		return artists[0]
	}
	return ""
}

func cleanCreatorName(s string) string {
	return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(s), " - Topic")), " ")
}

func uniqueNames(names []string) string {
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if key := strings.ToLower(name); name != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}
