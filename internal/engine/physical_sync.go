package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"ytmanager/internal/manifest"
)

type PhysicalChangeKind string

const (
	ChangeRelink      PhysicalChangeKind = "relink"
	ChangeMetadata    PhysicalChangeKind = "metadata"
	ChangeAdoptLocal  PhysicalChangeKind = "adopt_local"
	ChangeAdoptDelete PhysicalChangeKind = "adopt_delete"
)

type PhysicalChange struct {
	Kind        PhysicalChangeKind
	TrackID     string
	SourceID    string
	SourceTitle string
	Path        string
	OldPath     string
	Title       string
	OldTitle    string
	Artist      string
	OldArtist   string
	Duration    float64
	Detail      string
}

type PhysicalSyncOptions struct {
	Relink      bool
	Metadata    bool
	AdoptLocal  bool
	AdoptDelete bool
}

type PhysicalSyncPlan struct {
	Changes []PhysicalChange
}

func (p PhysicalSyncPlan) Count(kind PhysicalChangeKind) int {
	n := 0
	for _, c := range p.Changes {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

func (p PhysicalSyncPlan) EnabledChanges(opts PhysicalSyncOptions) []PhysicalChange {
	var out []PhysicalChange
	for _, c := range p.Changes {
		switch c.Kind {
		case ChangeRelink:
			if opts.Relink {
				out = append(out, c)
			}
		case ChangeMetadata:
			if opts.Metadata {
				out = append(out, c)
			}
		case ChangeAdoptLocal:
			if opts.AdoptLocal {
				out = append(out, c)
			}
		case ChangeAdoptDelete:
			if opts.AdoptDelete {
				out = append(out, c)
			}
		}
	}
	return out
}

type PhysicalSyncReport struct {
	Relinked int
	Updated  int
	Adopted  int
	Removed  int
	Backup   string
}

func (r PhysicalSyncReport) Total() int {
	return r.Relinked + r.Updated + r.Adopted + r.Removed
}

func (r PhysicalSyncReport) Summary() string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(r.Relinked, "relinked")
	add(r.Updated, "tags updated")
	add(r.Adopted, "added to albums")
	add(r.Removed, "deletions adopted")
	if len(parts) == 0 {
		return "No physical changes applied to sources."
	}
	return "Applied to sources: " + strings.Join(parts, " · ")
}

// PreviewPhysicalSync compares scanned disk rows against saved sources and proposes
// relinks, tag updates, new local tracks in album folders, and adopted physical deletions.
func (e *Engine) PreviewPhysicalSync(rows []LibraryTrack) PhysicalSyncPlan {
	e.mu.Lock()
	defer e.mu.Unlock()

	var plan PhysicalSyncPlan
	rowByID := map[string]LibraryTrack{}
	unmanagedBySourceID := map[string][]LibraryTrack{}
	for _, r := range rows {
		if r.ID != "" && !r.Unmanaged {
			rowByID[r.ID] = r
		}
		if r.Unmanaged && !r.Missing && !r.Invalid && r.Status == "present" && r.SourceID != "" {
			unmanagedBySourceID[r.SourceID] = append(unmanagedBySourceID[r.SourceID], r)
		}
	}

	relinkedTrack := map[string]bool{}
	relinkedPath := map[string]bool{}
	referenced := e.m.ReferencedTracks()
	sourceOf := map[string]*manifest.Source{}
	for _, s := range e.m.Sources {
		for _, tid := range s.Tracks {
			if _, ok := sourceOf[tid]; !ok {
				sourceOf[tid] = s
			}
		}
	}

	// 1. Relink moved or renamed files by unique embedded source ID.
	for id, t := range e.m.Tracks {
		if t == nil || t.Status == manifest.StatusRemoved || !referenced[id] {
			continue
		}
		row, hasRow := rowByID[id]
		if hasRow && !row.Missing && !row.Invalid {
			continue
		}
		var match *LibraryTrack
		ambiguous := false
		for _, want := range trackMatchIDs(id, t) {
			cands := unmanagedBySourceID[want]
			if len(cands) == 1 {
				c := cands[0]
				if match != nil && !strings.EqualFold(match.Path, c.Path) {
					ambiguous = true
					break
				}
				match = &c
			} else if len(cands) > 1 {
				ambiguous = true
				break
			}
		}
		if match != nil && !ambiguous && !strings.EqualFold(filepath.Clean(match.Path), filepath.Clean(t.Path)) {
			relinkedTrack[id] = true
			relinkedPath[strings.ToLower(filepath.Clean(match.Path))] = true
			src := sourceOf[id]
			srcID, srcTitle := "", ""
			if src != nil {
				srcID, srcTitle = src.ID, src.Title
			}
			plan.Changes = append(plan.Changes, PhysicalChange{
				Kind:        ChangeRelink,
				TrackID:     id,
				SourceID:    srcID,
				SourceTitle: srcTitle,
				Path:        match.Path,
				OldPath:     t.Path,
				Title:       t.Title,
				Detail:      fmt.Sprintf("%s → %s", t.Path, match.Path),
			})
		}
	}

	// 2. Tag updates (title/artist edited in physical audio files).
	for id, t := range e.m.Tracks {
		if t == nil || t.Status == manifest.StatusRemoved || !referenced[id] {
			continue
		}
		row, ok := rowByID[id]
		if !ok || row.Missing || row.Invalid || row.Status != "present" {
			continue
		}
		newTitle := strings.TrimSpace(row.Title)
		newArtist := strings.TrimSpace(row.Artist)
		titleChanged := newTitle != "" && newTitle != strings.TrimSpace(t.Title)
		artistChanged := newArtist != "" && newArtist != strings.TrimSpace(t.Artist)
		if !titleChanged && !artistChanged {
			continue
		}
		var diffs []string
		if titleChanged {
			diffs = append(diffs, fmt.Sprintf("title: %q → %q", t.Title, newTitle))
		}
		if artistChanged {
			diffs = append(diffs, fmt.Sprintf("artist: %q → %q", t.Artist, newArtist))
		}
		src := sourceOf[id]
		srcID, srcTitle := "", ""
		if src != nil {
			srcID, srcTitle = src.ID, src.Title
		}
		plan.Changes = append(plan.Changes, PhysicalChange{
			Kind:        ChangeMetadata,
			TrackID:     id,
			SourceID:    srcID,
			SourceTitle: srcTitle,
			Path:        row.Path,
			Title:       newTitle,
			OldTitle:    t.Title,
			Artist:      newArtist,
			OldArtist:   t.Artist,
			Detail:      strings.Join(diffs, " · "),
		})
	}

	// 3. Adopt new physical files placed inside a managed album's folder.
	albumByDir := map[string]*manifest.Source{}
	ambiguousDir := map[string]bool{}
	for _, s := range e.m.Sources {
		if s.Type != "album" {
			continue
		}
		for _, tid := range s.Tracks {
			t := e.m.Tracks[tid]
			if t == nil || t.Path == "" || t.Status != manifest.StatusDone {
				continue
			}
			dir := strings.ToLower(filepath.ToSlash(filepath.Dir(filepath.Clean(t.Path))))
			if dir == "." || dir == "" {
				continue
			}
			if prev, exists := albumByDir[dir]; exists && prev.ID != s.ID {
				ambiguousDir[dir] = true
			} else {
				albumByDir[dir] = s
			}
		}
	}
	for _, r := range rows {
		if !r.Unmanaged || !r.Orphan || r.Missing || r.Invalid || r.Status != "present" || r.Path == "" {
			continue
		}
		pKey := strings.ToLower(filepath.Clean(r.Path))
		if relinkedPath[pKey] {
			continue
		}
		dir := strings.ToLower(filepath.ToSlash(filepath.Dir(filepath.Clean(r.Path))))
		alb, ok := albumByDir[dir]
		if !ok || ambiguousDir[dir] {
			continue
		}
		trackID := "local:" + filepath.ToSlash(filepath.Clean(r.Path))
		if r.Hash != "" {
			trackID = "local:" + r.Hash[:min(16, len(r.Hash))]
		}
		if existing := e.m.Tracks[trackID]; existing != nil && existing.Status != manifest.StatusRemoved {
			continue
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(r.Path), filepath.Ext(r.Path))
		}
		plan.Changes = append(plan.Changes, PhysicalChange{
			Kind:        ChangeAdoptLocal,
			TrackID:     trackID,
			SourceID:    alb.ID,
			SourceTitle: alb.Title,
			Path:        r.Path,
			Title:       title,
			Artist:      strings.TrimSpace(r.Artist),
			Duration:    r.Duration,
			Detail:      fmt.Sprintf("Add to album %s (%s)", alb.Title, filepath.Base(r.Path)),
		})
	}

	// 4. Adopt physical deletions (missing files removed outside ytmanager).
	for id, t := range e.m.Tracks {
		if t == nil || t.Local || t.Status == manifest.StatusRemoved || !referenced[id] || relinkedTrack[id] {
			continue
		}
		row, ok := rowByID[id]
		if !ok || !row.Missing {
			continue
		}
		if t.Status != manifest.StatusDone && (t.Status != manifest.StatusPending || t.Error == "") {
			continue
		}
		src := sourceOf[id]
		srcID, srcTitle := "", ""
		if src != nil {
			srcID, srcTitle = src.ID, src.Title
		}
		plan.Changes = append(plan.Changes, PhysicalChange{
			Kind:        ChangeAdoptDelete,
			TrackID:     id,
			SourceID:    srcID,
			SourceTitle: srcTitle,
			Path:        t.Path,
			Title:       t.Title,
			Detail:      fmt.Sprintf("Missing on disk (%s) → remove from sources", t.Path),
		})
	}

	sort.Slice(plan.Changes, func(i, j int) bool {
		if plan.Changes[i].Kind != plan.Changes[j].Kind {
			return plan.Changes[i].Kind < plan.Changes[j].Kind
		}
		return strings.ToLower(plan.Changes[i].Title) < strings.ToLower(plan.Changes[j].Title)
	})
	return plan
}

