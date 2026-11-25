package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"ytmanager/internal/manifest"
)

// PhysicalFolder groups scanned audio files by their actual relative directory on disk/device.
type PhysicalFolder struct {
	Dir            string
	Files          []LibraryTrack
	TotalBytes     int64
	Synced         int
	Orphans        int
	Issues         int
	HasRelinkReady bool
	LinkedSources  string
}

// IssueItem represents a single actionable problem across sources and physical storage.
type IssueItem struct {
	Kind        string // download_failed | unavailable | missing_file | corrupt_file | relink_ready | duplicate | unlisted
	Badge       string
	Title       string
	Subtitle    string
	Error       string
	Path        string
	TrackID     string
	SourceID    string
	SourceTitle string
	SourceURL   string
	LibraryRow  *LibraryTrack
}

// UnifiedTrack merges a source slot with its physical file state on disk.
type UnifiedTrack struct {
	ID           string
	Title        string
	Status       string // synced | failed | missing | pending | unavailable | removed | unlisted
	Path         string
	Error        string
	Duration     float64
	Synced       bool
	SourceMember bool
	Duplicate    bool
	Orphan       bool
	LibraryRow   *LibraryTrack
}

// UnifiedAlbum combines an album source in data.music with physical tracks found on disk.
type UnifiedAlbum struct {
	Key       string
	Title     string
	Artist    string
	SourceID  string
	SourceURL string
	Cover     string
	Year      string
	HasSource bool
	Synced    int
	Pending   int
	Failed    int
	Missing   int
	Unavail   int
	ExtraDisk int
	Folders   []string
	Tracks    []UnifiedTrack
}

// BuildPhysicalFolders groups scanned files by their actual directory on disk or phone storage.
func BuildPhysicalFolders(rows []LibraryTrack) []PhysicalFolder {
	byDir := map[string]*PhysicalFolder{}
	srcSets := map[string]map[string]bool{}
	for _, r := range rows {
		if r.Path == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filepath.Clean(r.Path)))
		f := byDir[dir]
		if f == nil {
			f = &PhysicalFolder{Dir: dir}
			byDir[dir] = f
			srcSets[dir] = map[string]bool{}
		}
		f.Files = append(f.Files, r)
		f.TotalBytes += r.Size
		if r.Orphan && !r.Missing {
			f.Orphans++
		}
		if r.Missing || r.Invalid || r.FormatMismatch || r.Duplicate || r.Status == "unreadable" {
			f.Issues++
		} else if !r.Orphan && !r.Unmanaged && r.Status != "not scanned" {
			f.Synced++
		}
		if strings.Contains(r.Note, "run repair to relink") {
			f.HasRelinkReady = true
		}
		if r.Sources != "" {
			for _, part := range strings.Split(r.Sources, " · ") {
				if p := strings.TrimSpace(part); p != "" {
					srcSets[dir][p] = true
				}
			}
		}
	}
	out := make([]PhysicalFolder, 0, len(byDir))
	for dir, f := range byDir {
		sort.Slice(f.Files, func(i, j int) bool {
			return strings.ToLower(f.Files[i].Path) < strings.ToLower(f.Files[j].Path)
		})
		var names []string
		for name := range srcSets[dir] {
			names = append(names, name)
		}
		sort.Strings(names)
		f.LinkedSources = strings.Join(names, " · ")
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir == "." {
			return true
		}
		if out[j].Dir == "." {
			return false
		}
		return strings.ToLower(out[i].Dir) < strings.ToLower(out[j].Dir)
	})
	return out
}

