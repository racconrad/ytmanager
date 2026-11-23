package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

// track downloads one track and records the result; it returns false if ctx stopped it midway.
func (e *Engine) track(ctx context.Context, id string, emit func(Event)) bool {
	e.mu.Lock()
	t := e.m.Tracks[id]
	title, root, format := t.Title, e.m.Root, e.m.Format
	target := t.MediaURL
	alt := t.Alt
	referenceID := id
	if alt != "" {
		referenceID = alt
	}
	album, albumArtist, n := e.m.AlbumInfoOf(id)
	e.mu.Unlock()

	emit(Event{Kind: EvStart, ID: id, Title: title})
	stage := func(label string) { emit(Event{Kind: EvStage, ID: id, Title: title, Msg: label}) }
	provider := ytdlp.TrackProvider(id, target)
	if alt != "" {
		provider = ytdlp.YouTube
	}
	var res TrackResolution
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		res, err = e.resolveTrack(ctx, provider, id, title, target, alt, stage, emit)
		if err == nil || ctx.Err() != nil || !ytdlp.Transient(err) {
			break
		}
	}

	var releaseDL func()
	if err == nil && res.DownloadID != "" {
		var ok bool
		releaseDL, ok = e.acquireDownloadID(ctx, res.DownloadID)
		if !ok {
			emit(Event{Kind: EvStopped, ID: id, Title: title})
			return false
		}
		defer releaseDL()
		if reused, ok := e.reuseResolvedTrack(ctx, id, res.DownloadID); ok {
			emit(Event{Kind: EvLog, Msg: "Reused existing audio: " + title})
			emit(Event{Kind: EvDone, ID: id, Title: title, Msg: reused})
			return true
		}
	}

	var path, lyr string
	resolutionOK := err == nil
	for attempt := 0; resolutionOK && attempt < 2; attempt++ {
		path, lyr, err = e.download(ctx, id, referenceID, provider, res, album, albumArtist, n, format, root, stage, func(p float64) {
			emit(Event{Kind: EvProgress, ID: id, Title: title, Pct: p})
		})
		if err == nil || ctx.Err() != nil || ytdlp.Unavailable(err) || device.IsSpaceError(err) {
			break
		}
	}
	if ctx.Err() != nil {
		emit(Event{Kind: EvStopped, ID: id, Title: title})
		return false
	}

	e.mu.Lock()
	old := t.Path
	record(t, root, path, lyr, err)
	if err == nil && res.DownloadID != "" {
		t.ResolvedID = res.DownloadID
		if e.physicalIndex != nil {
			addPhysicalIndex(e.physicalIndex, referenceID, t.Path)
			addPhysicalIndex(e.physicalIndex, res.DownloadID, t.Path)
		}
	}
	if e.presence != nil && t.Status == manifest.StatusDone {
		e.presence[t.Path] = true
		if old != t.Path {
			e.presence[old] = false
		}
	}
	status, newPath := t.Status, t.Path
	moved := filepath.IsLocal(old) && old != newPath && status == manifest.StatusDone
	if moved {
		for otherID, other := range e.m.Tracks {
			if otherID != id && strings.EqualFold(other.Path, old) {
				moved = false
				break
			}
		}
	}
	saveErr := e.m.Save()
	e.mu.Unlock()
	if moved && saveErr == nil { // keep the old copy if the new path was not saved
		if strings.EqualFold(ytdlp.LyricsPath(old), ytdlp.LyricsPath(newPath)) {
			_ = os.Remove(filepath.Join(root, old))
		} else {
			removeOld(root, old)
		}
	}

	switch {
	case saveErr != nil:
		emit(Event{Kind: EvFailed, ID: id, Title: title, Msg: "could not save data.music: " + saveErr.Error(), Err: saveErr})
	case status == manifest.StatusUnavailable:
		emit(Event{Kind: EvUnavailable, ID: id, Title: title, Msg: err.Error()})
	case err != nil:
		emit(Event{Kind: EvFailed, ID: id, Title: title, Msg: err.Error(), Err: err})
	default:
		emit(Event{Kind: EvDone, ID: id, Title: title, Msg: lyr})
	}
	return true
}

func (e *Engine) acquireDownloadID(ctx context.Context, dlID string) (func(), bool) {
	for {
		e.mu.Lock()
		if e.activeDL == nil {
			e.activeDL = map[string]chan struct{}{}
		}
		wait, busy := e.activeDL[dlID]
		if !busy {
			ch := make(chan struct{})
			e.activeDL[dlID] = ch
			e.mu.Unlock()
			return func() {
				e.mu.Lock()
				delete(e.activeDL, dlID)
				close(ch)
				e.mu.Unlock()
			}, true
		}
		e.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (e *Engine) reuseResolvedTrack(ctx context.Context, id, dlID string) (string, bool) {
	e.mu.Lock()
	t := e.m.Tracks[id]
	if t == nil || t.Status == manifest.StatusRemoved {
		e.mu.Unlock()
		return "", false
	}
	path, lyr, ok := e.findExistingForIDLocked(id, dlID)
	if !ok {
		e.mu.Unlock()
		return "", false
	}
	e.mu.Unlock()

	if promoted, err := e.promoteSingleToAlbum(ctx, id, path); err == nil && promoted != "" {
		path = promoted
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	t = e.m.Tracks[id]
	if t == nil || t.Status == manifest.StatusRemoved {
		return "", false
	}
	prev := *t
	t.Path = path
	t.Status = manifest.StatusDone
	t.ResolvedID = dlID
	if t.Lyrics == "" && lyr != "" {
		t.Lyrics = lyr
	}
	t.Error, t.Fails = "", 0
	if err := e.m.Save(); err != nil {
		*t = prev
		return "", false
	}
	if e.presence != nil {
		e.presence[path] = true
	}
	return t.Lyrics, true
}

func (e *Engine) findExistingForIDLocked(skipID, key string) (string, string, bool) {
	if key == "" {
		return "", "", false
	}
	for otherID, other := range e.m.Tracks {
		if otherID == skipID || other == nil || other.Status != manifest.StatusDone || other.Path == "" {
			continue
		}
		if otherID == key || other.Alt == key || other.ResolvedID == key {
			if actual := e.m.AudioPath(other); actual != "" {
				return actual, other.Lyrics, true
			}
		}
	}
	if e.physicalIndex != nil {
		if paths := e.physicalIndex[key]; len(paths) == 1 {
			if actual := e.m.AudioPath(&manifest.Track{Path: paths[0]}); actual != "" {
				return actual, "", true
			}
		}
	}
	return "", "", false
}

func addPhysicalIndex(index map[string][]string, key, rel string) {
	if key == "" || rel == "" {
		return
	}
	for _, existing := range index[key] {
		if existing == rel {
			return
		}
	}
	index[key] = append(index[key], rel)
}

func record(t *manifest.Track, root, path, lyr string, err error) {
	switch {
	case err == nil:
		rel, _ := filepath.Rel(root, path)
		t.Status, t.Path, t.Lyrics, t.Error, t.Fails = manifest.StatusDone, rel, lyr, "", 0
	case ytdlp.Unavailable(err):
		t.Status, t.Error = manifest.StatusUnavailable, err.Error()
	case device.IsSpaceError(err):
		t.Status, t.Error = manifest.StatusPending, "Not enough free space; free space and sync to resume."
	default:
		t.Status, t.Error = manifest.StatusFailed, err.Error()
		if !ytdlp.Transient(err) {
			t.Fails++ // an unknown failure does not prove that a video is unavailable
		}
	}
}
