package engine

import (
	"context"
	"path/filepath"
	"strings"
)

// DeleteTracks is the managed-track shortcut; files are recoverable in trash.
func (e *Engine) DeleteTracks(ids []string) (int, error) {
	e.mu.Lock()
	var rows []LibraryTrack
	seen := map[string]bool{}
	for _, id := range ids {
		if t := e.m.Tracks[id]; t != nil && t.Path != "" && !seen[id] {
			rows = append(rows, LibraryTrack{ID: id, Title: t.Title, Path: t.Path})
			seen[id] = true
		}
	}
	e.mu.Unlock()
	report, err := e.TrashFiles(context.Background(), rows)
	return report.Tracks, err
}

func audioExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".opus", ".m4a", ".mp3", ".flac", ".wav", ".ogg", ".oga", ".aac", ".alac", ".aiff", ".aif", ".webm", ".weba", ".mp4":
		return true
	}
	return false
}
