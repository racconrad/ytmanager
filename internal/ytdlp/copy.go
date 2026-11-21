package ytdlp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ytmanager/internal/device"
)

// CopyFile copies without preserving times or permissions (MTP supports neither) and removes a
// partial copy on failure.
func CopyFile(src, dst string) error {
	return copyFile(src, dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
}

func LyricsPath(audio string) string {
	return strings.TrimSuffix(audio, filepath.Ext(audio)) + ".lrc"
}

func copyDownload(src, dst string) (string, error) {
	ext := filepath.Ext(dst)
	base := dst[:len(dst)-len(ext)]
	for n := 1; ; n++ {
		path := dst
		if n > 1 {
			path = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		if _, err := os.Lstat(LyricsPath(path)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		err := copyFile(src, path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if !errors.Is(err, os.ErrExist) {
			return path, err
		}
	}
}

func copyFile(src, dst string, flags int) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	stat, err := in.Stat()
	if err != nil {
		return err
	}
	if err := device.RequireSpace(filepath.Dir(dst), uint64(stat.Size())); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, flags, 0o666)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