// WorkspaceIssues returns a unified list of download errors, missing files, corrupt files,
// duplicates, relinkable files, and unlisted physical files on the device.
func (e *Engine) WorkspaceIssues(rows []LibraryTrack) []IssueItem {
	var out []IssueItem
	byID := map[string]LibraryTrack{}
	for _, r := range rows {
		byID[r.ID] = r
	}

	e.mu.Lock()
	seenSourceTracks := map[string]bool{}
	for _, s := range e.m.Sources {
		for _, tid := range s.Tracks {
			if seenSourceTracks[tid] {
				continue
			}
			t := e.m.Tracks[tid]
			if t == nil {
				continue
			}
			switch t.Status {
			case manifest.StatusFailed:
				seenSourceTracks[tid] = true
				item := IssueItem{
					Kind:        "download_failed",
					Badge:       "Download error",
					Title:       t.Title,
					Subtitle:    s.Title,
					Error:       t.Error,
					Path:        t.Path,
					TrackID:     tid,
					SourceID:    s.ID,
					SourceTitle: s.Title,
					SourceURL:   s.URL,
				}
				if row, ok := byID[tid]; ok {
					copyRow := row
					item.LibraryRow = &copyRow
				}
				out = append(out, item)
			case manifest.StatusUnavailable:
				seenSourceTracks[tid] = true
				out = append(out, IssueItem{
					Kind:        "unavailable",
					Badge:       "Unavailable",
					Title:       t.Title,
					Subtitle:    s.Title,
					Error:       t.Error,
					TrackID:     tid,
					SourceID:    s.ID,
					SourceTitle: s.Title,
					SourceURL:   s.URL,
				})
			case manifest.StatusDone:
				row, hasRow := byID[tid]
				var present bool
				if hasRow && row.Status != "not scanned" {
					present = !row.Missing
				} else {
					present = e.catalogTrackPresent(t)
				}
				if !t.Local && !present {
					seenSourceTracks[tid] = true
					item := IssueItem{
						Kind:        "missing_file",
						Badge:       "Missing on disk",
						Title:       t.Title,
						Subtitle:    s.Title,
						Error:       "File not found at " + t.Path,
						Path:        t.Path,
						TrackID:     tid,
						SourceID:    s.ID,
						SourceTitle: s.Title,
						SourceURL:   s.URL,
					}
					if hasRow {
						copyRow := row
						item.LibraryRow = &copyRow
					}
					out = append(out, item)
				}
			case manifest.StatusPending:
				if t.Error != "" {
					seenSourceTracks[tid] = true
					item := IssueItem{
						Kind:        "missing_file",
						Badge:       "Missing on disk",
						Title:       t.Title,
						Subtitle:    s.Title,
						Error:       t.Error,
						Path:        t.Path,
						TrackID:     tid,
						SourceID:    s.ID,
						SourceTitle: s.Title,
						SourceURL:   s.URL,
					}
					if row, ok := byID[tid]; ok {
						copyRow := row
						item.LibraryRow = &copyRow
					}
					out = append(out, item)
				}
			}
		}
	}
	e.mu.Unlock()

	seenPhysical := map[string]bool{}
	for i := range rows {
		r := rows[i]
		if r.Path == "" || r.Missing {
			continue
		}
		key := strings.ToLower(filepath.Clean(r.Path))
		if seenPhysical[key] {
			continue
		}
		switch {
		case r.Invalid || r.FormatMismatch || r.Status == "unreadable":
			seenPhysical[key] = true
			copyRow := r
			badge := "Corrupt / mismatch"
			if r.Orphan {
				badge = "Unlisted · corrupt"
			}
			out = append(out, IssueItem{
				Kind:        "corrupt_file",
				Badge:       badge,
				Title:       r.Title,
				Subtitle:    r.Path,
				Error:       r.Note,
				Path:        r.Path,
				TrackID:     r.ID,
				SourceTitle: r.Sources,
				LibraryRow:  &copyRow,
			})
		case strings.Contains(r.Note, "run repair to relink"):
			seenPhysical[key] = true
			copyRow := r
			out = append(out, IssueItem{
				Kind:        "relink_ready",
				Badge:       "Needs relink",
				Title:       r.Title,
				Subtitle:    r.Path,
				Error:       "Matches a saved source ID; run Repair to link",
				Path:        r.Path,
				TrackID:     r.ID,
				SourceTitle: r.Sources,
				LibraryRow:  &copyRow,
			})
		case r.Duplicate:
			seenPhysical[key] = true
			copyRow := r
			out = append(out, IssueItem{
				Kind:        "duplicate",
				Badge:       "Duplicate",
				Title:       r.Title,
				Subtitle:    r.Path,
				Error:       strings.TrimLeft(r.Note, " ·"),
				Path:        r.Path,
				TrackID:     r.ID,
				SourceTitle: r.Sources,
				LibraryRow:  &copyRow,
			})
		case r.Orphan && r.Unmanaged:
			seenPhysical[key] = true
			copyRow := r
			out = append(out, IssueItem{
				Kind:       "unlisted",
				Badge:      "Unlisted on disk",
				Title:      r.Title,
				Subtitle:   r.Path,
				Error:      "Physical file not linked to any saved source or local playlist",
				Path:       r.Path,
				TrackID:    r.ID,
				LibraryRow: &copyRow,
			})
		}
	}
	return out
}

