package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func openPermanentRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("destination is no longer a regular folder: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, fmt.Errorf("destination changed since review: %s", path)
	}
	return root, nil
}

func validatePermanentFile(ctx context.Context, root *os.Root, path string, row LibraryTrack) (os.FileInfo, error) {
	parent, leaf, err := openPermanentParent(root, path)
	if errors.Is(err, os.ErrNotExist) && row.Missing {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closePermanentParent(root, parent)
	st, err := parent.Lstat(leaf)
	if errors.Is(err, os.ErrNotExist) && row.Missing {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Status == "not scanned" || row.Missing || !st.Mode().IsRegular() || row.Hash == "" || row.Modified == 0 ||
		row.Modified != st.ModTime().UnixNano() || row.Size != st.Size() {
		return nil, fmt.Errorf("file not verified or changed since scan: %s", path)
	}
	f, err := parent.Open(leaf)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, fmt.Errorf("file changed while opening: %s", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, scanReader{ctx, f}); err != nil {
		return nil, err
	}
	if !strings.EqualFold(row.Hash, fmt.Sprintf("%x", hash.Sum(nil))) {
		return nil, fmt.Errorf("file contents changed since scan: %s", path)
	}
	return st, nil
}

func unlinkPermanentFile(root *os.Root, path string, before os.FileInfo) error {
	parent, leaf, err := openPermanentParent(root, path)
	if before == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closePermanentParent(root, parent)
	now, err := parent.Lstat(leaf)
	if before == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if before == nil || !now.Mode().IsRegular() || !os.SameFile(before, now) ||
		before.Size() != now.Size() || !before.ModTime().Equal(now.ModTime()) {
		return fmt.Errorf("file changed since review: %s", path)
	}
	return parent.Remove(leaf)
}

// os.Root permits symlinks within its boundary. Pin each reviewed directory
// before accessing its child so a redirected ancestor cannot change the target.
func openPermanentParent(root *os.Root, path string) (*os.Root, string, error) {
	parent := root
	for part := range strings.SplitSeq(filepath.Dir(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		info, err := parent.Lstat(part)
		if err != nil || !info.IsDir() {
			closePermanentParent(root, parent)
			if err != nil {
				return nil, "", err
			}
			return nil, "", fmt.Errorf("unsafe folder in path: %s", path)
		}
		child, err := parent.OpenRoot(part)
		closePermanentParent(root, parent)
		if err != nil {
			return nil, "", err
		}
		actual, err := child.Stat(".")
		if err != nil || !os.SameFile(info, actual) {
			child.Close()
			return nil, "", fmt.Errorf("folder changed since review: %s", path)
		}
		parent = child
	}
	return parent, filepath.Base(path), nil
}

func closePermanentParent(root, parent *os.Root) {
	if parent != root {
		parent.Close()
	}
}
