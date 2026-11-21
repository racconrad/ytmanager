package ytdlp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"ytmanager/internal/device"
)

// ExtractCover writes the embedded cover art of audioPath as a JPEG to outPath.
func ExtractCover(ctx context.Context, audioPath, outPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input := audioPath
	if device.IsMTP(audioPath) {
		local, cleanup, err := stageProbe(ctx, audioPath)
		if err != nil {
			return err
		}
		defer cleanup()
		input = local
	}
	out, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", input, "-map", "0:v:0", "-frames:v", "1", "-c:v", "mjpeg", outPath).CombinedOutput()
	if err != nil {
		_ = os.Remove(outPath)
		if len(out) > 0 {
			return errors.New(string(out))
		}
		return err
	}
	st, err := os.Stat(outPath)
	if err != nil || st.Size() == 0 {
		_ = os.Remove(outPath)
		return errors.New("audio has no embedded cover art")
	}
	return nil
}

// EnsureAlbumCover places cover.jpg beside dstAudioPath if it does not already exist.
// Existing covers (including user-supplied ones) are never overwritten.
func EnsureAlbumCover(ctx context.Context, dstAudioPath, srcAudioPath string) error {
	if dstAudioPath == "" {
		return nil
	}
	coverPath := filepath.Join(filepath.Dir(dstAudioPath), "cover.jpg")
	if _, err := os.Lstat(coverPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if srcAudioPath == "" {
		srcAudioPath = dstAudioPath
	}
	tmp, err := os.CreateTemp("", "ytm-cover-*.jpg")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := ExtractCover(ctx, srcAudioPath, tmpPath); err != nil {
		return err
	}
	err = copyFile(tmpPath, coverPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
