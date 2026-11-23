package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

// Repair reconciles existing audio by its embedded source ID and rebuilds playlists.
// It never deletes or rewrites audio. Ambiguous paths are left for manual review.
func (e *Engine) Repair(ctx context.Context, emit func(Event)) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("wait for downloads to finish before repairing")
	}
	data, err := json.Marshal(e.m)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	m := &manifest.Manifest{Root: e.m.Root}
	if err = json.Unmarshal(data, m); err != nil {
		e.mu.Unlock()
		return err
	}
	e.running = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.running = false; e.mu.Unlock() }()
	emit(Event{Kind: EvLog, Msg: "Checking library paths and embedded song IDs…"})
	rows, err := e.ScanLibrary(ctx)
	if err != nil {
		return err
	}
	index := map[string][]LibraryTrack{}
	var untagged []physicalCandidate
	for _, row := range rows {
		if row.Missing || row.Invalid || row.Status == "unreadable" {
			continue
		}
		if row.SourceID != "" {
			index[row.SourceID] = append(index[row.SourceID], row)
		}
		if cand, ok := candidateFromInfo(row.Path, row.SourceID, map[string]string{
			"title":        row.Title,
			"artist":       row.Artist,
			"album_artist": row.AlbumArtist,
			"album":        row.Album,
		}, row.Duration); ok {
			untagged = append(untagged, cand)
		}
	}
	backup := filepath.Join(m.Root, "data.music.repair-"+time.Now().UTC().Format("20060102T150405.000000000"))
	original, err := os.ReadFile(filepath.Join(m.Root, manifest.FileName))
	if os.IsNotExist(err) {
		original = data
	} else if err != nil {
		return err
	}
	if err = os.WriteFile(backup, original, 0600); err != nil {
		return err
	}
	repaired := 0
	claimed := claimedPhysicalPaths(m)
	var needsMetadata []string
	for id, t := range m.Tracks {
		if t.Status == manifest.StatusRemoved {
			continue
		}
		chosen := ""
		ambiguous := false
		for _, want := range trackMatchIDs(id, t) {
			candidates := index[want]
			// Prefer the recorded path; duplicate embedded IDs are ambiguous after moves.
			for _, row := range candidates {
				if strings.EqualFold(row.Path, t.Path) {
					chosen = row.Path
					break
				}
			}
			if chosen == "" && len(candidates) == 1 {
				chosen = candidates[0].Path
			} else if chosen == "" && len(candidates) > 1 {
				paths := make([]string, len(candidates))
				for i, c := range candidates {
					paths[i] = c.Path
				}
				if resolved, ok := resolveCanonicalCandidate(ctx, m, id, t, e.opt.Folders, paths); ok {
					chosen = resolved
					emit(Event{Kind: EvLog, Msg: "Duplicate copies on disk; review duplicates (f) in Library: " + t.Title})
				} else {
					ambiguous = true
				}
			}
			if chosen != "" || ambiguous {
				break
			}
		}
		if chosen == "" && !ambiguous {
			for _, want := range trackMatchIDs(id, t) {
				for otherID, other := range m.Tracks {
					if otherID == id || other == nil || other.Status != manifest.StatusDone || other.Path == "" {
						continue
					}
					if (otherID == want || other.Alt == want || other.ResolvedID == want) && m.AudioPath(other) != "" {
						chosen = other.Path
						break
					}
				}
				if chosen != "" {
					break
				}
			}
		}
		if chosen != "" && (chosen != t.Path || t.Status != manifest.StatusDone) {
			t.Path, t.Status, t.Error, t.Fails = chosen, manifest.StatusDone, "", 0
			claimed[strings.ToLower(filepath.Clean(chosen))] = true
			repaired++
			emit(Event{Kind: EvLog, Msg: "Recovered: " + t.Title})
		} else if chosen == "" && ambiguous {
			if t.Status == manifest.StatusDone && !t.Local {
				emit(Event{Kind: EvLog, Msg: "Ambiguous copies; review manually: " + t.Title})
				for _, row := range rows {
					if row.ID == id && (row.Missing || row.Invalid) {
						t.Status, t.Error = manifest.StatusPending, "File missing or invalid; resume to restore"
					}
				}
			}
		} else if chosen == "" && !t.Local {
			missingFile := t.Path == "" || m.AudioPath(t) == ""
			if t.Status == manifest.StatusDone {
				for _, row := range rows {
					if row.ID == id && (row.Missing || row.Invalid) {
						t.Status, t.Error = manifest.StatusPending, "File missing or invalid; resume to restore"
						missingFile = true
					}
				}
			}
			if missingFile {
				needsMetadata = append(needsMetadata, id)
			}
		}
		if t.Status == manifest.StatusUnavailable && t.Error == "exit status 1" {
			t.Status = manifest.StatusPending
			t.Error = ""
			t.Fails = 0
		}
	}
	for _, id := range needsMetadata {
		t := m.Tracks[id]
		if matched, ok := matchTrackByMetadata(m, id, t, untagged, claimed); ok {
			t.Path, t.Status, t.Error, t.Fails = matched, manifest.StatusDone, "", 0
			claimed[strings.ToLower(filepath.Clean(matched))] = true
			repaired++
			emit(Event{Kind: EvLog, Msg: "Consolidated: " + t.Title})
		}
	}
	if e.opt.Folders == "" {
		for id, t := range m.Tracks {
			if t == nil || t.Status != manifest.StatusDone || t.Path == "" {
				continue
			}
			oldPath := t.Path
			if promoted, promoteErr := promoteSingleWithManifest(ctx, m.Root, e.opt.Folders, m, nil, id, oldPath); promoteErr == nil && promoted != oldPath {
				for _, other := range m.Tracks {
					if other != nil && other.Path == oldPath {
						other.Path = promoted
					}
				}
				repaired++
				emit(Event{Kind: EvLog, Msg: "Moved to album folder: " + promoted})
			}
		}
	}
	if err = m.Save(); err != nil {
		return err
	}
	e.mu.Lock()
	e.m = m
	e.mu.Unlock()
	if err = m.WritePlaylists(); err != nil {
		return err
	}
	if e.opt.Folders == "" {
		seenDirs := map[string]bool{}
		for _, t := range m.Tracks {
			if t.Status != manifest.StatusDone || t.Path == "" {
				continue
			}
			full := filepath.Join(m.Root, t.Path)
			dir := filepath.Dir(full)
			relDir, relErr := filepath.Rel(m.Root, dir)
			if relErr != nil || relDir == "." || strings.EqualFold(filepath.Base(dir), "Singles") || seenDirs[dir] {
				continue
			}
			seenDirs[dir] = true
			_ = ytdlp.EnsureAlbumCover(ctx, full, full)
		}
	}
	emit(Event{Kind: EvLog, Msg: fmt.Sprintf("Reconciled %d files. Playlists rebuilt. Backup: %s", repaired, backup)})
	return nil
}
