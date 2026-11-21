package engine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"ytmanager/internal/ytdlp"
)

const trashDir = ".ytmanager-trash"

// ScanLibrary reconciles physical files with records without modifying either.
// Missing records remain visible. Trash is never part of the active library.
func (e *Engine) ScanLibrary(ctx context.Context) ([]LibraryTrack, error) {
	managed := e.Library()
	byPath := map[string][]LibraryTrack{}
	bySource := map[string][]LibraryTrack{}
	for _, t := range managed {
		byPath[filepath.Clean(t.Path)] = append(byPath[filepath.Clean(t.Path)], t)
		bySource[t.ID] = append(bySource[t.ID], t)
	}
	e.mu.Lock()
	for id, t := range e.m.Tracks {
		if t.Alt != "" {
			bySource[t.Alt] = append(bySource[t.Alt], bySource[id]...)
		}
	}
	e.mu.Unlock()
	var rows []LibraryTrack
	var files []scanFile
	var playlists []string
	seen := map[string]bool{}
	var failures []error
	root := e.Root()
	e.scanDone.Store(0)
	e.scanTotal.Store(0)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failures = append(failures, err)
			return nil
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), trashDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".m3u" || ext == ".m3u8" {
			playlists = append(playlists, p)
			return nil
		}
		if !audioExtension(p) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		stat, err := d.Info()
		if err != nil {
			failures = append(failures, err)
			return nil
		}
		if !stat.Mode().IsRegular() {
			return nil
		}
		entries := byPath[rel]
		if len(entries) == 0 {
			for expected, candidates := range byPath {
				if strings.EqualFold(expected, rel) {
					if entries != nil {
						entries = nil
						break
					}
					entries = candidates
				}
			}
		}
		if len(entries) == 0 {
			entries = []LibraryTrack{{ID: "file:" + rel, Title: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Unmanaged: true}}
		}
		files = append(files, scanFile{p, rel, stat, entries})
		e.scanTotal.Add(1)
		return nil
	})
	// Probing and hashing dominate on USB and MTP; a few workers keep the scan responsive.
	var mu sync.Mutex
	work := make(chan scanFile)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				got := e.scanOne(ctx, f, bySource)
				mu.Lock()
				for _, row := range got {
					seen[row.ID] = true
				}
				rows = append(rows, got...)
				mu.Unlock()
				e.scanDone.Add(1)
			}
		}()
	}
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		work <- f
	}
	close(work)
	wg.Wait()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		failures = append(failures, err)
	}
	for _, row := range managed {
		if !seen[row.ID] {
			row.Duplicate = false
			if !row.Missing {
				row.Status = "not scanned"
				row.Note = "File was not included in this scan; rescan before changing it"
			}
			rows = append(rows, row)
		}
	}
	// Local playlists also protect external music. An incomplete scan must never
	// classify files as safe cleanup candidates.
	refs := map[string]bool{}
	aliases := map[string][]string{}
	for _, p := range playlists {
		if ctx.Err() != nil {
			break
		}
		f, err := os.Open(p)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 4096), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "file://") {
				if u, err := url.Parse(line); err == nil {
					line = u.Path
				}
			}
			line = filepath.FromSlash(strings.ReplaceAll(line, "\\", "/"))
			if !filepath.IsAbs(line) {
				line = filepath.Join(filepath.Dir(p), line)
			}
			rel, err := filepath.Rel(root, line)
			if err == nil && filepath.IsLocal(rel) {
				rel = filepath.Clean(rel)
				if !refs[rel] {
					refs[rel] = true
					key := strings.ToLower(rel)
					aliases[key] = append(aliases[key], rel)
				}
			}
		}
		if err := sc.Err(); err != nil {
			failures = append(failures, err)
		}
		f.Close()
	}
	if ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		failures = append(failures, ctx.Err())
	}
	for i := range rows {
		path := rows[i].Path
		if refs[path] || playlistMatchesAlias(root, path, aliases[strings.ToLower(path)]) {
			rows[i].Orphan = false
			if rows[i].References == 0 {
				rows[i].Sources = "Local playlist"
				rows[i].References = 1
			}
		}
		if len(failures) > 0 {
			rows[i].Orphan = false
		}
	}
	markDuplicates(rows)
	e.mu.Lock()
	e.presence = map[string]bool{}
	for _, row := range rows {
		e.presence[row.Path] = !row.Missing && row.Size > 0
	}
	e.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Title != rows[j].Title {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, errors.Join(failures...)
}

type scanFile struct {
	path, rel string
	stat      os.FileInfo
	entries   []LibraryTrack
}

