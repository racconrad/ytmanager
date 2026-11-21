package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"ytmanager/internal/device"
)

// ErrInvalidAudio identifies files that need to be downloaded again, rather than
// temporary device, permission or tool failures.
var ErrInvalidAudio = errors.New("invalid audio file")

// ErrMissingAudio distinguishes a missing library file from a failed temporary copy.
var ErrMissingAudio = errors.New("missing audio file")

// Tags reads format and stream tags, including Opus stream metadata. Phone files
// are staged locally because ffprobe may need seeking that MTP cannot provide.
func Tags(ctx context.Context, path string) (map[string]string, error) {
	info, err := Probe(ctx, path)
	return info.Tags, err
}

type AudioInfo struct {
	Tags                          map[string]string
	Container, Codec              string
	Duration                      float64
	Bitrate, SampleRate, Channels int
	Cover                         bool
}

// Probe inspects the actual audio stream, rather than trusting its extension.
func Probe(ctx context.Context, path string) (AudioInfo, error) {
	var info AudioInfo
	if err := ctx.Err(); err != nil {
		return info, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return info, tagReadError(path, err)
	}
	if stat.Size() == 0 {
		return info, fmt.Errorf("audio file is empty; sync (Enter) to restore it: %w", ErrInvalidAudio)
	}
	input := path
	if device.IsMTP(path) {
		local, cleanup, err := stageProbe(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return info, ctx.Err()
			}
			return info, fmt.Errorf("cannot copy phone audio for tag reading; check the USB connection and free space in the computer's temporary folder: %w", err)
		}
		defer cleanup()
		input = local
	}
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=format_name,duration,bit_rate:format_tags:stream=codec_type,codec_name,duration,bit_rate,sample_rate,channels:stream_tags:stream_disposition=attached_pic", "-of", "json", "-i", input).Output()
	if ctx.Err() != nil {
		return info, ctx.Err()
	}
	if err != nil {
		return info, probeError(input, err)
	}
	var r struct {
		Format struct {
			Tags     map[string]string
			Name     string `json:"format_name"`
			Duration string
			Bitrate  string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			Tags        map[string]string
			Type        string `json:"codec_type"`
			Codec       string `json:"codec_name"`
			Duration    string
			Bitrate     string `json:"bit_rate"`
			SampleRate  string `json:"sample_rate"`
			Channels    int
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return info, fmt.Errorf("ffprobe returned invalid metadata: %w", err)
	}
	tags := map[string]string{}
	for _, stream := range r.Streams {
		if stream.Type == "video" && stream.Disposition.AttachedPic == 1 {
			info.Cover = true
		}
		if stream.Type != "audio" {
			continue
		}
		for k, v := range stream.Tags {
			tags[strings.ToLower(k)] = v
		}
		if stream.Type == "audio" && info.Codec == "" {
			info.Codec = stream.Codec
			info.Duration, _ = strconv.ParseFloat(stream.Duration, 64)
			info.Bitrate, _ = strconv.Atoi(stream.Bitrate)
			info.SampleRate, _ = strconv.Atoi(stream.SampleRate)
			info.Channels = stream.Channels
		}
	}
	if info.Codec == "" {
		return info, fmt.Errorf("no audio stream: %w", ErrInvalidAudio)
	}
	for k, v := range r.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	info.Container = r.Format.Name
	if info.Duration == 0 {
		info.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	}
	if info.Bitrate == 0 && info.Duration > 0 {
		info.Bitrate = int(float64(stat.Size()) * 8 / info.Duration)
	}
	for alias, key := range map[string]string{"albumartist": "album_artist", "album artist": "album_artist", "tracknumber": "track", "track_number": "track"} {
		if tags[key] == "" && tags[alias] != "" {
			tags[key] = tags[alias]
		}
	}
	info.Tags = tags
	return info, nil
}

func tagReadError(path string, err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("file is missing; sync (Enter) to restore it: %w: %w", ErrMissingAudio, err)
	case device.IsMTP(path):
		return fmt.Errorf("cannot read phone file; unlock the phone, enable USB File transfer and retry: %w", err)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("cannot read audio; check the file's read permissions: %w", err)
	default:
		return fmt.Errorf("cannot read audio file: %w", err)
	}
}

func probeError(input string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("ffprobe is missing; install ffmpeg with ffprobe: %w", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || len(exit.Stderr) == 0 {
		return fmt.Errorf("cannot read audio tags: %w", err)
	}
	detail := strings.Join(strings.Fields(strings.ReplaceAll(string(exit.Stderr), input+": ", "")), " ")
	if strings.Contains(detail, "Invalid data found") || strings.Contains(detail, "End of file") {
		return fmt.Errorf("audio could not be decoded; sync (Enter) to restore it (%s): %w", detail, ErrInvalidAudio)
	}
	return fmt.Errorf("ffprobe could not read audio tags: %s: %w", detail, err)
}

func stageProbe(ctx context.Context, path string) (string, func(), error) {
	in, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer in.Close()
	stat, err := in.Stat()
	if err != nil {
		return "", nil, err
	}
	if err := device.RequireSpace(os.TempDir(), uint64(stat.Size())); err != nil {
		return "", nil, err
	}
	out, err := os.CreateTemp("", "ytm-probe-*"+filepath.Ext(path))
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(out.Name()) }
	_, err = io.Copy(out, probeReader{ctx, in})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return out.Name(), cleanup, nil
}

type probeReader struct {
	ctx context.Context
	io.Reader
}

func (r probeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
