package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type scanFile struct {
	path, rel string
	stat      os.FileInfo
	entries   []LibraryTrack
}

func (e *Engine) discoverFiles(ctx context.Context, root string, byPath map[string][]LibraryTrack, byFold map[string][]string, discover func([]LibraryTrack)) ([]scanFile, []string, []error) {
	var files []scanFile
	var playlists []string
	var failures []error
	preview := discoveryBatch{publish: discover}
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
		if folded := byFold[strings.ToLower(rel)]; len(entries) == 0 && len(folded) == 1 {
			entries = byPath[folded[0]]
		}
		if len(entries) == 0 {
			entries = []LibraryTrack{{ID: "file:" + rel, Title: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Unmanaged: true}}
		}
		file := scanFile{p, rel, stat, entries}
		files = append(files, file)
		e.scanTotal.Add(1)
		preview.add(file)
		return nil
	})
	if err != nil {
		failures = append(failures, err)
	}
	if ctx.Err() == nil {
		preview.flush()
	}
	return files, playlists, failures
}

type discoveryBatch struct {
	publish func([]LibraryTrack)
	rows    []LibraryTrack
	last    time.Time
}

func (b *discoveryBatch) add(f scanFile) {
	if b.publish == nil {
		return
	}
	for _, row := range f.entries {
		row.Path, row.Size, row.Modified = f.rel, f.stat.Size(), f.stat.ModTime().UnixNano()
		row.Status, row.Orphan = "not scanned", false
		b.rows = append(b.rows, row)
	}
	// Each update rebuilds the grouped views; neither fast nor slow devices need one per file.
	if len(b.rows) >= 32 && time.Since(b.last) >= 150*time.Millisecond {
		b.flush()
	}
}

func (b *discoveryBatch) flush() {
	if len(b.rows) == 0 {
		return
	}
	b.publish(b.rows)
	b.rows, b.last = nil, time.Now()
}
