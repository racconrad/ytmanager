package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkspaceReaddTrackIsScopedAndConfirmable(t *testing.T) {
	m, _ := workspaceRemovalModel(t)
	if _, err := m.eng.ExcludeSourceTracks("first", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	m = press(m, "3", "enter", "tab")
	index := -1
	for i, action := range m.workspaceActions() {
		if action.label == "Re-add track…" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("source with removed tracks has no re-add action")
	}
	for m.ws.sideCursor < index {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if got := assertFits(t, sized(m, 30, 12)); !strings.Contains(got, "Re-add track") || !strings.Contains(got, "One") {
		t.Fatalf("picker does not fit: %s", got)
	}
	m = press(m, "enter")
	if got := assertFits(t, sized(m, 30, 12)); !strings.Contains(got, "First") || !strings.Contains(got, "One") {
		t.Fatalf("confirmation does not identify target: %s", got)
	}
	m = press(m, "esc", "esc")
	if got := m.eng.Collection("first").Tracks; len(got) != 0 {
		t.Fatalf("cancel changed source: %+v", got)
	}
	m = press(m, "tab")
	for m.ws.sideCursor < index {
		m = press(m, "down")
	}
	m = press(m, "enter", "enter")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	for m.run.busy {
		if cmd == nil {
			t.Fatal("restore stopped producing events")
		}
		out := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { out <- c() }(cmd)
		select {
		case msg := <-out:
			next, cmd = m.Update(msg)
			m = next.(model)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for re-add operation")
		}
	}
	if got := m.eng.Collection("first").Tracks; len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("selected source = %+v", got)
	}
	if got := m.eng.Collection("second").Tracks; len(got) != 2 {
		t.Fatalf("other source changed = %+v", got)
	}
}

func TestWorkspaceReaddDisconnectDoesNotResumeTheQueue(t *testing.T) {
	m, root := workspaceRemovalModel(t)
	m = press(m, "3", "enter", "tab", "enter")
	m.run = run{busy: true, verb: "re-added", queue: []string{"https://example.invalid"}, cancel: func() {}}
	m.unplugged(root)
	if m.ws.readd != nil {
		t.Fatal("disconnected destination left the modal over the device picker")
	}
	m.reconnect()
	if m.eng == nil || m.run.busy {
		t.Fatal("reconnect re-opened a general download from a single-track operation")
	}
}
