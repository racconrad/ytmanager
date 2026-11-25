package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
)

type workspaceRemovalMsg struct {
	eng    *engine.Engine
	scope  string
	count  int
	report engine.FileReport
	err    error
}

func (m *model) startWorkspaceRemoval(action string) (tea.Model, tea.Cmd) {
	if m.run.busy || m.library.busy {
		m.log("Wait for the current operation to finish.")
		return *m, nil
	}
	if action == "exclude_tracks" {
		source, ids := m.collectionSelection()
		if source == "" || len(ids) == 0 {
			m.log("Open a saved album or playlist and select tracks first.")
			return *m, nil
		}
		m.ws.removing, m.library.busy = true, true
		eng := m.eng
		return *m, func() tea.Msg {
			n, err := eng.ExcludeSourceTracks(source, ids)
			return workspaceRemovalMsg{eng: eng, scope: source, count: n, err: err}
		}
	}
	if m.library.scanning || !m.library.complete {
		m.log("Wait for the folder scan before deleting audio.")
		if !m.library.scanning {
			return *m, m.scanLibrary()
		}
		return *m, nil
	}
	rows := m.selectedWorkspaceRows(true)
	if len(rows) == 0 {
		m.log("No tracks selected for deletion.")
		return *m, nil
	}
	m.ws.deleteReview, m.ws.deleteCursor = rows, 0
	return *m, nil
}

func (m *model) workspaceRemovalKey(key string) (tea.Model, tea.Cmd) {
	if m.ws.deleteReview == nil {
		return *m, nil
	}
	switch key {
	case "up", "k":
		m.ws.deleteCursor = max(0, m.ws.deleteCursor-1)
	case "down", "j":
		m.ws.deleteCursor = min(len(m.ws.deleteReview)-1, m.ws.deleteCursor+1)
	case "home":
		m.ws.deleteCursor = 0
	case "end":
		m.ws.deleteCursor = len(m.ws.deleteReview) - 1
	case "esc":
		m.ws.deleteReview = nil
	case "enter":
		rows, eng := m.ws.deleteReview, m.eng
		if m.run.busy || m.library.busy || m.library.scanning || !m.library.complete {
			m.ws.deleteReview = nil
			m.log("The library changed. Review the selection again.")
			return *m, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.ws.deleteReview, m.ws.removing = nil, true
		m.library.busy, m.library.cancel, m.library.since = true, cancel, time.Now()
		return *m, func() tea.Msg {
			report, err := eng.DeletePermanently(ctx, rows)
			return workspaceRemovalMsg{eng: eng, report: report, err: err}
		}
	}
	return *m, nil
}

func (m model) finishWorkspaceRemoval(msg workspaceRemovalMsg) (tea.Model, tea.Cmd) {
	if m.eng != msg.eng || !m.ws.removing {
		return m, nil
	}
	m.ws.removing, m.library.busy = false, false
	if m.library.cancel != nil {
		m.library.cancel()
		m.library.cancel = nil
	}
	if msg.scope != "" {
		prefix := msg.scope + "\x00"
		for key := range m.ws.markedSourceTracks {
			if strings.HasPrefix(key, prefix) {
				delete(m.ws.markedSourceTracks, key)
			}
		}
		prefix = "source:" + prefix
		for key := range m.ws.markedTracks {
			if strings.HasPrefix(key, prefix) {
				delete(m.ws.markedTracks, key)
			}
		}
		if msg.count > 0 {
			m.log(ok.Render("Removed " + countLabel(msg.count, "track") + " from this list. Audio kept."))
		} else if msg.err == nil {
			m.log("No tracks from this list selected.")
		}
	} else {
		deleted := map[string]bool{}
		for _, item := range msg.report.Items {
			if item.Outcome == "permanently deleted" || item.Outcome == "already absent" {
				deleted[filepath.Clean(item.Path)] = true
			}
		}
		m.library.all = slices.DeleteFunc(m.library.all, func(row engine.LibraryTrack) bool {
			return deleted[filepath.Clean(row.Path)]
		})
		m.library.allGen++
		m.library.cachedValid = false
		m.eng.SetPresenceFromScan(m.library.all)
		clear(m.ws.markedSourceTracks)
		clear(m.ws.markedTracks)
		clear(m.ws.markedAlbums)
		clear(m.ws.markedFiles)
		clear(m.ws.markedFolders)
		clear(m.ws.markedIssues)
		m.log(msg.report.Summary())
	}
	if msg.err != nil {
		m.fail(msg.err)
	}
	m.refreshWorkspace()
	return m, m.requestCover(m.workspaceCoverKey())
}

func (m model) workspaceDeleteReview(b *strings.Builder) {
	rows := m.ws.deleteReview
	header(b, "Delete "+countLabel(len(rows), "track")+"?")
	b.WriteString(bad.Render("Cannot be restored.") + "\n")
	b.WriteString("All managed lists.\nLocal audio will be deleted.\n\n")
	limit := min(len(rows), 4, max(1, m.terminalHeight()-10))
	start := min(m.ws.deleteCursor, len(rows)-limit)
	for i, row := range rows[start : start+limit] {
		name := row.Title
		if name == "" {
			name = filepath.Base(row.Path)
		}
		if row.Path != "" && row.Path != name {
			name += " · " + row.Path
		}
		b.WriteString(pick(i+start == m.ws.deleteCursor, oneLine(name)) + "\n")
	}
	if len(rows) > limit {
		fmt.Fprintf(b, "  %d-%d of %d\n", start+1, start+limit, len(rows))
	}
	help := "enter delete · esc cancel"
	if len(rows) > limit {
		help += " · ↑↓ review"
	}
	b.WriteString("\n" + strings.Join(m.helpLines(help), "\n"))
}
