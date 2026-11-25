package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

type physicalCandidate struct {
	Path        string
	SourceID    string
	Title       string
	Artist      string
	AlbumArtist string
	Album       string
	Duration    float64
}

var (
	featNoise     = regexp.MustCompile(`(?i)\s*[\(\[]\s*(feat\.?|ft\.?|with)\b[^\)\]]*[\)\]]`)
	editionNoise  = regexp.MustCompile(`(?i)(\s*[\(\[][^\)\]]*(remaster|deluxe|explicit|clean|bonus\s*track|anniversary|expanded|special\s*edition|mono|stereo)[^\)\]]*[\)\]]|\s*-\s*(ep|single)\s*$)`)
	trackPrefixRe = regexp.MustCompile(`^\d+\s*[-._]\s*`)
)

// recoverExisting reuses unique, valid embedded IDs (and exact alias/metadata matches)
// before scheduling downloads. Ambiguous editions and deliberate deletions stay untouched.
func (e *Engine) recoverExisting(ctx context.Context, sources []string, emit func(Event)) error {
	e.mu.Lock()
	wanted := map[string][]string{}
	for _, sid := range sources {
		if s := e.m.Source(sid); s != nil {
			for _, id := range s.Tracks {
				t := e.m.Tracks[id]
				if t == nil || t.Local || t.Status == manifest.StatusRemoved || t.Status == manifest.StatusUnavailable {
					continue
				}
				if t.Path != "" && e.m.AudioPath(t) != "" {
					continue
				}
				wanted[id] = trackMatchIDs(id, t)
			}
		}
	}
	index := e.physicalIndex
	files := e.physicalFiles
	e.mu.Unlock()
	if len(wanted) == 0 {
		return nil
	}
	if index == nil {
		var err error
		index, files, err = e.indexExistingAudio(ctx, emit)
		if err != nil {
			return err
		}
		e.mu.Lock()
		e.physicalIndex = index
		e.physicalFiles = files
		e.mu.Unlock()
	}
	e.mu.Lock()
	previous := map[string]manifest.Track{}
	claimedPaths := claimedPhysicalPaths(e.m)
	var fallbackIDs []string
	var duplicateHints []string
	var promoteIDs []string
	for id, keys := range wanted {
		t := e.m.Tracks[id]
		if t == nil || t.Status == manifest.StatusRemoved {
			continue
		}
		chosen := ""
		ambiguous := false
		for _, key := range keys {
			if path, _, ok := e.findExistingForIDLocked(id, key); ok {
				chosen = path
				break
			}
			if paths := index[key]; len(paths) == 1 {
				chosen = paths[0]
				break
			} else if len(paths) > 1 {
				if resolved, ok := resolveCanonicalCandidate(ctx, e.m, id, t, e.opt.Folders, paths); ok {
					chosen = resolved
					duplicateHints = append(duplicateHints, t.Title)
				} else {
					ambiguous = true
				}
				break
			}
		}
		if chosen == "" {
			if !ambiguous {
				fallbackIDs = append(fallbackIDs, id)
			}
			continue
		}
		previous[id] = *t
		t.Path, t.Status, t.Error, t.Fails = chosen, manifest.StatusDone, "", 0
		claimedPaths[strings.ToLower(filepath.Clean(chosen))] = true
		promoteIDs = append(promoteIDs, id)
	}
	sort.Strings(fallbackIDs)
	for _, id := range fallbackIDs {
		t := e.m.Tracks[id]
		if t == nil || t.Status == manifest.StatusRemoved {
			continue
		}
		if matched, ok := matchTrackByMetadata(e.m, id, t, files, claimedPaths); ok {
			previous[id] = *t
			t.Path, t.Status, t.Error, t.Fails = matched, manifest.StatusDone, "", 0
			claimedPaths[strings.ToLower(filepath.Clean(matched))] = true
			promoteIDs = append(promoteIDs, id)
		}
	}
	if len(previous) > 0 {
		if err := e.m.Save(); err != nil {
			for id, t := range previous {
				*e.m.Tracks[id] = t
			}
			e.mu.Unlock()
			return err
		}
		if e.presence != nil {
			for id := range previous {
				e.presence[e.m.Tracks[id].Path] = true
			}
		}
	}
	var recovered []string
	for id := range previous {
		recovered = append(recovered, e.m.Tracks[id].Title)
	}
	e.mu.Unlock()
	for _, id := range promoteIDs {
		e.mu.Lock()
		t := e.m.Tracks[id]
		rel := ""
		if t != nil {
			rel = t.Path
		}
		e.mu.Unlock()
		if rel != "" {
			if promoted, ok := e.promoteSingleToAlbumLocked(ctx, id, rel); ok && promoted != rel {
				emit(Event{Kind: EvLog, Msg: "Moved to album folder: " + promoted})
			}
		}
	}
	for _, title := range duplicateHints {
		emit(Event{Kind: EvLog, Msg: "Duplicate copies on disk; review duplicates (f) in Library: " + title})
	}
	for _, title := range recovered {
		emit(Event{Kind: EvLog, Msg: "Reused existing audio: " + title})
	}
	return nil
}

