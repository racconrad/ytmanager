package engine

import (
	"fmt"
	"maps"
)

// ExcludeSourceTracks removes memberships from one source without touching audio or other sources.
// A nonzero count means the change was saved, even if refreshing generated playlists failed.
func (e *Engine) ExcludeSourceTracks(sourceID string, ids []string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return 0, fmt.Errorf("wait for the current operation to finish")
	}
	source := e.m.Source(sourceID)
	if source == nil {
		return 0, fmt.Errorf("source %q does not exist", sourceID)
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	drop := make(map[string]bool, len(wanted))
	for _, id := range source.Tracks {
		if wanted[id] {
			drop[id] = true
		}
	}
	if len(drop) == 0 {
		return 0, nil
	}
	previous := *source
	source.Excluded = maps.Clone(source.Excluded)
	if source.Excluded == nil {
		source.Excluded = make(map[string]bool, len(drop))
	}
	maps.Copy(source.Excluded, drop)
	source.DropTracks(drop)
	if err := e.m.Save(); err != nil {
		*source = previous
		return 0, fmt.Errorf("could not save data.music: %w", err)
	}
	if err := e.m.WritePlaylists(); err != nil {
		return len(drop), fmt.Errorf("could not refresh playlists: %w", err)
	}
	return len(drop), nil
}
