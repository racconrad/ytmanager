package tui

import (
	"strings"
	"testing"
	"time"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func TestContinueAllActionIsVisibleAndResumesSavedQueue(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Queue = []manifest.SourceRequest{{URL: "https://music.youtube.com/playlist?list=pending"}}
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 112, 30
	m.ws.section = secDetails
	if view := assertFits(t, m); !strings.Contains(view, "Continue all downloads") {
		t.Fatalf("Continue all action is not visible in Details:\n%s", view)
	}
	for _, section := range []navSection{secDetails, secSources} {
		m.ws.section = section
		found := false
		for _, action := range m.workspaceActions() {
			if action.id == "continue_all" && action.label == "Continue all downloads" {
				found = true
			}
		}
		if !found {
			t.Fatalf("Continue all downloads missing from section %v", section)
		}
	}

	next, cmd := m.runWorkspaceAction("continue_all")
	updated := next.(model)
	if cmd == nil || !updated.run.busy {
		t.Fatal("continue action did not start a download run")
	}
	updated.run.cancel()
	deadline := time.After(time.Second)
	for {
		select {
		case msg := <-updated.run.ch:
			if _, ok := msg.(doneMsg); ok {
				return
			}
		case <-deadline:
			t.Fatal("cancelled continue run did not stop")
		}
	}
}

func TestLinksAddedDuringRunPersistForNextSession(t *testing.T) {
	root := t.TempDir()
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.run.busy = true
	m.add([]string{"https://music.youtube.com/playlist?list=queued-album"})

	reopened, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Queue) != 1 || reopened.Queue[0].URL != "https://music.youtube.com/playlist?list=queued-album" {
		t.Fatalf("saved queue after adding link = %+v", reopened.Queue)
	}
	reopenedEngine, err := engine.Open(root, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reopenedEngine.HasPending() || reopenedEngine.QueuedSources() != 1 {
		t.Fatal("saved link was not resumable after reopening the destination")
	}
}
