package engine

import (
	"os"
	"path/filepath"
)

func playlistMatchesAlias(root, path string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	actual, err := os.Stat(filepath.Join(root, path))
	if err != nil {
		return false
	}
	// Case-folding finds candidates without equating distinct files on case-sensitive volumes.
	for _, candidate := range candidates {
		listed, err := os.Stat(filepath.Join(root, candidate))
		if err == nil && os.SameFile(actual, listed) {
			return true
		}
	}
	return false
}