func (e *Engine) indexExistingAudio(ctx context.Context, emit func(Event)) (map[string][]string, []physicalCandidate, error) {
	index := map[string][]string{}
	var files []physicalCandidate
	emit(Event{Kind: EvLog, Msg: "Checking existing files before downloading missing tracks…"})
	err := filepath.WalkDir(e.Root(), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), trashDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !audioExtension(p) {
			return nil
		}
		rel, _ := filepath.Rel(e.Root(), p)
		stat, err := d.Info()
		if err != nil {
			return nil
		}
		cached, hit := e.probes.get(rel, stat)
		info := cached.Info
		if !hit {
			if info, err = ytdlp.Probe(ctx, p); ctx.Err() != nil {
				return ctx.Err()
			} else if err != nil {
				return nil
			}
			e.probes.put(rel, stat, info, "")
		}
		id := embeddedID(info.Tags)
		if id != "" {
			index[id] = append(index[id], rel)
		}
		if cand, ok := candidateFromInfo(rel, id, info.Tags, info.Duration); ok {
			files = append(files, cand)
		}
		return nil
	})
	_ = e.probes.save()
	if err != nil {
		return nil, nil, fmt.Errorf("could not inspect existing music: %w", err)
	}
	return index, files, nil
}

func trackMatchIDs(id string, t *manifest.Track) []string {
	var keys []string
	add := func(k string) {
		if k == "" {
			return
		}
		for _, existing := range keys {
			if existing == k {
				return
			}
		}
		keys = append(keys, k)
	}
	add(id)
	if t != nil {
		add(t.Alt)
		add(t.ResolvedID)
	}
	return keys
}

func claimedPhysicalPaths(m *manifest.Manifest) map[string]bool {
	claimed := map[string]bool{}
	for _, t := range m.Tracks {
		if t != nil && t.Status == manifest.StatusDone && t.Path != "" {
			claimed[strings.ToLower(filepath.Clean(t.Path))] = true
		}
	}
	return claimed
}

func candidateFromInfo(rel, sourceID string, tags map[string]string, dur float64) (physicalCandidate, bool) {
	title := tags["title"]
	album := tags["album"]
	albumArtist := tags["album_artist"]
	artist := tags["artist"]
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	if title == "" || title == stem {
		title = strings.TrimSpace(trackPrefixRe.ReplaceAllString(stem, ""))
	}
	if title == "" {
		return physicalCandidate{}, false
	}
	dir := filepath.Dir(rel)
	if album == "" && dir != "." && !strings.EqualFold(filepath.Base(dir), "Singles") {
		album = filepath.Base(dir)
		if albumArtist == "" {
			parent := filepath.Dir(dir)
			if parent != "." {
				albumArtist = filepath.Base(parent)
			}
		}
	}
	return physicalCandidate{
		Path:        rel,
		SourceID:    sourceID,
		Title:       title,
		Artist:      artist,
		AlbumArtist: albumArtist,
		Album:       album,
		Duration:    dur,
	}, true
}

func enrichAlbumTags(m *manifest.Manifest, id string, tags map[string]string) {
	album, albumArtist, n := m.AlbumInfoOf(id)
	if album == "" {
		return
	}
	if tags["album"] == "" {
		tags["album"] = album
	}
	if tags["track"] == "" && n > 0 {
		tags["track"] = strconv.Itoa(n)
	}
	if albumArtist != "" && !strings.EqualFold(tags["album_artist"], "Various Artists") {
		tags["album_artist"] = albumArtist
	}
}