// UnifiedAlbums combines album sources from data.music with scanned physical tracks on disk.
func (e *Engine) UnifiedAlbums(rows []LibraryTrack) []UnifiedAlbum {
	byID := map[string]LibraryTrack{}
	byPath := map[string]LibraryTrack{}
	for _, r := range rows {
		if r.ID != "" && !strings.HasPrefix(r.ID, "file:") {
			byID[r.ID] = r
		}
		if r.Path != "" {
			byPath[strings.ToLower(filepath.Clean(r.Path))] = r
		}
	}

	e.mu.Lock()
	var albums []UnifiedAlbum
	usedPaths := map[string]bool{}
	excludedPaths := map[string]map[string]bool{}
	for _, s := range e.m.Sources {
		if s.Type != "album" {
			continue
		}
		if len(s.Excluded) > 0 {
			paths := map[string]bool{}
			for id := range s.Excluded {
				if t := e.m.Tracks[id]; t != nil && t.Path != "" {
					paths[strings.ToLower(filepath.Clean(t.Path))] = true
				}
			}
			excludedPaths[s.ID] = paths
		}
		artist := s.AlbumArtist
		if artist == "" {
			artist = s.Creator
		}
		alb := UnifiedAlbum{
			Key:       "source:" + s.ID,
			Title:     s.Title,
			Artist:    artist,
			SourceID:  s.ID,
			SourceURL: s.URL,
			Cover:     s.Cover,
			Year:      s.Year,
			HasSource: true,
		}
		folderSet := map[string]bool{}
		for _, tid := range s.TrackOrder() {
			t := e.m.Tracks[tid]
			if t == nil {
				continue
			}
			ut := UnifiedTrack{
				ID:           tid,
				SourceMember: true,
				Title:        t.Title,
				Path:         t.Path,
				Error:        t.Error,
				Duration:     t.Duration,
			}
			row, hasRow := byID[tid]
			if !hasRow && t.Path != "" {
				row, hasRow = byPath[strings.ToLower(filepath.Clean(t.Path))]
			}
			if hasRow {
				copyRow := row
				ut.LibraryRow = &copyRow
				ut.Duplicate = row.Duplicate
				ut.Orphan = row.Orphan
				if row.Path != "" {
					ut.Path = row.Path
				}
				if row.Duration > 0 {
					ut.Duration = row.Duration
				}
			}
			if ut.Path != "" {
				usedPaths[strings.ToLower(filepath.Clean(ut.Path))] = true
				if d := filepath.ToSlash(filepath.Dir(filepath.Clean(ut.Path))); d != "." {
					folderSet[d] = true
				}
			}
			switch t.Status {
			case manifest.StatusDone:
				var present bool
				if hasRow && row.Status != "not scanned" {
					present = !row.Missing && !row.Invalid
				} else {
					present = e.catalogTrackPresent(t)
				}
				if present {
					ut.Status = "synced"
					ut.Synced = true
					alb.Synced++
					if ut.Path != "" {
						folderSet[filepath.ToSlash(filepath.Dir(filepath.Clean(ut.Path)))] = true
					}
				} else {
					ut.Status = "missing"
					if ut.Error == "" {
						ut.Error = "Missing on disk"
					}
					alb.Missing++
				}
			case manifest.StatusFailed:
				ut.Status = "failed"
				alb.Failed++
			case manifest.StatusUnavailable:
				ut.Status = "unavailable"
				alb.Unavail++
			case manifest.StatusRemoved:
				ut.Status = "removed"
			default:
				ut.Status = "pending"
				alb.Pending++
			}
			alb.Tracks = append(alb.Tracks, ut)
		}
		for dir := range folderSet {
			alb.Folders = append(alb.Folders, dir)
		}
		sort.Strings(alb.Folders)
		albums = append(albums, alb)
	}
	e.mu.Unlock()

	sourceAlbumByDir := map[string]int{}
	for idx, a := range albums {
		for _, d := range a.Folders {
			key := strings.ToLower(d)
			if _, exists := sourceAlbumByDir[key]; !exists {
				sourceAlbumByDir[key] = idx
			}
		}
	}

	diskGroups := map[string]*UnifiedAlbum{}
	diskFolders := map[string]map[string]bool{}
	for _, r := range rows {
		if r.Path == "" {
			continue
		}
		pKey := strings.ToLower(filepath.Clean(r.Path))
		if usedPaths[pKey] {
			continue
		}
		usedPaths[pKey] = true
		dir := filepath.ToSlash(filepath.Dir(filepath.Clean(r.Path)))
		if idx, matched := sourceAlbumByDir[strings.ToLower(dir)]; matched && !excludedPaths[albums[idx].SourceID][pKey] {
			copyRow := r
			status := "unlisted"
			switch {
			case r.Status == "not scanned":
				status = "checking"
			case r.Invalid || r.FormatMismatch || r.Status == "unreadable":
				status = "failed"
				albums[idx].Failed++
			}
			if r.Status != "not scanned" {
				albums[idx].ExtraDisk++
			}
			albums[idx].Tracks = append(albums[idx].Tracks, UnifiedTrack{
				ID:         r.ID,
				Title:      r.Title,
				Path:       r.Path,
				Status:     status,
				Error:      strings.TrimLeft(r.Note, " ·"),
				Duration:   r.Duration,
				Duplicate:  r.Duplicate,
				Orphan:     r.Orphan,
				LibraryRow: &copyRow,
			})
			continue
		}
		k := r.AlbumKey()
		alb := diskGroups[k]
		if alb == nil {
			title := r.Album
			if title == "" {
				dir := filepath.Base(filepath.Dir(r.Path))
				if dir != "." {
					title = dir
				} else {
					title = "Loose tracks"
				}
			}
			artist := r.AlbumArtist
			if artist == "" {
				artist = r.Artist
			}
			alb = &UnifiedAlbum{
				Key:    "disk:" + k,
				Title:  title,
				Artist: artist,
			}
			diskGroups[k] = alb
			diskFolders[k] = map[string]bool{}
		}
		copyRow := r
		status := "synced"
		synced := true
		switch {
		case r.Missing:
			status, synced = "missing", false
			alb.Missing++
		case r.Invalid || r.FormatMismatch || r.Status == "unreadable":
			status, synced = "failed", false
			alb.Failed++
		case r.Status == "not scanned":
			status, synced = "checking", false
		case r.Orphan:
			status = "unlisted"
			alb.ExtraDisk++
		default:
			alb.Synced++
		}
		diskFolders[k][filepath.ToSlash(filepath.Dir(filepath.Clean(r.Path)))] = true
		alb.Tracks = append(alb.Tracks, UnifiedTrack{
			ID:         r.ID,
			Title:      r.Title,
			Status:     status,
			Path:       r.Path,
			Error:      strings.TrimLeft(r.Note, " ·"),
			Duration:   r.Duration,
			Synced:     synced,
			Duplicate:  r.Duplicate,
			Orphan:     r.Orphan,
			LibraryRow: &copyRow,
		})
	}
	for k, alb := range diskGroups {
		for dir := range diskFolders[k] {
			alb.Folders = append(alb.Folders, dir)
		}
		sort.Strings(alb.Folders)
		albums = append(albums, *alb)
	}
	sort.SliceStable(albums, func(i, j int) bool {
		if albums[i].HasSource != albums[j].HasSource {
			return albums[i].HasSource
		}
		return strings.ToLower(albums[i].Title) < strings.ToLower(albums[j].Title)
	})
	return albums
}

// FormatSummary formats a compact one-line status for a UnifiedAlbum.
func (a UnifiedAlbum) Summary() string {
	var parts []string
	if a.HasSource {
		total := len(a.Tracks)
		parts = append(parts, fmt.Sprintf("%d/%d synced", a.Synced, total))
	} else {
		parts = append(parts, fmt.Sprintf("%d on disk", len(a.Tracks)))
	}
	if a.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", a.Failed))
	}
	if a.Missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", a.Missing))
	}
	if a.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", a.Pending))
	}
	if a.ExtraDisk > 0 {
		parts = append(parts, fmt.Sprintf("%d unlisted", a.ExtraDisk))
	}
	if len(a.Folders) > 1 {
		parts = append(parts, fmt.Sprintf("%d folders", len(a.Folders)))
	}
	return strings.Join(parts, " · ")
}
