package ytdlp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"ytmanager/internal/device"
)

// Convert writes to a caller-owned temporary path. The original is never changed.
func Convert(ctx context.Context, input, output, format string) error {
	if !ValidFormat(format) {
		return fmt.Errorf("invalid target format: %s", format)
	}
	info, err := Probe(ctx, input)
	if err != nil {
		return err
	}
	if device.IsMTP(input) {
		local, cleanup, err := stageProbe(ctx, input)
		if err != nil {
			return err
		}
		defer cleanup()
		input = local
	}
	cover := ""
	// M4A's standard metadata mode retains attached artwork. Preserve source IDs
	// in the standard comment field as well, since arbitrary tags are not kept.
	if format == "m4a" && info.Tags["purl"] != "" && !strings.Contains(info.Tags["comment"], info.Tags["purl"]) {
		info.Tags["comment"] = strings.TrimSpace(info.Tags["comment"] + "\n" + info.Tags["purl"])
	}
	if info.Cover {
		cover = filepath.Join(filepath.Dir(output), "cover.jpg")
		data, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-n", "-i", input, "-map", "0:v:0", "-frames:v", "1", "-c:v", "mjpeg", cover).CombinedOutput()
		if err != nil {
			return fmt.Errorf("preserving cover art: %s: %w", strings.TrimSpace(string(data)), err)
		}
		defer os.Remove(cover)
		if format == "opus" {
			picture, err := opusPicture(cover)
			if err != nil {
				return err
			}
			info.Tags["metadata_block_picture"] = picture
		}
	}
	// Merge stream and container metadata so Opus tags survive conversion as well.
	var metadata strings.Builder
	metadata.WriteString(";FFMETADATA1\n")
	escape := strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\n", "\\\n", "\r", "")
	keys := make([]string, 0, len(info.Tags))
	for k := range info.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "encoder" || k == "duration" {
			continue
		}
		fmt.Fprintf(&metadata, "%s=%s\n", escape.Replace(k), escape.Replace(info.Tags[k]))
	}
	meta, err := os.CreateTemp(filepath.Dir(output), "metadata-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(meta.Name())
	if _, err = meta.WriteString(metadata.String()); err != nil {
		meta.Close()
		return err
	}
	if err = meta.Close(); err != nil {
		return err
	}
	args := []string{"-nostdin", "-v", "error", "-xerror", "-n", "-i", input, "-f", "ffmetadata", "-i", meta.Name()}
	if cover != "" && format != "opus" {
		args = append(args, "-i", cover)
	}
	args = append(args, "-map", "0:a:0", "-map_metadata", "1")
	if cover != "" && format != "opus" {
		args = append(args, "-map", "2:v:0", "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}
	switch format {
	case "opus":
		args = append(args, "-c:a", "libopus", "-b:a", "160k")
	case "m4a":
		args = append(args, "-c:a", "aac", "-b:a", "256k", "-movflags", "+faststart")
	case "mp3":
		args = append(args, "-c:a", "libmp3lame", "-q:a", "0", "-id3v2_version", "3")
	}
	args = append(args, output)
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("converting audio: %s: %w", strings.TrimSpace(string(out)), err)
	}
	verified, err := Probe(ctx, output)
	if err != nil {
		return fmt.Errorf("converted file failed verification: %w", err)
	}
	codec := map[string]string{"opus": "opus", "m4a": "aac", "mp3": "mp3"}[format]
	if verified.Codec != codec {
		return fmt.Errorf("unexpected converted codec %q", verified.Codec)
	}
	if info.Duration > 0 && math.Abs(verified.Duration-info.Duration) > math.Max(0.1, info.Duration*0.01) {
		return fmt.Errorf("converted duration differs from original")
	}
	if info.Cover && !verified.Cover {
		return fmt.Errorf("converted file lost its cover art; original retained")
	}
	// Decode the complete result before allowing the engine to replace a file.
	out, err = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-xerror", "-i", output, "-map", "0:a:0", "-f", "null", "-").CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("converted audio could not be decoded: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Ogg Opus stores artwork as a FLAC picture metadata block.
func opusPicture(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	put := func(n int) { _ = binary.Write(&b, binary.BigEndian, uint32(n)) }
	put(3)
	put(len("image/jpeg"))
	b.WriteString("image/jpeg")
	put(0)
	put(cfg.Width)
	put(cfg.Height)
	put(24)
	put(0)
	put(len(data))
	b.Write(data)
	return base64.StdEncoding.EncodeToString(b.Bytes()), nil
}
