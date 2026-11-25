package tui

import (
	"fmt"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/device"
)

func (m model) pickerMounts() []device.Mount {
	if len(m.recent) == 0 {
		return m.mounts
	}
	list := slices.Clone(m.recent)
	for _, mount := range m.mounts {
		if !slices.ContainsFunc(m.recent, func(r device.Mount) bool { return r.Path == mount.Path }) {
			list = append(list, mount)
		}
	}
	return list
}

func (m *model) replaceMounts(list, recent []device.Mount) {
	var selected string
	if m.screen == screenDevices && !m.startup {
		mounts := m.pickerMounts()
		if m.cursor < len(mounts) {
			selected = mounts[m.cursor].Path
		}
	}
	m.mounts, m.recent = list, recent
	if m.screen != screenDevices || m.startup {
		return
	}
	mounts := m.pickerMounts()
	m.cursor = len(mounts)
	for i, mount := range mounts {
		if mount.Path == selected {
			m.cursor = i
			break
		}
	}
}

type recentOpenedMsg struct{ destinationOpen }

func (m *model) openRecent(path string) tea.Cmd {
	m.opening = path
	opt, format := m.opt, m.format
	var root string
	for _, folder := range m.history {
		if folder.Path == path {
			root = folder.Mount
			break
		}
	}
	return func() tea.Msg {
		if root != "" && !slices.Contains(device.RemovableRoots(), root) {
			return recentOpenedMsg{destinationOpen{err: recentUnavailable(path)}}
		}
		dest := loadDestination(path, opt, format, true)
		if os.IsNotExist(dest.err) {
			dest.err = recentUnavailable(path)
		}
		return recentOpenedMsg{dest}
	}
}

func recentUnavailable(path string) error {
	return fmt.Errorf("Destination is no longer available: %s", device.Label(path))
}
