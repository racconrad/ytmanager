package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

// recoverExisting reuses unique, valid embedded IDs before scheduling downloads.
// Ambiguous editions and deliberate deletions always remain untouched.
func (e *Engine) recoverExisting(ctx context.Context, sources []string, emit func(Event)) error {
	e.mu.Lock()
	wanted := map[string]string{}
	for _, sid := range sources {
		if s := e.m.Source(sid); s != nil {
			for _, id := range s.Tracks {
				t := e.m.Tracks[id]
				if t == nil || t.Local || t.Status == manifest.StatusRemoved || t.Status == manifest.StatusUnavailable {
					continue
				}
				if t.Path != "" && e.m.AudioPath(t) != "" {
					continue
				}
				source := id
				if t.Alt != "" {
					source = t.Alt
				}
				wanted[id] = source
			}
		}
	}
	index := e.physicalIndex
	e.mu.Unlock()
	if len(wanted) == 0 {
		return nil
	}
	if index == nil {
		index = map[string][]string{}
		emit(Event{Kind: EvLog, Msg: "Checking existing files before downloading missing tracks…"})
		err := filepath.WalkDir(e.Root(), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				if strings.EqualFold(d.Name(), trashDir) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 || !audioExtension(p) {
				return nil
			}
			tags, err := ytdlp.Tags(ctx, p)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if id := embeddedID(tags); id != "" {
				rel, _ := filepath.Rel(e.Root(), p)
				index[id] = append(index[id], rel)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("could not inspect existing music: %w", err)
		}
		e.mu.Lock()
		e.physicalIndex = index
		e.mu.Unlock()
	}
	e.mu.Lock()
	previous := map[string]manifest.Track{}
	for id, source := range wanted {
		t := e.m.Tracks[id]
		if len(index[source]) != 1 || t == nil || t.Status == manifest.StatusRemoved {
			continue
		}
		previous[id] = *t
		t.Path, t.Status, t.Error, t.Fails = index[source][0], manifest.StatusDone, "", 0
	}
	if len(previous) > 0 {
		if err := e.m.Save(); err != nil {
			for id, t := range previous {
				*e.m.Tracks[id] = t
			}
			e.mu.Unlock()
			return err
		}
	}
	var recovered []string
	for id := range previous {
		recovered = append(recovered, e.m.Tracks[id].Title)
	}
	e.mu.Unlock()
	for _, title := range recovered {
		emit(Event{Kind: EvLog, Msg: "Reused existing audio: " + title})
	}
	return nil
}
