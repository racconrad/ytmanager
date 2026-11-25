package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Sanitize before truncating UTF-8: Windows-safe replacements can add bytes.
var naming = []string{
	"--windows-filenames",
	"--parse-metadata", "%(album_artist|Unknown)S:%(ytm_file_artist)s",
	"--parse-metadata", "%(album|Singles)S:%(ytm_file_album)s",
	"--parse-metadata", "%(track,title)S:%(ytm_file_title)s",
}

// layouts are the output templates for each Options.Folders value.
// Leave headroom in each component for extensions, partial files and duplicate suffixes.
var layouts = map[string]string{
	"":       "%(ytm_file_artist).220B/%(ytm_file_album).220B/%(ytm_prefix|).24B%(ytm_file_title).196B.%(ext)s",
	"artist": "%(ytm_file_artist).220B/%(ytm_file_title).220B.%(ext)s",
	"flat":   "%(ytm_file_artist).64B - %(ytm_file_title).153B.%(ext)s",
}

// Name returns the path, relative to the destination, that Download gives a file with these tags
// in the folders layout. yt-dlp builds it offline, so it matches new downloads exactly.
func Name(ctx context.Context, tags map[string]string, ext, folders string) (string, error) {
	if tags["title"] == "" {
		return "", errors.New("the file has no title tag")
	}
	info := map[string]any{"id": "x", "title": tags["title"], "track": tags["title"], "ext": ext, "ytm_prefix": "",
		"url": "http://localhost/x", "webpage_url": "http://localhost/x", "extractor": "generic", "extractor_key": "Generic"}
	for _, k := range []string{"album_artist", "album"} {
		if tags[k] != "" {
			info[k] = tags[k]
		}
	}
	if info["album_artist"] == nil && tags["artist"] != "" {
		info["album_artist"] = tags["artist"]
	}
	if n, err := strconv.Atoi(strings.Split(tags["track"], "/")[0]); err == nil { // "2" or "2/12"
		info["ytm_prefix"] = fmt.Sprintf("%02d - ", n)
	}
	tmp, err := os.CreateTemp("", "ytm-*.info.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(info)
	tmp.Close()
	if err != nil {
		return "", err
	}
	args := append([]string{"--no-warnings", "--load-info-json", tmp.Name(), "--skip-download", "-o", layouts[folders], "--print", "filename"}, naming...)
	out, err := output(ctx, args...)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out)))), nil
}
