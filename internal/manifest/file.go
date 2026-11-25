package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Summary reports the manifest counts stored in data.music without inspecting audio files.
type Summary struct {
	Format    string
	Sources   int
	Albums    int
	Playlists int
	Tracks    int
	Saved     int
	Pending   int
	Failed    int
	Queued    int
}

// Summarize reads data.music (or its backup) if present in root without creating or modifying files.
func Summarize(root string) (Summary, bool) {
	p := filepath.Join(root, FileName)
	m, _ := read(root, p)
	if m == nil {
		m, _ = read(root, p+".bak")
	}
	if m == nil {
		return Summary{}, false
	}
	return m.Summary(), true
}

// Summary counts the saved catalog without reading the destination's audio.
func (m *Manifest) Summary() Summary {
	s := Summary{
		Format:  m.Format,
		Sources: len(m.Sources),
		Queued:  len(m.Queue),
	}
	if s.Format == "" {
		s.Format = "opus"
	}
	for _, src := range m.Sources {
		switch src.Type {
		case "album":
			s.Albums++
		case "playlist", "mix":
			s.Playlists++
		}
	}
	referenced := m.ReferencedTracks()
	for id, t := range m.Tracks {
		if t == nil || t.Status == StatusRemoved || (!referenced[id] && t.Path == "") {
			continue
		}
		s.Tracks++
		switch t.Status {
		case StatusDone:
			s.Saved++
		case StatusPending:
			if referenced[id] {
				s.Pending++
			}
		case StatusFailed:
			if referenced[id] {
				s.Failed++
			}
		}
	}
	return s
}

// Load reads data.music from root, falling back to the backup copy that Save keeps on MTP.
// A destination without either starts empty.
func Load(root string) (*Manifest, error) {
	p := filepath.Join(root, FileName)
	m, err := read(root, p)
	if m == nil {
		if bak, _ := read(root, p+".bak"); bak != nil {
			return bak, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = &Manifest{Version: 1, Format: "opus", Tracks: map[string]*Track{}, Root: root}
	}
	return m, nil
}

// read returns nil, nil when the file does not exist, and nil plus the error when it cannot be used.
func read(root, p string) (*Manifest, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := &Manifest{Root: root}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w", filepath.Base(p), err)
	}
	if m.Tracks == nil {
		m.Tracks = map[string]*Track{}
	}
	for _, source := range m.Sources {
		var dropped map[string]bool
		for _, id := range source.Tracks {
			t := m.Tracks[id]
			if source.blocksTrack(id, t) {
				if dropped == nil {
					dropped = make(map[string]bool)
				}
				dropped[id] = true
			}
		}
		for id := range source.Readded {
			if t := m.Tracks[id]; t == nil || t.PermanentlyRemoved {
				delete(source.Readded, id)
			}
		}
		if len(dropped) != 0 {
			source.DropTracks(dropped)
		}
	}
	return m, nil
}

// Save never leaves a half-written manifest behind if the device is unplugged mid-write. On
// regular filesystems it writes a .tmp and renames it over the old file. MTP (gvfs) loses files
// on rename, so there it writes a full .bak first and then the real file; Load uses the .bak if
// the real file was cut short.
func (m *Manifest) Save() error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(m.Root, FileName)
	if strings.Contains(p, "/gvfs/mtp:host=") {
		if err := os.WriteFile(p+".bak", b, 0o644); err != nil {
			return err
		}
		return os.WriteFile(p, b, 0o644)
	}
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}
