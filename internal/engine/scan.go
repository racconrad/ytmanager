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
	return e.scanLibrary(ctx, nil)
}

func (e *Engine) scanLibrary(ctx context.Context, discover func([]LibraryTrack)) ([]LibraryTrack, error) {
	managed, removed, root := e.libraryRecords()
	byPath := map[string][]LibraryTrack{}
	bySource := map[string][]LibraryTrack{}
	for _, t := range managed {
		byPath[filepath.Clean(t.Path)] = append(byPath[filepath.Clean(t.Path)], t)
		bySource[t.ID] = append(bySource[t.ID], t)
	}
	// Case-only variants (MTP, FAT) match one managed path, never an ambiguous pair.
	byFold := map[string][]string{}
	for p := range byPath {
		byFold[strings.ToLower(p)] = append(byFold[strings.ToLower(p)], p)
	}
	e.mu.Lock()
	for id, t := range e.m.Tracks {
		if t.Alt != "" {
			bySource[t.Alt] = append(bySource[t.Alt], bySource[id]...)
		}
		if t.ResolvedID != "" && t.ResolvedID != id {
			bySource[t.ResolvedID] = append(bySource[t.ResolvedID], bySource[id]...)
		}
	}
	e.mu.Unlock()
	var rows []LibraryTrack
	seen := map[string]bool{}
	e.scanDone.Store(0)
	e.scanTotal.Store(0)
	files, playlists, failures := e.discoverFiles(ctx, root, byPath, byFold, discover)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		select {
		case work <- f:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, row := range managed {
		if seen[row.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, keep := libraryTrackOnDisk(root, row, removed[row.ID])
		if !keep {
			continue
		}
		if !row.Missing {
			row.Status = "not scanned"
			row.Note = "File was not included in this scan; rescan before changing it"
		}
		rows = append(rows, row)
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
			if ctx.Err() != nil {
				break
			}
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
	if err := ctx.Err(); err != nil {
		return nil, err
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
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Title != rows[j].Title {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		}
		return rows[i].ID < rows[j].ID
	})
	if err := errors.Join(failures...); err != nil {
		return rows, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	walked := make(map[string]bool, len(files))
	for _, f := range files {
		walked[f.rel] = true
	}
	e.probes.prune(walked)
	_ = e.probes.save() // the cache only speeds up the next scan; losing it is harmless
	presence := make(map[string]bool, len(rows))
	for _, row := range rows {
		presence[row.Path] = !row.Missing && row.Size > 0
	}
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.presence = presence
	e.mu.Unlock()
	return rows, nil
}

// ScanProgress reports files probed and files found by the current or last scan.
func (e *Engine) ScanProgress() (done, total int) {
	return int(e.scanDone.Load()), int(e.scanTotal.Load())
}

func (e *Engine) scanOne(ctx context.Context, f scanFile, bySource map[string][]LibraryTrack) []LibraryTrack {
	p, rel, stat, entries := f.path, f.rel, f.stat, f.entries
	var rows []LibraryTrack
	cached, hit := e.probes.get(rel, stat)
	info, hash := cached.Info, cached.Hash
	var probeErr, hashErr error
	if !hit {
		info, probeErr = ytdlp.Probe(ctx, p)
		if ctx.Err() != nil {
			return nil
		}
	}
	if hash == "" {
		hash, hashErr = hashFile(ctx, p)
		if ctx.Err() != nil {
			return nil
		}
	}
	// Failures are not cached so a reconnected device or installed tool is retried.
	if probeErr == nil && hashErr == nil && (!hit || cached.Hash == "") {
		e.probes.put(rel, stat, info, hash)
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
		if row.Hash != "" && row.Size > 0 {
			hashes[row.Hash] = append(hashes[row.Hash], i)
		}
		// Skip unreadable/corrupt files from title-only duplicate matching: they are already
		// reported as corrupt and often represent incomplete downloads, not true duplicates.
		if row.Invalid || row.Status == "unreadable" {
			continue
		}
		key := normalizeTitle(row.Artist) + "/" + normalizeTitle(row.Title)
		if normalizeTitle(row.Title) != "" {
			titles[key] = append(titles[key], i)
		}
	}
	for key, group := range titles {
		if !hasDistinctPaths(rows, group) {
			continue
		}
		matched := map[int]bool{}
		for a := 0; a < len(group); a++ {
			for b := a + 1; b < len(group); b++ {
				ia, ib := group[a], group[b]
				if isLikelyDuplicatePair(rows[ia], rows[ib]) {
					matched[ia] = true
					matched[ib] = true
				}
			}
		}
		for i := range matched {
			rows[i].Duplicate = true
			rows[i].DuplicateGroup = key
			rows[i].Note += " · Same artist/title; compare editions"
		}
	}
	for hash, group := range hashes {
		if !hasDistinctPaths(rows, group) {
			continue
		}
		for _, i := range group {
			rows[i].Duplicate = true
			rows[i].DuplicateGroup = hash
			rows[i].Note += " · Identical file bytes (SHA-256)"
		}
	}
}

func isLikelyDuplicatePair(a, b LibraryTrack) bool {
	if strings.EqualFold(filepath.Clean(a.Path), filepath.Clean(b.Path)) {
		return false
	}
	// Different durations (>8s) indicate an incomplete cut, snippet, or distinct edition.
	if a.Duration > 0 && b.Duration > 0 {
		diff := a.Duration - b.Duration
		if diff < 0 {
			diff = -diff
		}
		if diff > 8 {
			return false
		}
	}
	// Distinct known YouTube/source IDs on different albums (or >4s apart) are different tracks/editions.
	if a.SourceID != "" && b.SourceID != "" && a.SourceID != b.SourceID {
		albA, albB := normalizeTitle(a.Album), normalizeTitle(b.Album)
		if albA != "" && albB != "" && albA != albB {
			return false
		}
		if a.Duration > 0 && b.Duration > 0 {
			diff := a.Duration - b.Duration
			if diff < 0 {
				diff = -diff
			}
			if diff > 4 {
				return false
			}
		}
	}
	return true
}

func hasDistinctPaths(rows []LibraryTrack, indices []int) bool {
	if len(indices) < 2 {
		return false
	}
	first := strings.ToLower(filepath.Clean(rows[indices[0]].Path))
	for _, idx := range indices[1:] {
		if strings.ToLower(filepath.Clean(rows[idx].Path)) != first {
			return true
		}
	}
	return false
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
