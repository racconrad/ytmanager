package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
)

type FileResult struct {
	Path, Outcome, Error string
	Bytes                int64
}
type FileReport struct {
	Items                                      []FileResult
	Deleted, Missing, Failed, Restored, Tracks int
	Bytes                                      int64
	Batch                                      string
	Changed                                    int
	Permanent                                  bool
}

func (r FileReport) Summary() string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(r.Failed, "failed")
	add(r.Restored, "restored")
	add(r.Changed, "changed")
	switch {
	case r.Permanent:
		add(r.Deleted, "permanently deleted")
		if r.Deleted == 0 && r.Missing == 0 {
			add(r.Tracks, "removed from lists")
		}
	case r.Changed > 0:
		add(r.Deleted, "originals in trash")
	default:
		add(r.Deleted, "moved to trash")
	}
	add(r.Missing, "already absent")
	if len(parts) == 0 {
		return "No files changed"
	}
	if r.Bytes > 0 {
		parts = append(parts, Human(uint64(r.Bytes)))
	}
	return strings.Join(parts, " · ")
}
func (r *FileReport) failure(path string, err error) {
	r.Failed++
	r.Items = append(r.Items, FileResult{Path: path, Outcome: "failed", Error: err.Error()})
}

type trashEntry struct {
	Path, Stored, Hash           string
	Replacement, ReplacementHash string
	Size                         int64
	Tracks                       map[string]manifest.Track
	Restored                     bool
	Move                         bool
	Reset                        bool `json:",omitempty"`
}
type trashBatch struct {
	ID      string
	Entries []*trashEntry
}

func (item *trashEntry) trashedTrack(old manifest.Track) manifest.Track {
	old.Path = item.Path
	old.Status = manifest.StatusRemoved
	if item.Reset && !old.Local {
		old.Status, old.Error, old.Fails = manifest.StatusPending, "", 0
	}
	return old
}

func (item *trashEntry) canRestoreTrack(current, old manifest.Track) bool {
	if item.Replacement != "" && current.Path == item.Replacement {
		return true
	}
	if current.Path != item.Path {
		return false
	}
	return current.Status == manifest.StatusRemoved || (item.Reset && current == item.trashedTrack(old))
}

func saveTrash(root *os.Root, batch *trashBatch) error {
	dir := filepath.Join(trashDir, batch.ID)
	if err := root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "journal.json")
	// Like data.music, keep a complete backup before writing to MTP.
	if err = root.WriteFile(p+".bak", data, 0600); err != nil {
		return err
	}
	return root.WriteFile(p, data, 0600)
}

func safeAudioPath(path string) bool {
	return filepath.IsLocal(path) && audioExtension(path) && !strings.EqualFold(strings.Split(filepath.Clean(path), string(filepath.Separator))[0], trashDir)
}

func safeLibraryPath(path string) bool {
	return safeAudioPath(path) || (filepath.IsLocal(path) && strings.ToLower(filepath.Ext(path)) == ".lrc" && !strings.EqualFold(strings.Split(filepath.Clean(path), string(filepath.Separator))[0], trashDir))
}

// moveRoot never overwrites. MTP copies are verified before the original is removed.
func moveRoot(ctx context.Context, root *os.Root, from, to string, mtp bool) error {
	return moveRootBeforeRemove(ctx, root, from, to, mtp, nil)
}

func moveRootBeforeRemove(ctx context.Context, root *os.Root, from, to string, mtp bool, beforeRemove func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := root.Lstat(to); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("destination already exists: %s", to)
		}
		return err
	}
	if err := root.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	if !mtp {
		// Link creates the target exclusively; Rename could overwrite a file
		// created between the existence check and the move.
		if err := root.Link(from, to); err == nil {
			return finishRootMove(root, from, to, beforeRemove)
		}
	}
	in, err := root.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), scanReader{ctx, in})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		check, openErr := root.Open(to)
		if openErr != nil {
			err = openErr
		} else {
			copyHash := sha256.New()
			_, err = io.Copy(copyHash, scanReader{ctx, check})
			check.Close()
			if err == nil && fmt.Sprintf("%x", h.Sum(nil)) != fmt.Sprintf("%x", copyHash.Sum(nil)) {
				err = fmt.Errorf("copy verification failed: %s", to)
			}
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		original, openErr := root.Open(from)
		if openErr != nil {
			err = openErr
		} else {
			hash := sha256.New()
			_, err = io.Copy(hash, scanReader{ctx, original})
			original.Close()
			if err == nil && fmt.Sprintf("%x", hash.Sum(nil)) != fmt.Sprintf("%x", h.Sum(nil)) {
				err = fmt.Errorf("source changed during copy: %s", from)
			}
		}
	}
	if err != nil {
		_ = root.Remove(to)
		return err
	}
	return finishRootMove(root, from, to, beforeRemove)
}

