// Package ytdlp wraps the yt-dlp binary: list sources, read metadata and download audio.
package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func base(cookies string) []string {
	a := []string{"--no-warnings", "--color", "never"}
	if cookies != "" {
		a = append(a, "--cookies-from-browser", Keyring(cookies, runtime.GOOS, os.Getenv("XDG_CURRENT_DESKTOP")))
	}
	return a
}

func output(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "yt-dlp", args...).Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil, processError(err, lastErr(string(ee.Stderr)))
	}
	return out, err
}

func lastErr(stderr string) string {
	var d diagnostic
	for _, line := range strings.Split(stderr, "\n") {
		d.add(line)
	}
	return d.detail()
}

// Info returns the full info-json of a track so it can be enriched before downloading.
func Info(ctx context.Context, target, cookies string) (map[string]any, error) {
	spec := lookupProvider(Provider(target))
	if spec == nil {
		return nil, fmt.Errorf("unsupported track URL")
	}
	out, err := output(ctx, append(base(cookies), "-J", "--no-playlist", "--", target)...)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	if m == nil || m["_type"] == "playlist" || m["_type"] == "multi_video" || m["entries"] != nil {
		return nil, errors.New("expected a single track, not a collection")
	}
	if spec.sanitizeInfo != nil {
		if err := spec.sanitizeInfo(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func MusicURL(id string) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(id)
}
