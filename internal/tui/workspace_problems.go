package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
)

func (m model) currentProblem() (engine.IssueItem, bool) {
	items := m.filteredIssues()
	if m.ws.listCursor >= 0 && m.ws.listCursor < len(items) {
		return items[m.ws.listCursor], true
	}
	return engine.IssueItem{}, false
}

func (m model) primaryProblemAction() string {
	item, found := m.currentProblem()
	if !found || item.TrackID == "" {
		return ""
	}
	if item.Kind == "unavailable" {
		return "set_alt"
	}
	if (item.Kind == "download_failed" || item.Kind == "missing_file") && item.SourceURL != "" {
		return "retry_issue"
	}
	return ""
}

func (m model) problemHelp() string {
	switch m.primaryProblemAction() {
	case "set_alt":
		return "enter alternative · esc back · d delete · ctrl+a all · space mark · tab actions"
	case "retry_issue":
		return "enter retry · esc back · d delete · ctrl+a all · space mark · tab actions"
	}
	if _, found := m.currentProblem(); found {
		return "enter actions · esc back · d delete · ctrl+a all · space mark"
	}
	return "esc details · ? keys · q quit"
}

func (m *model) retryProblem() tea.Cmd {
	item, found := m.currentProblem()
	if !found || item.TrackID == "" || item.SourceURL == "" {
		return nil
	}
	switch item.Kind {
	case "download_failed", "unavailable", "missing_file":
	default:
		return nil
	}
	if m.run.busy || m.library.busy {
		m.log("Wait for the current operation to finish before retrying.")
		return nil
	}
	if err := m.eng.Retry(item.TrackID); err != nil {
		m.fail(err)
		return nil
	}
	return m.download([]string{item.SourceURL})
}
