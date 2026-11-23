package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

type LibraryChange struct {
	Track               LibraryTrack
	To, Format, Problem string
	Convert             bool
}
type LibraryPlan struct {
	Changes        []LibraryChange
	Format, Layout string
}

// PlanLibrary shows every destination and conflict before any files are changed.
// An empty layout preserves folders; an empty format preserves the audio format.
func (e *Engine) PlanLibrary(ctx context.Context, rows []LibraryTrack, format, layout string) (LibraryPlan, error) {
	plan := LibraryPlan{Format: format, Layout: layout}
	if format != "" && !ytdlp.ValidFormat(format) {
		return plan, fmt.Errorf("invalid format")
	}
	if layout != "keep" && layout != "" && layout != "artist" && layout != "flat" {
		return plan, fmt.Errorf("invalid folder layout")
	}
	paths := map[string]bool{}
	targets := map[string]int{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		if paths[row.Path] {
			continue
		}
		paths[row.Path] = true
		change := LibraryChange{Track: row, To: row.Path, Format: format}
		if row.Missing || row.Invalid || row.Status == "unreadable" || !safeAudioPath(row.Path) {
			change.Problem = "Missing or unreadable audio; repair first"
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(row.Path)), ".")
		if format != "" {
			codec := map[string]string{"opus": "opus", "m4a": "aac", "mp3": "mp3"}[format]
			change.Convert = row.Codec != codec || ext != format || row.FormatMismatch
			ext = format
			change.To = strings.TrimSuffix(row.Path, filepath.Ext(row.Path)) + "." + format
		}
		if layout != "keep" {
			tags := map[string]string{"title": row.Title, "artist": row.Artist, "album_artist": row.AlbumArtist, "album": row.Album, "track": row.TrackNumber}
			e.mu.Lock()
			enrichAlbumTags(e.m, row.ID, tags)
			e.mu.Unlock()
			rel, err := ytdlp.Name(ctx, tags, ext, layout)
			if err != nil {
				change.Problem = err.Error()
			} else {
				change.To = rel
			}
		}
		if !safeAudioPath(change.To) {
			change.Problem = "Unsafe target path"
		}
		if change.To == row.Path && !change.Convert {
			continue
		}
		// Changing a codec in place uses a separate filename so an interruption can
		// never truncate the original, even when its extension was misleading.
		if change.To == row.Path {
			change.To = strings.TrimSuffix(change.To, filepath.Ext(change.To)) + " (converted)." + ext
		}
		if st, err := os.Lstat(filepath.Join(e.Root(), change.To)); err == nil && st != nil {
			change.Problem = "Target already exists; no files will be overwritten"
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			change.Problem = err.Error()
		}
		// Case folding also protects destinations copied to Windows or a phone.
		key := strings.ToLower(change.To)
		if previous, ok := targets[key]; ok {
			change.Problem = "Multiple selected files have the same target"
			plan.Changes[previous].Problem = change.Problem
		}
		targets[key] = len(plan.Changes)
		plan.Changes = append(plan.Changes, change)
	}
	return plan, nil
}

// publishCopy creates a new file exclusively and verifies all copied bytes.
func publishCopy(ctx context.Context, root *os.Root, local, dest string) error {
	st, err := os.Stat(local)
	if err != nil {
		return err
	}
	if err = root.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if err = device.RequireSpace(filepath.Join(root.Name(), filepath.Dir(dest)), uint64(st.Size())); err != nil {
		return err
	}
	in, err := os.Open(local)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, scanReader{ctx, in})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		a, hashErr := hashFile(ctx, local)
		if hashErr != nil {
			err = hashErr
		} else {
			b, hashErr := hashFile(ctx, filepath.Join(root.Name(), dest))
			if hashErr != nil {
				err = hashErr
			} else if a != b {
				err = fmt.Errorf("copy verification failed")
			}
		}
	}
	if err != nil {
		_ = root.Remove(dest)
	}
	return err
}

