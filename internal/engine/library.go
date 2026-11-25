package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"ytmanager/internal/manifest"
)

// LibraryTrack describes a managed file. Sources and creators are searchable context,
// not a claim that a playlist owner is the recording artist.
type LibraryTrack struct {
	ID, Title, Path, Sources, Creators             string
	Artist, Album, AlbumArtist, TrackNumber        string
	Codec, Container, Hash, DuplicateGroup, Status string
	SourceID                                       string
	References                                     int
	Orphan, Missing, Invalid, FormatMismatch       bool
	Bitrate, SampleRate, Channels                  int
	Modified                                       int64
	Duration                                       float64
	Duplicate                                      bool
	Unmanaged                                      bool
	Size                                           int64
	Note                                           string
}

func (e *Engine) Library() []LibraryTrack {
	out, removed, root := e.libraryRecords()
	kept := out[:0]
	for _, row := range out {
		if row, keep := libraryTrackOnDisk(root, row, removed[row.ID]); keep {
			kept = append(kept, row)
		}
	}
	out = kept
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Title), strings.ToLower(out[j].Title)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	// Similar titles flag candidates only. Different editions or artists can match;
	// the UI explicitly asks the user to compare them before deleting anything.
	groups := map[string][]int{}
	for i, t := range out {
		key := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, t.Title)
		if key != "" {
			groups[key] = append(groups[key], i)
		}
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		for a := 0; a < len(group); a++ {
			for b := a + 1; b < len(group); b++ {
				ia, ib := group[a], group[b]
				if out[ia].Duration > 0 && out[ib].Duration > 0 {
					diff := out[ia].Duration - out[ib].Duration
					if diff < 0 {
						diff = -diff
					}
					if diff > 8 {
						continue
					}
				}
				out[ia].Duplicate = true
				out[ib].Duplicate = true
			}
		}
	}
	return out
}

func (e *Engine) libraryRecords() ([]LibraryTrack, map[string]bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	contexts := map[string]struct{ sources, creators []string }{}
	pathSources := map[string]map[string]string{}
	for _, source := range e.m.Sources {
		for _, id := range source.Tracks {
			if t := e.m.Tracks[id]; t != nil && t.Path != "" && t.Status != manifest.StatusRemoved {
				p := filepath.Clean(t.Path)
				if pathSources[p] == nil {
					pathSources[p] = map[string]string{}
				}
				pathSources[p][source.ID] = source.Title
			}
			c := contexts[id]
			c.sources = append(c.sources, source.Title)
			if source.Creator != "" {
				c.creators = append(c.creators, source.Creator)
			}
			contexts[id] = c
		}
	}
	var out []LibraryTrack
	removed := map[string]bool{}
	for id, t := range e.m.Tracks {
		if t.Path == "" {
			continue
		}
		removed[id] = t.Status == manifest.StatusRemoved
		row := LibraryTrack{ID: id, Title: t.Title, Path: t.Path, Duration: t.Duration, Status: "present"}
		row.Sources = strings.Join(contexts[id].sources, " · ")
		row.Creators = strings.Join(contexts[id].creators, " · ")
		row.References = len(contexts[id].sources)
		if refs := pathSources[filepath.Clean(t.Path)]; len(refs) > row.References {
			row.References = len(refs)
			var names []string
			for _, name := range refs {
				names = append(names, name)
			}
			sort.Strings(names)
			row.Sources = strings.Join(names, " · ")
		}
		row.Orphan = row.References == 0
		if album, _ := e.m.AlbumOf(id); album != "" {
			row.Album = album
		}
		out = append(out, row)
	}
	return out, removed, e.m.Root
}

func libraryTrackOnDisk(root string, row LibraryTrack, removed bool) (LibraryTrack, bool) {
	path := filepath.Join(root, row.Path)
	if removed {
		// A crash after saving the tombstone may leave a file to delete.
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return row, false
		}
	}
	if info, err := os.Stat(path); err == nil {
		row.Size, row.Modified = info.Size(), info.ModTime().UnixNano()
		if !info.Mode().IsRegular() || info.Size() == 0 {
			row.Invalid, row.Status = true, "invalid"
		}
	} else if os.IsNotExist(err) {
		row.Missing, row.Status = true, "missing"
	} else {
		row.Status, row.Note = "unreadable", err.Error()
	}
	return row, true
}

// ManifestLibrary returns known tracks from data.music without blocking on os.Stat.
func (e *Engine) ManifestLibrary() []LibraryTrack {
	e.mu.Lock()
	defer e.mu.Unlock()
	type ctxInfo struct{ sources, creators []string }
	contexts := map[string]ctxInfo{}
	for _, source := range e.m.Sources {
		for _, id := range source.Tracks {
			c := contexts[id]
			c.sources = append(c.sources, source.Title)
			if source.Creator != "" {
				c.creators = append(c.creators, source.Creator)
			}
			contexts[id] = c
		}
	}
	var out []LibraryTrack
	for id, t := range e.m.Tracks {
		if t.Path == "" || t.Status == manifest.StatusRemoved {
			continue
		}
		row := LibraryTrack{
			ID:         id,
			Title:      t.Title,
			Path:       t.Path,
			Duration:   t.Duration,
			Status:     "not scanned",
			Sources:    strings.Join(contexts[id].sources, " · "),
			Creators:   strings.Join(contexts[id].creators, " · "),
			References: len(contexts[id].sources),
		}
		row.Orphan = row.References == 0
		if album, _ := e.m.AlbumOf(id); album != "" {
			row.Album = album
		}
		if e.presence != nil {
			if present, known := e.presence[t.Path]; known {
				if present {
					row.Status = "present"
				} else {
					row.Missing, row.Status = true, "missing"
				}
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Title), strings.ToLower(out[j].Title)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// TracksAtPath returns all non-removed manifest tracks referencing the given relative path.
func (e *Engine) TracksAtPath(rel string) []LibraryTrack {
	e.mu.Lock()
	defer e.mu.Unlock()
	want := filepath.Clean(rel)
	var out []LibraryTrack
	for id, t := range e.m.Tracks {
		if t.Path != "" && t.Status != manifest.StatusRemoved && filepath.Clean(t.Path) == want {
			out = append(out, LibraryTrack{ID: id, Title: t.Title, Path: t.Path, Duration: t.Duration})
		}
	}
	return out
}

func FilterLibrary(all []LibraryTrack, query string, duplicates bool) []LibraryTrack {
	terms := strings.Fields(strings.ToLower(query))
	var out []LibraryTrack
	for _, t := range all {
		if duplicates && !t.Duplicate {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{t.Title, t.Path, t.Sources, t.Creators, t.Artist, t.Album, t.Codec, t.Status, t.ID}, " "))
		match := true
		for _, term := range terms {
			if !strings.Contains(hay, term) {
				match = false
				break
			}
		}
		if match {
			out = append(out, t)
		}
	}
	return out
}

func (t LibraryTrack) AlbumKey() string {
	artist := t.AlbumArtist
	if artist == "" {
		artist = t.Artist
	}
	if t.Album == "" {
		return filepath.Dir(t.Path)
	}
	return strings.ToLower(artist + " / " + t.Album)
}

func (e *Engine) SourceTrackIDs(id string) map[string]bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := map[string]bool{}
	if s := e.m.Source(id); s != nil {
		for _, id := range s.Tracks {
			ids[id] = true
		}
	}
	return ids
}
