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
	for _, row := range rows {
		if !row.Missing && !row.Invalid && row.SourceID != "" {
			index[row.SourceID] = append(index[row.SourceID], row)
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
	for id, t := range m.Tracks {
		if t.Status == manifest.StatusRemoved {
			continue
		}
		want := id
		if t.Alt != "" {
			want = t.Alt
		}
		candidates := index[want]
		// Prefer the recorded path; duplicate embedded IDs are ambiguous after moves.
		chosen := ""
		for _, row := range candidates {
			if strings.EqualFold(row.Path, t.Path) {
				chosen = row.Path
				break
			}
		}
		if chosen == "" && len(candidates) == 1 {
			chosen = candidates[0].Path
		}
		if chosen != "" && (chosen != t.Path || t.Status != manifest.StatusDone) {
			t.Path, t.Status, t.Error, t.Fails = chosen, manifest.StatusDone, "", 0
			repaired++
			emit(Event{Kind: EvLog, Msg: "Recovered: " + t.Title})
		} else if chosen == "" && t.Status == manifest.StatusDone && !t.Local {
			if len(candidates) > 1 {
				emit(Event{Kind: EvLog, Msg: "Ambiguous copies; review manually: " + t.Title})
			}
			for _, row := range rows {
				if row.ID == id && (row.Missing || row.Invalid) {
					t.Status, t.Error = manifest.StatusPending, "File missing or invalid; resume to restore"
				}
			}
		}
		if t.Status == manifest.StatusUnavailable && t.Error == "exit status 1" {
			t.Status = manifest.StatusPending
			t.Error = ""
			t.Fails = 0
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
