package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ytmanager/internal/device"
)

const recentLimit = 6

type recentFolder struct {
	Path  string `json:"path"`
	Mount string `json:"mount,omitempty"`
}

func destinationHistoryPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "ytmanager", "recent.json")
	}
	return ""
}

func readDestinationHistory(path string) []recentFolder {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var folders []recentFolder
	if json.Unmarshal(data, &folders) != nil {
		return nil
	}
	var history []recentFolder
	for _, folder := range folders {
		if !filepath.IsAbs(folder.Path) || folder.Mount != "" && !withinFolder(folder.Path, folder.Mount) {
			continue
		}
		if !slices.ContainsFunc(history, func(f recentFolder) bool { return f.Path == folder.Path }) {
			history = append(history, folder)
			if len(history) == recentLimit {
				break
			}
		}
	}
	return history
}

func (m *model) rememberDestination(path string) {
	if m.historyPath == "" {
		return
	}
	folder := recentFolder{Path: path}
	for _, root := range m.mountRoots {
		if len(root) > len(folder.Mount) && withinFolder(path, root) {
			folder.Mount = root
		}
	}
	if len(m.history) > 0 && m.history[0] == folder {
		return
	}
	history := []recentFolder{folder}
	for _, f := range m.history {
		if f.Path != path && len(history) < recentLimit {
			history = append(history, f)
		}
	}
	m.history = history
	if err := writeDestinationHistory(m.historyPath, history); err != nil {
		m.log(warn.Render("Could not save recent destinations: " + err.Error()))
	}
}

func writeDestinationHistory(path string, history []recentFolder) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "recent-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	writeErr := json.NewEncoder(f).Encode(history)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func withinFolder(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func availableRecent(history []recentFolder, mounts []device.Mount, roots []string) []device.Mount {
	var recent []device.Mount
	for _, folder := range history {
		if folder.Mount != "" && !slices.Contains(roots, folder.Mount) {
			continue
		}
		if info, err := os.Stat(folder.Path); err != nil || !info.IsDir() {
			continue
		}
		result := device.Mount{Path: folder.Path}
		for _, mount := range mounts {
			if withinFolder(folder.Path, mount.Path) {
				result.Free = mount.Free
				break
			}
		}
		recent = append(recent, result)
	}
	return recent
}