func (e *Engine) ApplyLibraryPlan(ctx context.Context, plan LibraryPlan) (FileReport, error) {
	e.mu.Lock()
	defer func() { e.presence = nil; e.mu.Unlock() }()
	var report FileReport
	if e.running {
		return report, fmt.Errorf("wait for the current operation to finish")
	}
	// A blocked preview is never partly executed.
	for _, c := range plan.Changes {
		if c.Problem != "" {
			return report, fmt.Errorf("%s: %s", c.Track.Path, c.Problem)
		}
	}
	root, err := os.OpenRoot(e.m.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()
	batch := &trashBatch{ID: time.Now().UTC().Format("20060102T150405.000000000")}
	report.Batch = batch.ID
	var failures []error
	for _, c := range plan.Changes {
		if err = ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		p := c.Track.Path
		if !safeAudioPath(p) || !safeAudioPath(c.To) || p == c.To {
			return report, fmt.Errorf("unsafe conversion or move path")
		}
		st, err := root.Lstat(p)
		if err == nil && (!st.Mode().IsRegular() || (c.Track.Modified != 0 && st.ModTime().UnixNano() != c.Track.Modified)) {
			err = fmt.Errorf("file changed; scan and preview again")
		}
		if err == nil && c.Track.Hash != "" {
			var hash string
			hash, err = hashFile(ctx, filepath.Join(e.m.Root, p))
			if err == nil && hash != c.Track.Hash {
				err = fmt.Errorf("file contents changed; scan again")
			}
		}
		if err != nil {
			report.failure(p, err)
			failures = append(failures, err)
			continue
		}
		oldTracks := map[string]manifest.Track{}
		newLocalID := ""
		for id, t := range e.m.Tracks {
			if t.Path == p {
				oldTracks[id] = *t
			}
		}
		if len(oldTracks) == 0 {
			// Local files stay manageable after renaming, without synthetic downloads.
			id := "local:" + c.Track.Hash
			if c.Track.Hash == "" {
				id = "local:" + p
			}
			if _, exists := e.m.Tracks[id]; exists {
				id += ":" + p
			}
			oldTracks[id] = manifest.Track{Title: c.Track.Title, Duration: c.Track.Duration, Path: p, Status: manifest.StatusDone, Local: true}
			newLocalID = id
		}
		var entry *trashEntry
		if c.Convert {
			if !ytdlp.ValidFormat(c.Format) || strings.TrimPrefix(filepath.Ext(c.To), ".") != c.Format {
				return report, fmt.Errorf("invalid conversion plan")
			}
			tmp, tmpErr := os.MkdirTemp("", "ytmanager-convert-*")
			if tmpErr != nil {
				report.failure(p, tmpErr)
				failures = append(failures, tmpErr)
				continue
			}
			local := filepath.Join(tmp, "audio."+c.Format)
			err = ytdlp.Convert(ctx, filepath.Join(e.m.Root, p), local, c.Format)
			if err == nil {
				err = publishCopy(ctx, root, local, c.To)
			}
			os.RemoveAll(tmp)
			if err == nil {
				hash, _ := hashFile(ctx, filepath.Join(e.m.Root, p))
				replacementHash, _ := hashFile(ctx, filepath.Join(e.m.Root, c.To))
				entry = &trashEntry{Path: p, Stored: filepath.Join(trashDir, batch.ID, "files", p), Hash: hash, Size: st.Size(), Tracks: oldTracks, Replacement: c.To, ReplacementHash: replacementHash}
				err = journalReplacement(root, batch, entry)
				if err != nil {
					err = errors.Join(err, root.Remove(c.To))
				}
			}
		} else {
			hash, hashErr := hashFile(ctx, filepath.Join(e.m.Root, p))
			err = hashErr
			if err == nil {
				entry = &trashEntry{Path: p, Stored: filepath.Join(trashDir, batch.ID, "files", p), Hash: hash, Size: st.Size(), Tracks: oldTracks, Replacement: c.To, ReplacementHash: hash, Move: true}
				err = moveRootBeforeRemove(ctx, root, p, c.To, device.IsMTP(e.m.Root), func() error {
					return journalReplacement(root, batch, entry)
				})
			}
		}
		if err != nil {
			report.failure(p, err)
			failures = append(failures, err)
			continue
		}
		for id, old := range oldTracks {
			if e.m.Tracks[id] == nil {
				copy := old
				e.m.Tracks[id] = &copy
			}
			t := e.m.Tracks[id]
			t.Path, t.Status, t.Error = c.To, manifest.StatusDone, ""
		}
		if err = e.m.Save(); err != nil {
			for id, old := range oldTracks {
				*e.m.Tracks[id] = old
			}
			if newLocalID != "" {
				delete(e.m.Tracks, newLocalID)
			}
			if c.Convert {
				_ = root.Remove(c.To)
			} else {
				failures = append(failures, moveRoot(context.Background(), root, c.To, p, device.IsMTP(e.m.Root)))
			}
			report.failure(p, err)
			failures = append(failures, err)
			continue
		}
		if c.Convert {
			if err = moveRoot(ctx, root, p, entry.Stored, device.IsMTP(e.m.Root)); err != nil {
				report.failure(p, fmt.Errorf("new file saved; original retained: %w", err))
				failures = append(failures, err)
			} else {
				report.Deleted++
				report.Bytes += entry.Size
			}
		}
		fromLRC, toLRC := strings.TrimSuffix(p, filepath.Ext(p))+".lrc", strings.TrimSuffix(c.To, filepath.Ext(c.To))+".lrc"
		if fromLRC != toLRC {
			if sideStat, sideErr := root.Lstat(fromLRC); sideErr == nil && sideStat.Mode().IsRegular() {
				hash, sideErr := hashFile(ctx, filepath.Join(e.m.Root, fromLRC))
				side := &trashEntry{Path: fromLRC, Stored: filepath.Join(trashDir, batch.ID, "files", fromLRC), Hash: hash, Size: sideStat.Size(), Replacement: toLRC, ReplacementHash: hash}
				if sideErr == nil {
					sideErr = publishCopy(ctx, root, filepath.Join(e.m.Root, fromLRC), toLRC)
				}
				if sideErr == nil {
					sideErr = journalReplacement(root, batch, side)
					if sideErr != nil {
						sideErr = errors.Join(sideErr, root.Remove(toLRC))
					}
				}
				if sideErr == nil && !audioUsesSidecar(root, fromLRC) {
					sideErr = moveRoot(ctx, root, fromLRC, side.Stored, device.IsMTP(e.m.Root))
				}
				if sideErr != nil {
					report.failure(fromLRC, sideErr)
					failures = append(failures, sideErr)
				}
			}
		}
		report.Items = append(report.Items, FileResult{Path: p, Outcome: "verified → " + c.To, Bytes: st.Size()})
		report.Tracks++
		report.Changed++
		pruneEmpty(root, filepath.Dir(p))
	}
	failures = append(failures, e.m.WritePlaylists())
	return report, errors.Join(failures...)
}

// Journal only replacements exclusively created by this operation.
func journalReplacement(root *os.Root, batch *trashBatch, entry *trashEntry) error {
	batch.Entries = append(batch.Entries, entry)
	if err := saveTrash(root, batch); err != nil {
		batch.Entries = batch.Entries[:len(batch.Entries)-1]
		return err
	}
	return nil
}

func SortLibrary(rows []LibraryTrack, group string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if group == "albums" && a.AlbumKey() != b.AlbumKey() {
			return a.AlbumKey() < b.AlbumKey()
		}
		if group == "duplicates" && a.DuplicateGroup != b.DuplicateGroup {
			return a.DuplicateGroup < b.DuplicateGroup
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}

func audioUsesSidecar(root *os.Root, lrc string) bool {
	dir, err := root.Open(filepath.Dir(lrc))
	if err != nil {
		return true
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return true
	}
	stem := strings.TrimSuffix(lrc, filepath.Ext(lrc))
	for _, entry := range entries {
		p := filepath.Join(filepath.Dir(lrc), entry.Name())
		if audioExtension(p) && strings.TrimSuffix(p, filepath.Ext(p)) == stem {
			return true
		}
	}
	return false
}
