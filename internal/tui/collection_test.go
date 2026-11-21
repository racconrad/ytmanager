package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

func TestCollectionShowsSourceOrderAndReturnsToSelection(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Merge("track", "other", "Other", "url", nil)
	data.Merge("album", "album", "Record", "url", []manifest.Entry{
		{ID: "b", Title: "Second alphabetically"},
		{ID: "a", Title: "First alphabetically", Unavailable: true},
		{ID: "b", Title: "Second alphabetically"},
	})
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.cursor = 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = sized(next.(model), 80, 24)
	view := assertFits(t, m)
	first, second := strings.Index(view, "Second alphabetically"), strings.Index(view, "First alphabetically")
	if first < 0 || second <= first || strings.Count(view, "Second alphabetically") != 2 || !strings.Contains(view, "Unavailable") {
		t.Fatalf("missing ordered tracks and statuses:\n%s", view)
	}
	m = keyType(m, tea.KeyEsc)
	if m.screen != screenSources || m.cursor != 1 {
		t.Fatalf("did not return to selected source: %v/%d", m.screen, m.cursor)
	}
}

func TestCollectionProgressAndSelectionFitNarrowTerminal(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	src := data.Merge("playlist", "list", "Collection", "url", []manifest.Entry{
		{ID: "first", Title: "Opening Song"},
		{ID: "last", Title: "Closing Song"},
	})
	src.Cover = "https://example.com/cover.jpg"
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = sized(next.(model), 36, 12)
	m = keyType(m, tea.KeyEnd)
	m.apply(engine.Event{Kind: engine.EvStart, ID: "last", Title: "Closing Song"})
	m.apply(engine.Event{Kind: engine.EvStage, ID: "last", Msg: "Reading metadata"})
	if got := assertFits(t, m); !strings.Contains(got, "Reading metadata") || !strings.Contains(got, "Preview cover") {
		t.Fatalf("missing cover action or phase:\n%s", got)
	}
	m.apply(engine.Event{Kind: engine.EvProgress, ID: "last", Pct: 0.45})
	if got := assertFits(t, m); !strings.Contains(got, "Downloading 45%") {
		t.Fatalf("missing active progress:\n%s", got)
	}
	if err := openCover("file:///etc/passwd"); err == nil {
		t.Fatal("cover opener accepted a local file")
	}
}
