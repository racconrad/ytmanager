// Package engine ties the manifest, yt-dlp and lyrics together; the TUI and the headless
// sync/add commands both drive it.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"ytmanager/internal/device"
	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

type Options struct {
	Cookies   string `json:"cookies"`    // browser to read YouTube cookies from; "" = none
	AlbumJobs int    `json:"album_jobs"` // simultaneous sources, 1 to 4
	Jobs      int    `json:"jobs"`       // parallel downloads

	// how tracks are tagged and named; "" is the default
	Artists   string `json:"artists,omitempty"`    // "" = main artist, the rest as "(feat. …)" in the title | main | all
	Folders   string `json:"folders,omitempty"`    // "" = Artist/Album | artist | flat
	RawTitles bool   `json:"raw_titles,omitempty"` // keep "(Official Video)" and the like in video titles
}

type EventKind string

const (
	EvLog         EventKind = "log"     // Msg is a line for the user
	EvPlan        EventKind = "plan"    // Total tracks are about to be downloaded
	EvReading     EventKind = "reading" // ID is the source URL
	EvReadDone    EventKind = "read_done"
	EvStart       EventKind = "start"
	EvStage       EventKind = "stage"
	EvProgress    EventKind = "progress"    // Pct of the current track, 0..1
	EvStopped     EventKind = "stopped"     // a track was interrupted by pause or cancel; it stays pending
	EvDone        EventKind = "done"        // Msg is the lyrics kind found, if any
	EvFailed      EventKind = "failed"      // retried on the next sync
	EvUnavailable EventKind = "unavailable" // permanent; needs an alternative
)

type Event struct {
	Kind                  EventKind
	ID, Title             string
	SourceID, SourceTitle string
	Msg                   string
	Pct                   float64
	Total                 int
	Err                   error // machine-readable failure, when available
}

type SourceView struct {
	ID, Type, Title, URL, Creator, Cover, Year                  string
	Done, Total, Pending, Failed, Missing, Unavailable, Removed int
}

// MissingView is an unavailable or failed track plus the first source it appears in.
type MissingView struct {
	ID, Title, Source, URL, Alt, Error string
	Failed                             bool
}

// Engine guards the manifest with a mutex because several download workers update it at once.
type Engine struct {
	mu   sync.Mutex
	m    *manifest.Manifest
	opt  Options
	gate *gate

	// set while a download or sync runs; Add persists requests and wakes the scheduler
	running       bool
	accepting     bool
	more          chan struct{}
	spaceCheck    func(string, uint64) error                                    // nil uses live storage checks
	listSource    func(context.Context, string, string) (*ytdlp.Listing, error) // nil uses yt-dlp
	trackWork     func(context.Context, string, func(Event)) bool               // nil downloads normally
	resolveSong   func(context.Context, string) (string, error)                 // nil uses YouTube Music
	providers     map[string]TrackProvider
	physicalIndex map[string][]string // embedded source IDs, cached for one sync
	physicalFiles []physicalCandidate // readable audio files without embedded IDs, cached for one sync
	activeDL      map[string]chan struct{}
	presence      map[string]bool // refreshed by scans and operations, never poll USB in each render
	probes        *probeCache
	scanDone      atomic.Int64
	scanTotal     atomic.Int64
}

// Open loads (or creates) the destination at root, which may be ~/… or an mtp:// URI.
func Open(root string, opt Options) (*Engine, error) {
	return open(root, opt, true)
}

func OpenExisting(root string, opt Options) (*Engine, error) {
	return open(root, opt, false)
}

func open(root string, opt Options, create bool) (*Engine, error) {
	root, err := device.Resolve(root)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("missing destination path")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	} else {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("not a directory: %s", root)
		}
	}
	m, err := manifest.Load(root)
	if err != nil {
		return nil, err
	}
	e := &Engine{m: m, gate: newGate(), probes: loadProbeCache(m.Root)}
	e.providers = e.defaultProviders()
	e.SetOptions(opt)
	return e, nil
}

func (e *Engine) Root() string { return e.m.Root }

func (e *Engine) Format() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m.Format
}

func (e *Engine) SetFormat(f string) error {
	if !ytdlp.ValidFormat(f) {
		return fmt.Errorf("invalid format %q (opus, m4a, mp3)", f)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.m.Format = f
	return e.m.Save()
}

func (e *Engine) SourceCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.m.Sources)
}

func (e *Engine) Snapshot() []SourceView {
	return e.snapshot(e.trackPresent)
}

// CatalogSources reads saved source counts and cached presence without touching audio files.
func (e *Engine) CatalogSources() []SourceView {
	return e.snapshot(e.catalogTrackPresent)
}

func (e *Engine) snapshot(present func(*manifest.Track) bool) []SourceView {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]SourceView, len(e.m.Sources))
	for i, s := range e.m.Sources {
		var d, p, f, m, u, r int
		for _, id := range s.Tracks {
			if track := e.m.Tracks[id]; track != nil {
				switch track.Status {
				case manifest.StatusDone:
					if present(track) {
						d++
					} else {
						m++
					}
				case manifest.StatusPending:
					p++
				case manifest.StatusFailed:
					f++
				case manifest.StatusUnavailable:
					u++
				case manifest.StatusRemoved:
					r++
				}
			}
		}
		out[i] = SourceView{ID: s.ID, Type: s.Type, Title: s.Title, URL: s.URL, Creator: s.Creator,
			Cover: s.Cover, Year: s.Year, Done: d, Total: len(s.Tracks), Pending: p, Failed: f, Missing: m, Unavailable: u, Removed: r}
	}
	return out
}