// ScanProgress reports files probed and files found by the current or last scan.
func (e *Engine) ScanProgress() (done, total int) {
	return int(e.scanDone.Load()), int(e.scanTotal.Load())
}

func (e *Engine) scanOne(ctx context.Context, f scanFile, bySource map[string][]LibraryTrack) []LibraryTrack {
	p, rel, stat, entries := f.path, f.rel, f.stat, f.entries
	var rows []LibraryTrack
	info, probeErr := ytdlp.Probe(ctx, p)
	if ctx.Err() != nil {
		return nil
	}
	hash, hashErr := hashFile(ctx, p)
	if ctx.Err() != nil {
		return nil
	}
	for _, row := range entries {
		row.Path, row.Size, row.Modified = rel, stat.Size(), stat.ModTime().UnixNano()
		row.Missing, row.Invalid, row.Duplicate, row.Note = false, false, false, ""
		row.Status, row.Hash = "present", hash
		if probeErr != nil {
			row.Invalid = errors.Is(probeErr, ytdlp.ErrInvalidAudio)
			row.Status = "unreadable"
			if row.Invalid {
				row.Status = "invalid"
			}
			row.Note = probeErr.Error()
		} else {
			if info.Tags["title"] != "" {
				row.Title = info.Tags["title"]
			}
			row.Artist, row.AlbumArtist = info.Tags["artist"], info.Tags["album_artist"]
			row.Creators = row.Artist
			if info.Tags["album"] != "" {
				row.Album = info.Tags["album"]
			}
			row.TrackNumber = info.Tags["track"]
			row.SourceID = embeddedID(info.Tags)
			row.Duration, row.Codec, row.Container = info.Duration, info.Codec, info.Container
			row.Bitrate, row.SampleRate, row.Channels = info.Bitrate, info.SampleRate, info.Channels
			row.FormatMismatch = !codecMatchesExtension(row.Codec, filepath.Ext(rel))
			if row.FormatMismatch {
				row.Note = "Extension does not match audio codec"
			}
			// A moved file with a known embedded ID is protected from orphan cleanup.
			if row.Unmanaged {
				id := embeddedID(info.Tags)
				for _, t := range bySource[id] {
					row.References = t.References
					row.Sources = t.Sources
					row.Note += " · Known source ID; run repair to relink"
				}
			}
		}
		if hashErr != nil {
			row.Note += " · Cannot compare bytes: " + hashErr.Error()
		}
		row.Orphan = row.References == 0
		rows = append(rows, row)
	}
	return rows
}

func normalizeTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

func markDuplicates(rows []LibraryTrack) {
	hashes, titles := map[string][]int{}, map[string][]int{}
	for i, row := range rows {
		if row.Missing {
			continue
		}
		if row.Hash != "" {
			hashes[row.Hash] = append(hashes[row.Hash], i)
		}
		key := normalizeTitle(row.Artist) + "/" + normalizeTitle(row.Title)
		if normalizeTitle(row.Title) != "" {
			titles[key] = append(titles[key], i)
		}
	}
	for key, group := range titles {
		if len(group) < 2 {
			continue
		}
		for _, i := range group {
			rows[i].Duplicate = true
			rows[i].DuplicateGroup = key
			rows[i].Note += " · Same artist/title; compare editions"
		}
	}
	for hash, group := range hashes {
		if len(group) < 2 {
			continue
		}
		for _, i := range group {
			rows[i].Duplicate = true
			rows[i].DuplicateGroup = hash
			rows[i].Note += " · Identical file bytes (SHA-256)"
		}
	}
}

func codecMatchesExtension(codec, ext string) bool {
	switch strings.ToLower(ext) {
	case ".opus":
		return codec == "opus"
	case ".m4a", ".mp4":
		return codec == "aac" || codec == "alac"
	case ".mp3":
		return codec == "mp3"
	case ".flac":
		return codec == "flac"
	case ".aac":
		return codec == "aac"
	case ".alac":
		return codec == "alac"
	case ".ogg", ".oga":
		return codec == "vorbis" || codec == "opus" || codec == "flac"
	case ".wav", ".aiff", ".aif":
		return strings.HasPrefix(codec, "pcm_")
	case ".webm", ".weba":
		return codec == "opus" || codec == "vorbis"
	}
	return true
}

func embeddedID(tags map[string]string) string {
	for _, key := range []string{"purl", "comment", "url"} {
		for _, value := range strings.Fields(tags[key]) {
			if id := ytdlp.TrackReferenceID(value); id != "" {
				return id
			}
		}
	}
	return ""
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, scanReader{ctx, f}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

type scanReader struct {
	ctx context.Context
	r   io.Reader
}

func (r scanReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
