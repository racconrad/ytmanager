package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkspaceDownloadShowsWalkingCatWithoutHidingProgress(t *testing.T) {
	m := sized(scrollModel(t), 112, 30)
	m.run = run{busy: true, verb: "downloaded", total: 4}
	position := func(view string) int {
		for _, line := range strings.Split(view, "\n") {
			if i := strings.Index(line, `/\_/\`); i >= 0 {
				return i
			}
		}
		return -1
	}
	start := position(assertFits(t, m))
	m.run.done = 3
	wide := assertFits(t, m)
	if start < 0 || position(wide) <= start || !strings.Contains(wide, "3/4 processed") {
		t.Fatalf("cat did not walk alongside the real activity status:\n%s", wide)
	}
	catRow := func(view string) int {
		return strings.Count(strings.SplitN(view, `/\_/\`, 2)[0], "\n")
	}
	m.log("A new download log")
	withLog := assertFits(t, m)
	if catRow(withLog) != catRow(wide) ||
		strings.Index(withLog, `/\_/\`) < strings.LastIndex(withLog, "╰") ||
		!strings.Contains(withLog, "A new download log") {
		t.Fatalf("the cat must stay below the workspace and beside the log area:\n%s", withLog)
	}
	m.run.busy = false
	if idle := assertFits(t, m); catRow(idle) != catRow(wide) {
		t.Fatalf("finishing moved or removed the cat footer:\n%s", idle)
	}
	m.run.busy = true
	compact := assertFits(t, sized(m, 30, 12))
	if !strings.Contains(compact, "3/4 processed") || !strings.Contains(compact, "=^.^=") || position(compact) >= 0 {
		t.Fatalf("compact activity lost its status:\n%s", compact)
	}
}

func TestSelectAllVisibleSourcesUsesControlAWithoutTakingAddShortcut(t *testing.T) {
	m := press(sized(scrollModel(t), 30, 12), "3")
	m.marked["A000"] = true
	m.ws.query = "A01"
	m = keyType(m, tea.KeyCtrlA)
	if len(m.pickedURLs(m.filteredSources())) != 10 || !m.marked["A000"] {
		t.Fatalf("expected all ten matching sources to be marked")
	}
	checkScreenContains(t, m, "10/10 selected", "ctrl+a all")
	m = keyType(m, tea.KeyCtrlA)
	checkScreenContains(t, m, "ctrl+a all")
	m = press(m, "a")
	if m.prompt != promptURLs {
		t.Fatal("plain a must still add a link")
	}
}

func TestSourceBulkActionsDoNotStartFromAnEmptyFilter(t *testing.T) {
	m := press(scrollModel(t), "3")
	m.ws.query = "Nothing matches this"
	for _, action := range []string{"sync_picked", "download_picked"} {
		next, cmd := m.runWorkspaceAction(action)
		if next.(model).run.busy || cmd != nil {
			t.Fatalf("%s started despite an empty filter", action)
		}
	}
}

func TestCompactFooterCanReadEveryLineOfTheLatestLog(t *testing.T) {
	m := sized(scrollModel(t), 30, 12)
	m.log("First line\nSecond line\nThird line\nRecovery action")
	checkScreenContains(t, m, "Logs › Recovery action")
	for _, line := range []string{"Third line", "Second line", "First line"} {
		m = keyType(m, tea.KeyPgUp)
		checkScreenContains(t, m, "Logs › "+line)
	}
}