func resolveCanonicalCandidate(ctx context.Context, m *manifest.Manifest, id string, t *manifest.Track, folders string, candidates []string) (string, bool) {
	if t == nil || len(candidates) < 2 {
		return "", false
	}
	album, albumArtist, n := m.AlbumInfoOf(id)
	if album == "" && t.Artist == "" && albumArtist == "" {
		return "", false
	}
	title := t.Title
	if title == "" {
		return "", false
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(candidates[0])), ".")
	if ext == "" {
		ext = "opus"
	}
	tags := map[string]string{
		"title":        title,
		"artist":       t.Artist,
		"album_artist": albumArtist,
		"album":        album,
	}
	if n > 0 {
		tags["track"] = strconv.Itoa(n)
	}
	expected, err := ytdlp.Name(ctx, tags, ext, folders)
	if err != nil || expected == "" {
		return "", false
	}
	var exact []string
	for _, c := range candidates {
		if strings.EqualFold(filepath.Clean(c), filepath.Clean(expected)) {
			exact = append(exact, c)
		}
	}
	if len(exact) == 1 {
		return exact[0], true
	}
	if folders == "" && album != "" && albumArtist != "" {
		expectedDir := filepath.Dir(expected)
		var inDir []string
		for _, c := range candidates {
			if strings.EqualFold(filepath.Dir(filepath.Clean(c)), filepath.Clean(expectedDir)) {
				inDir = append(inDir, c)
			}
		}
		if len(inDir) == 1 {
			return inDir[0], true
		}
	}
	return "", false
}

