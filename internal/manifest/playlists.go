package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ExportMarker = "#EXTM3U\n# ytmanager compatibility export\n"
const sourceMarker = "# ytmanager source: "

func (m *Manifest) PlaylistName(s *Source) string {
	base := SafeName(s.Title)
	collision := false
	for _, other := range m.Sources {
		if other.ID != s.ID && other.Type != "track" && strings.EqualFold(SafeName(other.Title), base) {
			collision = true
		}
	}
	if collision {
		base += " [" + SafeName(s.ID) + "]"
	}
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name += " [" + SafeName(s.ID) + "-" + strconv.Itoa(n) + "]"
		}
		name += ".m3u8"
		path := filepath.Join(m.Root, name)
		stat, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return name
		}
		if err != nil {
			return name
		}
		if err == nil && stat.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err == nil && (strings.Contains(string(data), "\n"+sourceMarker+s.ID+"\n") || string(data) == m.PlaylistText(s, false)) {
				return name
			}
		}
	}
}
func (m *Manifest) PlaylistText(s *Source, marked bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	if marked {
		b.WriteString(sourceMarker + s.ID + "\n")
	}
	for _, id := range s.Tracks {
		if t := m.Tracks[id]; t != nil && t.Status == StatusDone {
			fmt.Fprintf(&b, "#EXTINF:%d,%s\n%s\n", int(t.Duration), strings.Join(strings.Fields(t.Title), " "), filepath.ToSlash(t.Path))
		}
	}
	return b.String()
}

// WritePlaylists refreshes owned exports and removes stale owned playlists.
// Custom playlists have no ownership marker and are never removed.
func (m *Manifest) WritePlaylists() error {
	expected := map[string]string{}
	for _, s := range m.Sources {
		if s.Type != "track" {
			expected[m.PlaylistName(s)] = s.ID
		}
	}
	files, err := os.ReadDir(m.Root)
	if err != nil {
		return err
	}
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Name()))
		if file.IsDir() || file.Type()&os.ModeSymlink != 0 || (ext != ".m3u8" && ext != ".m3u") {
			continue
		}
		path := filepath.Join(m.Root, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		owned := ""
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, sourceMarker) {
				owned = strings.TrimPrefix(line, sourceMarker)
				break
			}
		}
		canonical := strings.TrimSuffix(file.Name(), ext) + ".m3u8"
		if owned != "" && expected[canonical] != owned {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	for _, s := range m.Sources {
		if s.Type == "track" {
			continue
		}
		name := m.PlaylistName(s)
		content := m.PlaylistText(s, true)
		path := filepath.Join(m.Root, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		exported := strings.TrimSuffix(path, ".m3u8") + ".m3u"
		if old, err := os.ReadFile(exported); err == nil && strings.HasPrefix(string(old), ExportMarker) {
			if err := os.WriteFile(exported, []byte(ExportMarker+strings.TrimPrefix(content, "#EXTM3U\n")), 0644); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// SafeName returns a file name that is valid on FAT32/exFAT.
func SafeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if s == "" {
		s = "untitled"
	}
	return s
}