// ApplyPhysicalSync writes a backup of data.music and applies the enabled physical changes to sources.
func (e *Engine) ApplyPhysicalSync(plan PhysicalSyncPlan, opts PhysicalSyncOptions) (PhysicalSyncReport, error) {
	var report PhysicalSyncReport
	selected := plan.EnabledChanges(opts)
	if len(selected) == 0 {
		return report, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return report, fmt.Errorf("wait for downloads to finish before updating sources")
	}

	snapshot, err := json.Marshal(e.m)
	if err != nil {
		return report, err
	}
	backup := filepath.Join(e.m.Root, "data.music.sync-"+time.Now().UTC().Format("20060102T150405.000000000"))
	original, readErr := os.ReadFile(filepath.Join(e.m.Root, manifest.FileName))
	if os.IsNotExist(readErr) {
		original = snapshot
	} else if readErr != nil {
		return report, readErr
	}
	if err := os.WriteFile(backup, original, 0o600); err != nil {
		return report, err
	}
	report.Backup = backup

	restore := func() {
		var prev manifest.Manifest
		if json.Unmarshal(snapshot, &prev) == nil {
			prev.Root = e.m.Root
			e.m = &prev
		}
	}

	removedIDs := map[string]bool{}
	for _, c := range selected {
		switch c.Kind {
		case ChangeRelink:
			if t := e.m.Tracks[c.TrackID]; t != nil && c.Path != "" {
				t.Path = c.Path
				t.Status = manifest.StatusDone
				t.Error = ""
				t.Fails = 0
				report.Relinked++
			}
		case ChangeMetadata:
			if t := e.m.Tracks[c.TrackID]; t != nil {
				changed := false
				if c.Title != "" && c.Title != t.Title {
					t.Title = c.Title
					changed = true
				}
				if c.Artist != "" && c.Artist != t.Artist {
					t.Artist = c.Artist
					changed = true
				}
				if changed {
					report.Updated++
				}
			}
		case ChangeAdoptLocal:
			s := e.m.Source(c.SourceID)
			if s == nil || c.TrackID == "" || c.Path == "" {
				continue
			}
			e.m.Tracks[c.TrackID] = &manifest.Track{
				Title:    c.Title,
				Artist:   c.Artist,
				Duration: c.Duration,
				Path:     c.Path,
				Status:   manifest.StatusDone,
				Local:    true,
			}
			exists := false
			for _, tid := range s.Tracks {
				if tid == c.TrackID {
					exists = true
					break
				}
			}
			if !exists {
				s.Tracks = append(s.Tracks, c.TrackID)
				if s.Order != nil {
					s.Order = append(s.Order, c.TrackID)
				}
			}
			report.Adopted++
		case ChangeAdoptDelete:
			if t := e.m.Tracks[c.TrackID]; t != nil && !t.Local {
				t.Status = manifest.StatusRemoved
				t.PermanentlyRemoved = true
				t.Error = ""
				removedIDs[c.TrackID] = true
				report.Removed++
			}
		}
	}

	if len(removedIDs) > 0 {
		for _, s := range e.m.Sources {
			if s.Excluded == nil {
				s.Excluded = map[string]bool{}
			}
			for id := range removedIDs {
				if slices.Contains(s.Tracks, id) {
					s.Excluded[id] = true
				}
			}
			s.DropTracks(removedIDs)
		}
	}

	if err := e.m.Save(); err != nil {
		restore()
		return report, err
	}
	if err := e.m.WritePlaylists(); err != nil {
		return report, err
	}
	e.presence = nil
	return report, nil
}
