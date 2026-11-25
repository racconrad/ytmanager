package tui

import (
	"context"
	"os"
	"path/filepath"
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
	m = sized(press(m, "3", "down", "v"), 80, 24)
	view := assertFits(t, m)
	first, second := strings.Index(view, "01 Second alphabetically"), strings.Index(view, "02 First alphabetically")
	third := strings.Index(view, "03 Second alphabetically")
	if first < 0 || second <= first || third <= second {
		t.Fatalf("missing ordered tracks:\n%s", view)
	}
	m = press(m, "down")
	if !strings.Contains(assertFits(t, m), "Unavailable") {
		t.Fatalf("missing Unavailable status in detail panel")
	}
	m = keyType(m, tea.KeyEsc)
	if m.ws.section != secSources || m.ws.sourceID != "" || m.ws.listCursor != 1 {
		t.Fatalf("did not return to selected source: %v/%q/%d", m.ws.section, m.ws.sourceID, m.ws.listCursor)
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
	m = sized(press(m, "3", "v"), 36, 12)
	m = keyType(m, tea.KeyEnd)
	m.apply(engine.Event{Kind: engine.EvStart, ID: "last", Title: "Closing Song"})
	m.apply(engine.Event{Kind: engine.EvStage, ID: "last", Msg: "Reading metadata"})
	if got := assertFits(t, m); !strings.Contains(got, "Reading metadata") {
		t.Fatalf("missing live phase:\n%s", got)
	}
	m.apply(engine.Event{Kind: engine.EvProgress, ID: "last", Pct: 0.45})
	if got := assertFits(t, m); !strings.Contains(got, "Downloading 45%") {
		t.Fatalf("missing active progress:\n%s", got)
	}
	if err := openCover("file:///etc/passwd"); err == nil {
		t.Fatal("cover opener accepted a local file")
	}
}

func TestSourceFilesSyncViewAndAlbumErrorDetection(t *testing.T) {
	root := t.TempDir()
	data, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	syncedRel := filepath.Join("Artist", "Record", "01 - Synced Song.opus")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, syncedRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, syncedRel), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	data.Merge("album", "OLAK_err", "Record", "https://music.youtube.com/playlist?list=OLAK_err", []manifest.Entry{
		{ID: "ok1", Title: "Synced Song"},
		{ID: "fail1", Title: "Broken Song"},
		{ID: "miss1", Title: "Vanished Song"},
	})
	data.Tracks["ok1"].Path = syncedRel
	data.Tracks["ok1"].Status = manifest.StatusDone
	data.Tracks["fail1"].Status = manifest.StatusFailed
	data.Tracks["fail1"].Error = "HTTP Error 403: Forbidden"
	data.Tracks["miss1"].Path = filepath.Join("Artist", "Record", "03 - Vanished Song.opus")
	data.Tracks["miss1"].Status = manifest.StatusDone
	if err := data.Save(); err != nil {
		t.Fatal(err)
	}

	m := newModel()
	if err := m.openDest(root); err != nil {
		t.Fatal(err)
	}
	m.eng.WarmPresence(context.Background())
	m = sized(press(m, "3"), 100, 26)
	srcView := assertFits(t, m)
	if !strings.Contains(srcView, "1 failed") || !strings.Contains(srcView, "1 missing") {
		t.Fatalf("sources view did not surface album errors:\n%s", srcView)
	}
	if missing := m.eng.Missing(); len(missing) != 1 || !missing[0].Failed || !strings.Contains(missing[0].Error, "403") {
		t.Fatalf("Missing() did not include general download failure: %+v", missing)
	}

	// Open Source files & sync view directly with S
	m = press(m, "S")
	if m.ws.section != secSources || m.ws.sourceID != "OLAK_err" {
		t.Fatalf("expected drilled source files view, got section=%v sourceID=%q", m.ws.section, m.ws.sourceID)
	}
	filesView := assertFits(t, m)
	for _, want := range []string{"Sources › Record", "1/3 synced", "✓ Synced", syncedRel, "✗ Failed", "HTTP Error 403: Forbidden", "! Missing"} {
		if !strings.Contains(filesView, want) {
			t.Fatalf("source files view missing %q:\n%s", want, filesView)
		}
	}

	// RetrySource resets failed and missing tracks to pending
	n, err := m.eng.RetrySource("OLAK_err")
	if err != nil || n != 2 {
		t.Fatalf("RetrySource = %d, %v; want 2", n, err)
	}
}