func (e *Engine) promoteSingleToAlbumLocked(ctx context.Context, id, rel string) (string, bool) {
	promoted, err := e.promoteSingleToAlbum(ctx, id, rel)
	if err != nil || promoted == "" || promoted == rel {
		return rel, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, other := range e.m.Tracks {
		if other != nil && other.Path == rel {
			other.Path = promoted
		}
	}
	if e.presence != nil {
		e.presence[rel] = false
		e.presence[promoted] = true
	}
	if err := e.m.Save(); err != nil {
		return rel, false
	}
	if e.physicalIndex != nil {
		for key, paths := range e.physicalIndex {
			for i, p := range paths {
				if p == rel {
					paths[i] = promoted
				}
			}
			e.physicalIndex[key] = paths
		}
	}
	return promoted, true
}

func (e *Engine) promoteSingleToAlbum(ctx context.Context, id, rel string) (string, error) {
	return promoteSingleWithManifest(ctx, e.Root(), e.opt.Folders, e.m, &e.mu, id, rel)
}

func promoteSingleWithManifest(ctx context.Context, root, folders string, m *manifest.Manifest, mu interface {
	Lock()
	Unlock()
}, id, rel string) (string, error) {
	if folders != "" || rel == "" || !strings.EqualFold(filepath.Base(filepath.Dir(rel)), "Singles") {
		return rel, nil
	}
	if mu != nil {
		mu.Lock()
	}
	album, albumArtist, n := m.AlbumInfoOf(id)
	t := m.Tracks[id]
	title, artist := "", ""
	if t != nil {
		title, artist = t.Title, t.Artist
	}
	if mu != nil {
		mu.Unlock()
	}
	if album == "" {
		return rel, nil
	}
	full := filepath.Join(root, rel)
	info, probeErr := ytdlp.Probe(ctx, full)
	tags := map[string]string{}
	if probeErr == nil {
		for k, v := range info.Tags {
			tags[k] = v
		}
	}
	if tags["title"] == "" {
		tags["title"] = title
	}
	if tags["artist"] == "" {
		tags["artist"] = artist
	}
	tags["album"] = album
	if n > 0 {
		tags["track"] = strconv.Itoa(n)
	}
	if albumArtist != "" && !strings.EqualFold(tags["album_artist"], "Various Artists") {
		tags["album_artist"] = albumArtist
	}
	if tags["album_artist"] == "" {
		tags["album_artist"] = tags["artist"]
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
	if ext == "" {
		return rel, nil
	}
	targetRel, err := ytdlp.Name(ctx, tags, ext, "")
	if err != nil || targetRel == "" || strings.EqualFold(targetRel, rel) || !safeAudioPath(targetRel) {
		return rel, nil
	}
	if _, statErr := os.Stat(filepath.Join(root, targetRel)); statErr == nil {
		return rel, nil
	}
	if err := moveFile(root, rel, targetRel); err != nil {
		return rel, err
	}
	updateAlbumTagsInPlace(ctx, filepath.Join(root, targetRel), tags["album"], tags["album_artist"], tags["track"])
	return targetRel, nil
}

func updateAlbumTagsInPlace(ctx context.Context, full, album, albumArtist, track string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return
	}
	ext := filepath.Ext(full)
	tmp, err := os.CreateTemp(filepath.Dir(full), ".ytm-tag-*"+ext)
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	args := []string{"-nostdin", "-v", "error", "-y", "-i", full, "-map", "0", "-c", "copy", "-map_metadata", "0"}
	if album != "" {
		args = append(args, "-metadata", "album="+album)
	}
	if albumArtist != "" {
		args = append(args, "-metadata", "album_artist="+albumArtist)
	}
	if track != "" {
		args = append(args, "-metadata", "track="+track)
	}
	args = append(args, tmpPath)
	if err := exec.CommandContext(ctx, "ffmpeg", args...).Run(); err != nil {
		return
	}
	_ = relocate(tmpPath, full)
}

func matchTrackByMetadata(m *manifest.Manifest, id string, t *manifest.Track, files []physicalCandidate, claimed map[string]bool) (string, bool) {
	if t == nil || len(files) == 0 {
		return "", false
	}
	for _, relaxed := range []bool{false, true} {
		norm := normalizeSongTitle
		normAlbum := normalizeTitle
		if relaxed {
			norm = normalizeRelaxedTitle
			normAlbum = normalizeRelaxedTitle
		}
		wantTitle := norm(t.Title)
		if wantTitle == "" {
			continue
		}
		album, albumArtist, _ := m.AlbumInfoOf(id)
		wantAlbum := normAlbum(album)

		var matches []string
		for _, f := range files {
			key := strings.ToLower(filepath.Clean(f.Path))
			if claimed[key] {
				continue
			}
			if f.SourceID != "" && hasTrackID(m, f.SourceID) {
				continue
			}
			if norm(f.Title) != wantTitle {
				continue
			}
			if t.Duration > 0 && f.Duration > 0 && math.Abs(t.Duration-f.Duration) > 5 {
				continue
			}
			if wantAlbum != "" {
				if normAlbum(f.Album) != wantAlbum {
					continue
				}
				if albumArtist != "" && !strings.EqualFold(albumArtist, "Various Artists") {
					fileArtist := f.AlbumArtist
					if fileArtist == "" {
						fileArtist = f.Artist
					}
					if fileArtist != "" && !strings.EqualFold(fileArtist, "Various Artists") && !artistsOverlap(albumArtist, fileArtist) && !artistsOverlap(albumArtist, f.Artist) {
						continue
					}
				}
			} else {
				if t.Artist == "" || f.Artist == "" || !artistsOverlap(t.Artist, f.Artist) {
					continue
				}
			}
			matches = append(matches, f.Path)
		}
		if len(matches) == 1 {
			return matches[0], true
		}
		if len(matches) > 1 {
			return "", false
		}
	}
	return "", false
}

func hasTrackID(m *manifest.Manifest, id string) bool {
	if m.Tracks[id] != nil {
		return true
	}
	for _, t := range m.Tracks {
		if t != nil && (t.Alt == id || t.ResolvedID == id) {
			return true
		}
	}
	return false
}

func normalizeSongTitle(s string) string {
	cleaned := videoNoise.ReplaceAllString(featNoise.ReplaceAllString(s, ""), "")
	return normalizeTitle(cleaned)
}

func normalizeRelaxedTitle(s string) string {
	cleaned := editionNoise.ReplaceAllString(videoNoise.ReplaceAllString(featNoise.ReplaceAllString(s, ""), ""), "")
	return normalizeTitle(cleaned)
}

func artistsOverlap(a, b string) bool {
	na, nb := normalizeTitle(cleanArtist(a)), normalizeTitle(cleanArtist(b))
	if na == "" || nb == "" {
		return false
	}
	return na == nb || strings.Contains(na, nb) || strings.Contains(nb, na)
}
