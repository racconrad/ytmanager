package engine

import (
	"context"
	"fmt"
	"strings"

	"ytmanager/internal/manifest"
)

// RefreshLibrary reconciles missing/invalid managed files after a complete scan.
// Tool or permission errors never cause music to be scheduled for replacement.
func (e *Engine) RefreshLibrary(ctx context.Context) ([]LibraryTrack, error) {
	return e.RefreshLibraryProgress(ctx, nil)
}

// RefreshLibraryProgress publishes discovered files before probing them. Only its final
// result can be used for mutations or to infer a file is missing.
func (e *Engine) RefreshLibraryProgress(ctx context.Context, discover func([]LibraryTrack)) ([]LibraryTrack, error) {
	rows, err := e.scanLibrary(ctx, discover)
	if err != nil {
		return rows, err
	}
	return rows, e.reconcileRows(ctx, rows)
}

func (e *Engine) reconcileRows(ctx context.Context, rows []LibraryTrack) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.running {
		return nil
	}
	previous := map[string]manifest.Track{}
	for _, r := range rows {
		t := e.m.Tracks[r.ID]
		if t == nil || t.Local || t.Status == manifest.StatusRemoved || t.Path != r.Path {
			continue
		}
		if r.Missing || r.Invalid {
			if t.Status != manifest.StatusPending || t.Error != "File missing or invalid; resume to restore" {
				previous[r.ID] = *t
				t.Status, t.Error = manifest.StatusPending, "File missing or invalid; resume to restore"
			}
		}
	}
	if len(previous) == 0 {
		return nil
	}
	if err := e.m.Save(); err != nil {
		for id, t := range previous {
			*e.m.Tracks[id] = t
		}
		return err
	}
	return e.m.WritePlaylists()
}

// CleanOrphans rechecks membership at confirmation, including local playlists.
func (e *Engine) CleanOrphans(ctx context.Context, selected []LibraryTrack) (FileReport, error) {
	var report FileReport
	current, err := e.ScanLibrary(ctx)
	if err != nil {
		return report, err
	}
	byID := map[string]LibraryTrack{}
	for _, r := range current {
		byID[r.ID] = r
	}
	for _, r := range selected {
		actual, ok := byID[r.ID]
		if !ok || !actual.Orphan || actual.Missing || actual.Path != r.Path {
			return report, fmt.Errorf("playlist membership changed; review cleanup again: %s", r.Path)
		}
		if strings.Contains(actual.Note, "run repair to relink") {
			return report, fmt.Errorf("run repair to relink this moved file before bulk cleanup: %s", r.Path)
		}
	}
	return e.TrashFiles(ctx, selected)
}

// ResetTracks moves the reviewed files to trash and marks their downloaded tracks pending,
// so the next sync downloads them again from scratch. Restoring the trash batch undoes it.
func (e *Engine) ResetTracks(ctx context.Context, selected []LibraryTrack) (FileReport, error) {
	return e.trashFiles(ctx, selected, true)
}