func finishRootMove(root *os.Root, from, to string, beforeRemove func() error) error {
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return errors.Join(err, root.Remove(to))
		}
		return root.Remove(from)
	}
	if err := root.Remove(from); err != nil {
		_ = root.Remove(to)
		return err
	}
	return nil
}

// TrashFiles accepts the exact reviewed rows, including external audio. Every
// managed entry sharing a physical path must be selected. No directory is deleted.
func (e *Engine) TrashFiles(ctx context.Context, selected []LibraryTrack) (FileReport, error) {
	return e.trashFiles(ctx, selected, false)
}

func (e *Engine) trashFiles(ctx context.Context, selected []LibraryTrack, reset bool) (FileReport, error) {
	e.mu.Lock()
	defer func() { e.presence = nil; e.mu.Unlock() }()
	var report FileReport
	if e.running {
		return report, fmt.Errorf("wait for the current operation to finish")
	}
	root, err := os.OpenRoot(e.m.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()
	paths := map[string][]string{}
	reviewed := map[string]LibraryTrack{}
	selectedIDs := map[string]bool{}
	for _, row := range selected {
		selectedIDs[row.ID] = true
	}
	for _, row := range selected {
		p := filepath.Clean(row.Path)
		if t := e.m.Tracks[row.ID]; t != nil {
			if p != filepath.Clean(t.Path) && p != filepath.Clean(e.m.AudioPath(t)) {
				return report, fmt.Errorf("path changed for %s; rescan before deleting", row.Title)
			}
		} else if row.ID != "file:"+row.Path && row.ID != "file:"+p && !row.Unmanaged {
			return report, fmt.Errorf("unknown file: %s", p)
		}
		if !safeAudioPath(p) {
			return report, fmt.Errorf("unsafe audio path: %s", p)
		}
		for id, t := range e.m.Tracks {
			if filepath.Clean(t.Path) == p && t.Status != manifest.StatusRemoved && !selectedIDs[id] {
				return report, fmt.Errorf("%s shares its file; select all entries for this path", p)
			}
		}
		if _, ok := paths[p]; !ok {
			paths[p] = nil
		}
		if e.m.Tracks[row.ID] != nil {
			paths[p] = append(paths[p], row.ID)
		}
		reviewed[p] = row
	}
	batch := &trashBatch{ID: time.Now().UTC().Format("20060102T150405.000000000")}
	report.Batch = batch.ID
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var failures []error
	for _, p := range keys {
		if err = ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		stat, statErr := root.Lstat(p)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			report.failure(p, statErr)
			failures = append(failures, statErr)
			continue
		}
		if statErr == nil && !stat.Mode().IsRegular() {
			err = fmt.Errorf("not a regular audio file: %s", p)
			report.failure(p, err)
			failures = append(failures, err)
			continue
		}
		row := reviewed[p]
		if statErr == nil && ((row.Modified != 0 && row.Modified != stat.ModTime().UnixNano()) || (row.Size != 0 && row.Size != stat.Size())) {
			err = fmt.Errorf("file changed since review; rescan: %s", p)
			report.failure(p, err)
			failures = append(failures, err)
			continue
		}
		entry := &trashEntry{Path: p, Stored: filepath.Join(trashDir, batch.ID, "files", p), Tracks: map[string]manifest.Track{}, Reset: reset}
		for _, id := range paths[p] {
			if _, ok := entry.Tracks[id]; !ok {
				old := *e.m.Tracks[id]
				old.Path = p
				entry.Tracks[id] = old
			}
		}
		if statErr == nil {
			entry.Size = stat.Size()
			entry.Hash, err = hashFile(ctx, filepath.Join(e.m.Root, p))
			if err == nil && row.Hash != "" && entry.Hash != row.Hash {
				err = fmt.Errorf("file contents changed; rescan: %s", p)
			}
			if err != nil {
				report.failure(p, err)
				failures = append(failures, err)
				continue
			}
		}
		batch.Entries = append(batch.Entries, entry)
		if err = saveTrash(root, batch); err != nil {
			report.failure(p, err)
			failures = append(failures, err)
			break
		}
		for id, old := range entry.Tracks {
			*e.m.Tracks[id] = entry.trashedTrack(old)
		}
		if err = e.m.Save(); err != nil {
			for id, old := range entry.Tracks {
				*e.m.Tracks[id] = old
			}
			report.failure(p, err)
			failures = append(failures, err)
			break
		}
		if statErr == nil {
			err = moveRoot(ctx, root, p, entry.Stored, device.IsMTP(e.m.Root))
			if err == nil {
				if _, checkErr := root.Lstat(p); !errors.Is(checkErr, os.ErrNotExist) {
					err = fmt.Errorf("could not verify removal: %s", p)
				}
			}
			if err != nil {
				if _, checkErr := root.Lstat(p); checkErr == nil {
					for id, old := range entry.Tracks {
						*e.m.Tracks[id] = old
					}
					failures = append(failures, e.m.Save())
				}
				report.failure(p, err)
				failures = append(failures, err)
				continue
			}
			report.Deleted++
			report.Bytes += entry.Size
			report.Items = append(report.Items, FileResult{Path: p, Outcome: "verified in trash", Bytes: entry.Size})
		} else {
			report.Missing++
			report.Items = append(report.Items, FileResult{Path: p, Outcome: "already absent"})
		}
		report.Tracks += len(entry.Tracks)
		lrc := strings.TrimSuffix(p, filepath.Ext(p)) + ".lrc"
		shared := false
		for id, t := range e.m.Tracks {
			if _, trashed := entry.Tracks[id]; !trashed && t.Status != manifest.StatusRemoved && strings.TrimSuffix(t.Path, filepath.Ext(t.Path))+".lrc" == lrc {
				shared = true
			}
		}
		// External alternate formats may also share a lyric sidecar.
		dir, openErr := root.Open(filepath.Dir(p))
		if openErr == nil {
			matches, _ := dir.ReadDir(-1)
			dir.Close()
			for _, other := range matches {
				otherPath := filepath.Join(filepath.Dir(p), other.Name())
				if otherPath != p && audioExtension(otherPath) && strings.TrimSuffix(otherPath, filepath.Ext(otherPath)) == strings.TrimSuffix(p, filepath.Ext(p)) {
					shared = true
				}
			}
		} else {
			shared = true
		}
		if !shared {
			if st, lrcErr := root.Lstat(lrc); lrcErr == nil && st.Mode().IsRegular() {
				side := &trashEntry{Path: lrc, Stored: filepath.Join(trashDir, batch.ID, "files", lrc), Size: st.Size()}
				side.Hash, _ = hashFile(ctx, filepath.Join(e.m.Root, lrc))
				batch.Entries = append(batch.Entries, side)
				if err = saveTrash(root, batch); err == nil {
					err = moveRoot(ctx, root, lrc, side.Stored, device.IsMTP(e.m.Root))
				}
				if err != nil {
					report.failure(lrc, err)
					failures = append(failures, err)
				} else {
					report.Deleted++
					report.Bytes += side.Size
					report.Items = append(report.Items, FileResult{Path: lrc, Outcome: "verified in trash", Bytes: side.Size})
				}
			}
		}
		pruneEmpty(root, filepath.Dir(p))
	}
	if len(batch.Entries) > 0 {
		failures = append(failures, saveTrash(root, batch), e.m.WritePlaylists())
	}
	return report, errors.Join(failures...)
}