func (e *Engine) catalogTrackPresent(t *manifest.Track) bool {
	present, known := e.presence[t.Path]
	return t.Path != "" && (!known || present)
}

// trackPresent caches filesystem presence between explicit operations.
func (e *Engine) trackPresent(t *manifest.Track) bool {
	if e.presence == nil {
		e.presence = map[string]bool{}
	}
	if present, ok := e.presence[t.Path]; ok {
		return present
	}
	present := e.m.AudioPath(t) != ""
	e.presence[t.Path] = present
	return present
}

// WarmPresence populates the filesystem presence cache asynchronously without holding e.mu during I/O.
func (e *Engine) WarmPresence(ctx context.Context) {
	e.mu.Lock()
	root := e.m.Root
	var tracks []manifest.Track
	for _, t := range e.m.Tracks {
		if t.Path != "" && t.Status != manifest.StatusRemoved {
			tracks = append(tracks, *t)
		}
	}
	e.mu.Unlock()
	res := make(map[string]bool, len(tracks))
	tmp := &manifest.Manifest{Root: root}
	for i := range tracks {
		if ctx.Err() != nil {
			return
		}
		res[tracks[i].Path] = tmp.AudioPath(&tracks[i]) != ""
	}
	e.mu.Lock()
	e.presence = res
	e.mu.Unlock()
}

// SetPresenceFromScan updates the presence cache directly from scanned physical rows.
func (e *Engine) SetPresenceFromScan(rows []LibraryTrack) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.presence == nil {
		e.presence = map[string]bool{}
	}
	for _, r := range rows {
		if r.Path != "" {
			e.presence[r.Path] = !r.Missing
		}
	}
}

// Missing lists unavailable and failed tracks in source order.
func (e *Engine) Missing() []MissingView {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[string]bool{}
	var out []MissingView
	for _, s := range e.m.Sources {
		for _, id := range s.Tracks {
			if t := e.m.Tracks[id]; t != nil && (t.Status == manifest.StatusUnavailable || t.Status == manifest.StatusFailed) && !seen[id] {
				seen[id] = true
				out = append(out, MissingView{id, t.Title, s.Title, s.URL, t.Alt, t.Error, t.Status == manifest.StatusFailed})
			}
		}
	}
	return out
}

// SetAlt resolves url (a song, album or playlist) to the track that replaces id and marks it
// pending. In an album or playlist it picks the song with the most similar title.
func (e *Engine) SetAlt(ctx context.Context, id, url string) (string, error) {
	e.mu.Lock()
	t := e.m.Tracks[id]
	e.mu.Unlock()
	if t == nil {
		return "", fmt.Errorf("track %s does not exist", id)
	}
	if ytdlp.Provider(url) != ytdlp.YouTube {
		return "", fmt.Errorf("use a YouTube Music link for the alternative")
	}
	l, err := ytdlp.List(ctx, url, e.opt.Cookies)
	if err != nil {
		return "", err
	}
	i := 0
	if len(l.Entries) != 1 {
		if i = ytdlp.Match(t.Title, t.Duration, l.Entries); i < 0 {
			return "", fmt.Errorf("could not find %q in %s; paste the song link instead", t.Title, l.Title)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t.Alt, t.Status, t.Error, t.Fails = l.Entries[i].ID, manifest.StatusPending, "", 0
	return l.Entries[i].Title, e.m.Save()
}

// Reorganize marks every downloaded track pending, so the next sync downloads it again with the
// current format and settings; each old file is removed once its replacement is in place.
func (e *Engine) Reorganize() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, t := range e.m.Tracks {
		if t.Status == manifest.StatusDone && !t.Local {
			t.Status = manifest.StatusPending
			n++
		}
	}
	return n, e.m.Save()
}

// Retry marks an unavailable track pending again, in case the error was temporary.
func (e *Engine) Retry(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t := e.m.Tracks[id]; t != nil {
		t.Status, t.Fails = manifest.StatusPending, 0
	}
	return e.m.Save()
}

func (e *Engine) Remove(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return fmt.Errorf("wait for the current operation to finish")
	}
	previousSources := append([]*manifest.Source(nil), e.m.Sources...)
	previousQueue := append([]manifest.SourceRequest(nil), e.m.Queue...)
	var legacyPaths []string
	if source := e.m.Source(id); source != nil {
		e.completeRequest(source.URL)
		legacy := e.m.PlaylistText(source, false)
		path := filepath.Join(e.m.Root, manifest.SafeName(source.Title)+".m3u8")
		if data, err := os.ReadFile(path); err == nil && string(data) == legacy {
			legacyPaths = append(legacyPaths, path)
		}
		export := strings.TrimSuffix(path, ".m3u8") + ".m3u"
		if data, err := os.ReadFile(export); err == nil && string(data) == manifest.ExportMarker+strings.TrimPrefix(legacy, "#EXTM3U\n") {
			legacyPaths = append(legacyPaths, export)
		}
	}
	e.m.Remove(id)
	if err := e.m.Save(); err != nil {
		e.m.Sources, e.m.Queue = previousSources, previousQueue
		return err
	}
	for _, p := range legacyPaths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return e.m.WritePlaylists()
}

func Human(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KB", b/1024)
}
