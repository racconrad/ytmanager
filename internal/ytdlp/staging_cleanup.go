package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const discardSuffix = ".discard"

var stageName = regexp.MustCompile("^[0-9a-f]{64}(-[1-9][0-9]*)?$")

// CleanupAbandonedStages retries disposal only for stages explicitly marked by a finished download.
func CleanupAbandonedStages(ctx context.Context, destination string) (int, error) {
	root, err := openStageRoot(destination, false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	var removed int
	var firstErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		key, ok := strings.CutSuffix(entry.Name(), discardSuffix)
		if !ok || !stageName.MatchString(key) || !entry.Type().IsRegular() {
			continue
		}
		ok, err := cleanupMarkedStage(root, key)
		if ok {
			removed++
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return removed, firstErr
}

func cleanupMarkedStage(root *os.Root, key string) (bool, error) {
	lock, err := openStageLock(root, key)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if ok, err := tryStageLock(lock); !ok || err != nil {
		return false, err
	}
	return removeDiscardedStage(root, key)
}

func discardStage(root *os.Root, key string) {
	// Keep the marker outside the stage so an interrupted RemoveAll cannot erase it first.
	if err := createStageMarker(root, key+discardSuffix); err == nil {
		_, _ = removeDiscardedStage(root, key)
	}
}

func removeDiscardedStage(root *os.Root, key string) (bool, error) {
	marker := key + discardSuffix
	info, err := root.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return false, fmt.Errorf("invalid download staging marker: %s", marker)
	}
	if _, err := root.Lstat(key + runningSuffix); !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	info, err = root.Lstat(key)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if exists && !info.IsDir() {
		return false, fmt.Errorf("not a download staging directory: %s", key)
	}
	if err := root.RemoveAll(key); err != nil {
		return false, err
	}
	return exists, root.Remove(marker)
}
