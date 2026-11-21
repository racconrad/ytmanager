package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"

	"ytmanager/internal/lyrics"
	"ytmanager/internal/ytdlp"
)

// download enriches the info-json (artist, album, track number, lyrics) and hands it to yt-dlp.
func (e *Engine) download(ctx context.Context, sourceID, referenceID, provider string, res TrackResolution, album string, n int, format, root string, stage func(string), progress func(float64)) (string, string, error) {
	stage("Reading metadata")
	info, err := ytdlp.Info(ctx, res.TargetURL, e.opt.Cookies)
	if err != nil {
		return "", "", fmt.Errorf("reading song information: %w", err)
	}
	if cover, _ := info["thumbnail"].(string); cover != "" {
		e.rememberAlbumCover(sourceID, cover)
	}
	refURL := res.TargetURL
	if canonical, ok := info["webpage_url"].(string); ok && ytdlp.Provider(canonical) == provider {
		refURL = canonical
	}
	ref := ytdlp.TrackReference(refURL, referenceID)
	info["meta_purl"], info["meta_comment"] = ref, ref
	e.prepareProviderInfo(provider, info)
	artist, title := tag(info, album, n, e.opt)
	dur, _ := info["duration"].(float64)
	if res.LyricsID == "" {
		e.mu.Lock()
		if t := e.m.Tracks[sourceID]; t != nil {
			if title != "" {
				t.Title = title
			}
			if dur > 0 {
				t.Duration = dur
			}
		}
		e.mu.Unlock()
	}
	album, _ = info["album"].(string)

	stage("Looking up lyrics")
	lr, hasLyr := lyrics.Find(ctx, artist, title, album, dur, res.LyricsID)
	if hasLyr {
		info["meta_lyrics"] = lr.Text
	}

	tmp, err := os.CreateTemp("", "ytm-*.info.json")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(info)
	tmp.Close()
	if err != nil {
		return "", "", err
	}

	stage("Downloading")
	stageID := res.DownloadID
	if sourceID != res.DownloadID {
		stageID = fmt.Sprintf("%x", sha256.Sum256([]byte(sourceID+"\x00"+res.DownloadID)))
	}
	path, err := ytdlp.Download(ctx, stageID, tmp.Name(), root, format, e.opt.Folders, e.opt.Cookies, progress)
	if err != nil {
		return "", "", fmt.Errorf("downloading or processing audio: %w", err)
	}
	if e.opt.Folders == "" && album != "" {
		_ = ytdlp.EnsureAlbumCover(ctx, path, path)
	}
	kind := ""
	if hasLyr {
		kind = "plain"
		if lr.Synced && writeLyrics(path, lr.Text) == nil {
			kind = "synced"
		}
	}
	return path, kind, nil
}

func writeLyrics(audio, text string) error {
	path := ytdlp.LyricsPath(audio)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