func pruneEmpty(root *os.Root, dir string) {
	for filepath.IsLocal(dir) && dir != "." && dir != trashDir {
		if err := root.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// RestoreTrash restores the newest batch that still has files. It is durable
// across restarts and refuses to replace any file created since deletion.
func (e *Engine) RestoreTrash(ctx context.Context) (FileReport, error) {
	e.mu.Lock()
	defer func() { e.presence = nil; e.mu.Unlock() }()
	var report FileReport
	if e.running {
		return report, fmt.Errorf("wait for the current operation to finish")
	}
	root, err := os.OpenRoot(e.m.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()
	entries, err := os.ReadDir(filepath.Join(e.m.Root, trashDir))
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	var failures []error
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].IsDir() {
			continue
		}
		id := entries[i].Name()
		p := filepath.Join(trashDir, id, "journal.json")
		data, readErr := root.ReadFile(p)
		batch := &trashBatch{}
		if readErr != nil || json.Unmarshal(data, batch) != nil {
			data, readErr = root.ReadFile(p + ".bak")
			if readErr != nil {
				failures = append(failures, readErr)
				continue
			}
			if readErr = json.Unmarshal(data, batch); readErr != nil {
				failures = append(failures, readErr)
				continue
			}
		}
		if batch.ID != id {
			return report, fmt.Errorf("invalid trash journal")
		}
		pending := false
		for _, item := range batch.Entries {
			if item == nil {
				return report, fmt.Errorf("invalid trash entry")
			}
			if item.Restored || e.blocksTrashRestore(item) {
				continue
			}
			if item.Replacement != "" && !safeLibraryPath(item.Replacement) {
				return report, fmt.Errorf("unsafe replacement path")
			}
			if !safeLibraryPath(item.Path) || item.Stored != filepath.Join(trashDir, id, "files", item.Path) {
				return report, fmt.Errorf("unsafe trash entry")
			}
			for _, old := range item.Tracks {
				if old.Path != item.Path {
					return report, fmt.Errorf("invalid track path in trash journal")
				}
			}
			if _, err := root.Lstat(item.Stored); err == nil {
				pending = true
			} else if item.Hash != "" {
				hash, _ := hashFile(ctx, filepath.Join(e.m.Root, item.Path))
				if hash == item.Hash {
					for id, old := range item.Tracks {
						if t := e.m.Tracks[id]; t != nil && item.canRestoreTrack(*t, old) {
							pending = true
						}
					}
				}
			}
			if item.Replacement != "" {
				if _, err := root.Lstat(item.Replacement); err == nil {
					if item.Move {
						pending = true
					} else {
						hash, _ := hashFile(ctx, filepath.Join(e.m.Root, item.Path))
						if hash == item.Hash {
							pending = true
						}
					}
				}
			}
		}
		if !pending {
			continue
		}
		report.Batch = id
		for _, item := range batch.Entries {
			if err = ctx.Err(); err != nil {
				failures = append(failures, err)
				break
			}
			if item.Restored || e.blocksTrashRestore(item) {
				continue
			}
			if item.Replacement != "" {
				if _, checkErr := root.Lstat(item.Replacement); checkErr == nil {
					hash, hashErr := hashFile(ctx, filepath.Join(e.m.Root, item.Replacement))
					if hashErr != nil || hash != item.ReplacementHash {
						err = fmt.Errorf("converted file changed; restore manually: %s", item.Replacement)
						report.failure(item.Path, err)
						failures = append(failures, err)
						continue
					}
				}
			}
			if st, checkErr := root.Lstat(item.Stored); checkErr == nil {
				hash, hashErr := hashFile(ctx, filepath.Join(e.m.Root, item.Stored))
				if !st.Mode().IsRegular() || hashErr != nil || hash != item.Hash {
					err = fmt.Errorf("trashed file changed or is unreadable: %s", item.Path)
					report.failure(item.Path, err)
					failures = append(failures, err)
					continue
				}
			}
			if item.Move {
				originalHash, _ := hashFile(ctx, filepath.Join(e.m.Root, item.Path))
				if originalHash == item.Hash {
					if err = root.Remove(item.Replacement); err != nil && !errors.Is(err, os.ErrNotExist) {
						report.failure(item.Path, err)
						failures = append(failures, err)
						continue
					}
				} else if _, checkErr := root.Lstat(item.Replacement); errors.Is(checkErr, os.ErrNotExist) {
					hash, _ := hashFile(ctx, filepath.Join(e.m.Root, item.Path))
					if hash != item.Hash {
						continue
					}
				} else if err = moveRoot(ctx, root, item.Replacement, item.Path, device.IsMTP(e.m.Root)); err != nil {
					report.failure(item.Path, err)
					failures = append(failures, err)
					continue
				}
			} else if _, err = root.Lstat(item.Stored); errors.Is(err, os.ErrNotExist) {
				// A crash may have happened after the move but before updating the journal.
				hash, hashErr := hashFile(ctx, filepath.Join(e.m.Root, item.Path))
				if hashErr != nil || hash != item.Hash {
					continue
				}
			} else if err == nil {
				if err = moveRoot(ctx, root, item.Stored, item.Path, device.IsMTP(e.m.Root)); err != nil {
					report.failure(item.Path, err)
					failures = append(failures, err)
					continue
				}
			} else {
				report.failure(item.Path, err)
				failures = append(failures, err)
				continue
			}
			previous := map[string]manifest.Track{}
			created := map[string]bool{}
			for trackID, old := range item.Tracks {
				if current := e.m.Tracks[trackID]; current != nil && item.canRestoreTrack(*current, old) {
					previous[trackID] = *current
					*current = old
				} else if current == nil && old.Local {
					created[trackID] = true
					copy := old
					e.m.Tracks[trackID] = &copy
				}
			}
			if err = e.m.Save(); err != nil {
				for id, t := range previous {
					*e.m.Tracks[id] = t
				}
				for id := range created {
					delete(e.m.Tracks, id)
				}
				report.failure(item.Path, err)
				failures = append(failures, err)
				continue
			}
			if item.Replacement != "" && !item.Move {
				if err = root.Remove(item.Replacement); err != nil && !errors.Is(err, os.ErrNotExist) {
					report.failure(item.Replacement, err)
					failures = append(failures, err)
					continue
				}
			}
			item.Restored = true
			report.Restored++
			report.Bytes += item.Size
			report.Items = append(report.Items, FileResult{Path: item.Path, Outcome: "restored", Bytes: item.Size})
			if err = saveTrash(root, batch); err != nil {
				failures = append(failures, err)
				break
			}
		}
		failures = append(failures, e.m.WritePlaylists())
		break
	}
	return report, errors.Join(failures...)
}

// EmptyTrash is explicitly confirmed by the caller. Only verified journal files
// are removed; unknown files and journals are retained for manual recovery.
func (e *Engine) EmptyTrash(ctx context.Context) (FileReport, error) {
	e.mu.Lock()
	defer func() { e.presence = nil; e.mu.Unlock() }()
	var report FileReport
	report.Permanent = true
	if e.running {
		return report, fmt.Errorf("wait for the current operation to finish")
	}
	root, err := os.OpenRoot(e.m.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()
	dirs, err := os.ReadDir(filepath.Join(e.m.Root, trashDir))
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	var failures []error
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		data, err := root.ReadFile(filepath.Join(trashDir, d.Name(), "journal.json"))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		var batch trashBatch
		if err = json.Unmarshal(data, &batch); err != nil || batch.ID != d.Name() {
			failures = append(failures, fmt.Errorf("invalid trash journal: %s", d.Name()))
			continue
		}
		for _, item := range batch.Entries {
			if item == nil {
				return report, fmt.Errorf("invalid trash entry")
			}
			if err = ctx.Err(); err != nil {
				return report, err
			}
			if !filepath.IsLocal(item.Path) || item.Stored != filepath.Join(trashDir, d.Name(), "files", item.Path) {
				return report, fmt.Errorf("unsafe trash path")
			}
			st, err := root.Lstat(item.Stored)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err == nil && !st.Mode().IsRegular() {
				err = fmt.Errorf("not a regular trashed file")
			}
			if err == nil {
				err = root.Remove(item.Stored)
			}
			if err != nil {
				report.failure(item.Path, err)
				failures = append(failures, err)
				continue
			}
			if _, err = root.Lstat(item.Stored); !errors.Is(err, os.ErrNotExist) {
				err = fmt.Errorf("could not verify permanent removal")
				report.failure(item.Path, err)
				failures = append(failures, err)
				continue
			}
			report.Deleted++
			report.Bytes += st.Size()
			report.Items = append(report.Items, FileResult{Path: item.Path, Outcome: "permanently deleted", Bytes: st.Size()})
			pruneEmpty(root, filepath.Dir(item.Stored))
		}
	}
	return report, errors.Join(failures...)
}
