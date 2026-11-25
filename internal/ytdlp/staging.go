package ytdlp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const runningSuffix = ".running"

func stageDir(destination string) string {
	scope := sha256.Sum256(fmt.Appendf(nil, "%d\x00%s\x00%s", os.Geteuid(), os.Getenv("LOCALAPPDATA"), filepath.Clean(destination)))
	return filepath.Join(os.TempDir(), fmt.Sprintf("ytmanager-downloads-v1-%x", scope[:16]))
}

func openStageRoot(destination string, create bool) (*os.Root, error) {
	dir := stageDir(destination)
	if create {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a private download staging directory: %s", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, fmt.Errorf("download staging directory changed: %s", dir)
	}
	if !ownsStageRoot(root, actual) {
		root.Close()
		return nil, fmt.Errorf("not a private download staging directory: %s", dir)
	}
	return root, nil
}

func stageKey(id, format, folders string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(id+"\x00"+format+"\x00"+folders)))
}

func prepareStage(ctx context.Context, root *os.Root, key string) (string, error) {
	base := key
	for next := 1; ; next++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// A child may survive its parent crashing, but the parent's lock does not.
		_, err := root.Lstat(key + runningSuffix)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		key = fmt.Sprintf("%s-%d", base, next)
	}
	if _, err := removeDiscardedStage(root, key); err != nil {
		return "", fmt.Errorf("could not discard the previous download: %w", err)
	}
	if err := root.Mkdir(key, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := root.Lstat(key)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a download staging directory: %s", key)
	}
	if err := createStageMarker(root, key+runningSuffix); err != nil {
		return "", err
	}
	return key, nil
}

func createStageMarker(root *os.Root, name string) error {
	marker, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	return marker.Close()
}

func openStageLock(root *os.Root, key string) (*os.File, error) {
	// Shards keep lock files bounded without unlinking inodes other processes may hold.
	return root.OpenFile(key[:2]+".lock", os.O_CREATE|os.O_RDWR, 0600)
}

func waitStageLock(ctx context.Context, root *os.Root, key string) (*os.File, error) {
	lock, err := openStageLock(root, key)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var acquired bool
		if acquired, err = tryStageLock(lock); acquired {
			return lock, nil
		} else if err != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	lock.Close()
	return nil, err
}
