package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"ytmanager/internal/manifest"
)

type validatedDeletion struct {
	row  LibraryTrack
	stat os.FileInfo
	ids  []string
}

func (e *Engine) DeletePermanently(ctx context.Context, selected []LibraryTrack) (FileReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	report := FileReport{Permanent: true}
	if e.running {
		return report, fmt.Errorf("wait for downloads to finish before deleting files")
	}
	root, err := openPermanentRoot(e.m.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()

	aliases := map[string][]string{}
	linked := map[string]bool{}
	for id, t := range e.m.Tracks {
		if t.Path != "" {
			path := filepath.Clean(t.Path)
			aliases[path] = append(aliases[path], id)
		}
		if !t.PermanentlyRemoved {
			linked[id] = true
			if t.Alt != "" {
				linked[t.Alt] = true
			}
			if t.ResolvedID != "" {
				linked[t.ResolvedID] = true
			}
		}
	}
	var targets []validatedDeletion
	var failures []error
	fail := func(path string, err error) {
		report.failure(path, err)
		failures = append(failures, err)
	}
	seen := map[string]bool{}
	for _, row := range selected {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(errors.Join(failures...), err)
		}
		t := e.m.Tracks[row.ID]
		if row.Path == "" {
			if t == nil || t.Path != "" {
				fail(row.ID, fmt.Errorf("file location not verified for %s; rescan before deleting", row.Title))
			} else if !seen["id:"+row.ID] {
				seen["id:"+row.ID] = true
				targets = append(targets, validatedDeletion{row: row, ids: []string{row.ID}})
			}
			continue
		}
		p := filepath.Clean(row.Path)
		if !safeAudioPath(p) || (t == nil && row.ID != "file:"+p && row.ID != "file:"+row.Path) ||
			(t != nil && p != filepath.Clean(t.Path)) {
			fail(row.Path, fmt.Errorf("file location changed or is unsafe; rescan: %s", row.Path))
			continue
		}
		if row.Unmanaged && len(aliases[p]) == 0 && linked[row.SourceID] {
			fail(p, fmt.Errorf("repair and relink the moved track before deleting: %s", p))
			continue
		}
		if seen["path:"+p] {
			continue
		}
		seen["path:"+p] = true
		stat, err := validatePermanentFile(ctx, root, p, row)
		if err != nil {
			fail(p, err)
			continue
		}
		row.Path = p
		ids := aliases[p]
		if t != nil && !slices.Contains(ids, row.ID) {
			ids = append(slices.Clone(ids), row.ID)
		}
		targets = append(targets, validatedDeletion{row, stat, ids})
	}
	if len(targets) == 0 {
		return report, errors.Join(failures...)
	}

	// Record tombstones before unlinking, so a failed manifest write cannot
	// leave a deleted file eligible for automatic download.
	prior := map[string]manifest.Track{}
	for _, target := range targets {
		for _, id := range target.ids {
			if _, ok := prior[id]; !ok {
				track := e.m.Tracks[id]
				prior[id] = *track
				track.Status, track.PermanentlyRemoved = manifest.StatusRemoved, true
			}
		}
	}
	if len(prior) > 0 {
		if err := e.m.Save(); err != nil {
			for id, track := range prior {
				*e.m.Tracks[id] = track
			}
			return report, errors.Join(errors.Join(failures...), err)
		}
	}

	deleted := map[string]bool{}
	for _, target := range targets {
		p := target.row.Path
		if err := ctx.Err(); err != nil {
			fail(p, err)
			break
		}
		if p != "" {
			if err := unlinkPermanentFile(root, p, target.stat); err != nil {
				fail(p, err)
				continue
			}
			if target.stat != nil {
				report.Deleted++
				report.Bytes += target.stat.Size()
				report.Items = append(report.Items, FileResult{Path: p, Outcome: "permanently deleted", Bytes: target.stat.Size()})
			} else {
				report.Missing++
				report.Items = append(report.Items, FileResult{Path: p, Outcome: "already absent"})
			}
		}
		for _, id := range target.ids {
			deleted[id] = true
		}
	}
	for id, track := range prior {
		if !deleted[id] {
			*e.m.Tracks[id] = track
		}
	}
	report.Tracks = len(deleted)
	if len(prior) > 0 {
		for _, s := range e.m.Sources {
			s.DropTracks(deleted)
		}
		failures = append(failures, e.m.Save())
		if len(deleted) > 0 {
			failures = append(failures, e.m.WritePlaylists())
		}
	}
	e.presence = nil
	return report, errors.Join(failures...)
}

func (e *Engine) blocksTrashRestore(item *trashEntry) bool {
	for id := range item.Tracks {
		if track := e.m.Tracks[id]; track != nil && track.PermanentlyRemoved {
			return true
		}
	}
	return false
}
