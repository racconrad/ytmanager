package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

func containedAudioPath(root, path string) bool {
	if !safeAudioPath(path) {
		return false
	}
	dir, err := openPermanentRoot(root)
	if err != nil {
		return false
	}
	defer dir.Close()
	st, err := dir.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// readdKnownAudio is called with the engine mutex held.
func (e *Engine) readdKnownAudio(ctx context.Context, id string, t *manifest.Track, keys []string) string {
	usable := func(path string) bool {
		if !containedAudioPath(e.m.Root, path) {
			return false
		}
		info, err := ytdlp.Probe(ctx, filepath.Join(e.m.Root, path))
		if err != nil {
			return true // Older downloads and local imports may not contain an embedded identity.
		}
		tag := embeddedID(info.Tags)
		return tag == "" || slices.Contains(keys, tag)
	}
	if t.Status == manifest.StatusDone {
		if path := e.m.AudioPath(t); usable(path) {
			return path
		}
	}
	for _, key := range keys {
		if path, _, ok := e.findExistingForIDLocked(id, key); ok && usable(path) {
			return path
		}
	}
	return ""
}

func (e *Engine) readdIndexedAudio(ctx context.Context, id string, keys []string, emit func(Event)) (bool, error) {
	index, _, err := e.indexExistingAudio(ctx, emit)
	if err != nil {
		return false, err
	}
	var path string
	for _, key := range keys {
		for _, found := range index[key] {
			if !containedAudioPath(e.m.Root, found) {
				continue
			}
			if path != "" && path != found {
				return false, nil
			}
			path = found
		}
	}
	if path == "" {
		return false, nil
	}

	e.mu.Lock()
	t := e.m.Tracks[id]
	old := *t
	t.Path, t.Status = path, manifest.StatusDone
	if err := e.m.Save(); err != nil {
		*t = old
		e.mu.Unlock()
		return false, fmt.Errorf("could not save reused audio: %w", err)
	}
	e.presence = nil
	event := Event{Kind: EvDone, ID: id, Title: t.Title}
	e.mu.Unlock()
	emit(event)
	return true, nil
}
