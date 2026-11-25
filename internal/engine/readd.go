package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ytmanager/internal/manifest"
)

func (e *Engine) RestorableTracks(sourceID string) []CollectionTrack {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.m.Source(sourceID)
	if s == nil {
		return nil
	}
	members := make(map[string]bool, len(s.Tracks))
	for _, id := range s.Tracks {
		members[id] = true
	}
	var tracks []CollectionTrack
	for id, t := range e.m.Tracks {
		if canReadd(s, id, t, members[id]) {
			title := t.Title
			if title == "" {
				title = id
			}
			tracks = append(tracks, CollectionTrack{ID: id, Title: title, Status: t.Status})
		}
	}
	slices.SortFunc(tracks, func(a, b CollectionTrack) int {
		if n := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return tracks
}

func canReadd(s *manifest.Source, id string, t *manifest.Track, member bool) bool {
	if s.Type == "track" && s.ID != id && !member && !s.Excluded[id] {
		return false
	}
	return t != nil && (s.Excluded[id] ||
		t.PermanentlyRemoved || (t.ReaddGeneration > 0 && !member) ||
		(s.Readded[id] && (t.Status == manifest.StatusPending || t.Status == manifest.StatusFailed)) ||
		(member && t.Status == manifest.StatusRemoved))
}

// ReaddTrack never resumes the normal download queue.
func (e *Engine) ReaddTrack(ctx context.Context, sourceID, trackID string, emit func(Event)) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("wait for the current operation to finish")
	}
	s, t := e.m.Source(sourceID), e.m.Tracks[trackID]
	if s == nil || !canReadd(s, trackID, t, slices.Contains(s.Tracks, trackID)) {
		e.mu.Unlock()
		return fmt.Errorf("track is no longer available to re-add to this list")
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	if t.Status == manifest.StatusRemoved && !t.PermanentlyRemoved {
		for _, other := range e.m.Sources {
			if other != s && slices.Contains(other.Tracks, trackID) {
				e.mu.Unlock()
				return fmt.Errorf("this track is in the trash for multiple lists; re-adding it here would affect them")
			}
		}
	}
	oldSource, oldTrack := *s, *t
	keys := trackMatchIDs(trackID, t)
	path := e.readdKnownAudio(ctx, trackID, t, keys)
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	if path == "" && t.Local {
		e.mu.Unlock()
		return fmt.Errorf("no download source for this local track")
	}
	s.Excluded = maps.Clone(s.Excluded)
	delete(s.Excluded, trackID)
	s.Readded = maps.Clone(s.Readded)
	if s.Readded == nil {
		s.Readded = make(map[string]bool)
	}
	s.Readded[trackID] = true
	if !slices.Contains(s.Tracks, trackID) {
		s.Tracks = append(slices.Clone(s.Tracks), trackID)
		if s.Order != nil {
			s.Order = append(slices.Clone(s.Order), trackID)
		}
	}
	if t.PermanentlyRemoved {
		t.PermanentlyRemoved = false
		t.ReaddGeneration++
	}
	t.Path, t.Error, t.Fails = path, "", 0
	if path == "" {
		t.Status, t.Lyrics = manifest.StatusPending, ""
	} else {
		t.Status = manifest.StatusDone
	}
	if err := e.m.Save(); err != nil {
		*s, *t = oldSource, oldTrack
		e.mu.Unlock()
		return fmt.Errorf("could not save re-added track: %w", err)
	}
	if path != "" {
		e.presence = nil
		err := e.writeReaddedPlaylist(s)
		event := Event{Kind: EvDone, ID: trackID, Title: t.Title}
		e.mu.Unlock()
		emit(event)
		return err
	}
	e.running = true
	e.physicalIndex, e.physicalFiles = nil, nil
	do := e.trackWork
	if do == nil {
		do = func(ctx context.Context, id string, emit func(Event)) bool {
			return e.downloadTrack(ctx, id, emit, false)
		}
	}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.physicalIndex = nil
		e.mu.Unlock()
	}()
	emit(Event{Kind: EvPlan, Total: 1})
	reused, err := e.readdIndexedAudio(ctx, trackID, keys, emit)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if !reused {
		e.worker(ctx, trackID, emit, do)
	}
	e.mu.Lock()
	err = e.writeReaddedPlaylist(s)
	e.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (e *Engine) writeReaddedPlaylist(s *manifest.Source) error {
	if err := e.m.WriteSourcePlaylist(s); err != nil {
		return fmt.Errorf("track saved, but could not update this list's export: %w", err)
	}
	return nil
}
