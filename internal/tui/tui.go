// Package tui is the Bubble Tea interface. Each screen has its own file with its key handler and
// view (devices.go, sources.go, settings.go, missing.go); input.go routes keys and the text
// prompt, connection.go watches for unplugged devices, cat.go is the mascot and view.go holds
// styles and shared helpers.
package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/device"
	"ytmanager/internal/engine"
	"ytmanager/internal/manifest"
)

type screen int

const (
	screenDevices  screen = iota // pick a drive or phone
	screenBrowse                 // pick a folder inside it
	screenSources                // a destination is open: sources and downloads
	screenSettings               // download settings
	screenLibrary                // search and manage downloaded music
	screenMissing                // unavailable tracks and their alternatives
	screenCollection
	screenArtist
)

// prompt is what the text input is asking for; promptNone means it is hidden.
type prompt int

const (
	promptNone prompt = iota
	promptPath
	promptMkdir
	promptURLs
	promptAlt
	promptArtist
)

type model struct {
	opt        engine.Options
	format     string // -format flag, applied to every destination opened
	screen     screen
	eng        *engine.Engine // nil on screenDevices and screenBrowse
	cursor     int
	ws         workspaceState
	library    libraryState
	collection collectionState
	artist     artistState
	covers     coverState
	overlay    *coverOverlay
	nextCover  *coverPlacement

	mounts         []device.Mount
	mountRoots     []string
	recent         []device.Mount
	history        []recentFolder
	historyPath    string
	startup        bool
	opening        string
	browse         string // folder shown in screenBrowse
	dirs           []string
	browseStats    manifest.Summary
	browseHasStats bool
	browseSubStats map[string]manifest.Summary
	browseReturn   bool
	browseFrom     screen
	browseCursor   int
	settingsFrom   screen
	settingsCursor int
	shortcutHelp   bool
	shortcutCursor int

	input  textinput.Model
	prompt prompt
	altFor string // track that receives the alternative being typed

	run        run
	lost       *lost           // destination unplugged by accident; reopened when it comes back
	marked     map[string]bool // sources picked with space
	logs       []string
	logScroll  int // wrapped lines above the live log tail; zero follows new messages
	cat        cat
	bar        progress.Model
	width      int
	height     int
	space      device.SpaceInfo
	spaceKnown bool
}

// run is the state of the current sync. Its messages carry ch, so events from a cancelled run
// that arrive after a new one started are recognised and ignored.
type run struct {
	readingURL   string
	readingSince time.Time
	busy         bool
	verb         string // what finished tracks were: downloaded | moved
	urls         []string
	ch           chan tea.Msg
	cancel       context.CancelFunc
	queue        []string // links added just as a sync was ending; started right after it
	jobs         map[string]*job
	order        []string
	total        int // tracks to download in this sync
	done         int
	failed       int
	unavailable  int
}

type job struct {
	sourceID, sourceTitle string
	title                 string
	pct                   float64
	stage                 string
	hasProgress           bool
}

type (
	eventMsg struct {
		ch chan tea.Msg
		engine.Event
	}
	doneMsg struct {
		ch  chan tea.Msg
		err error
	}
	altMsg struct {
		title, sourceURL string
		err              error
	}
	mountsMsg struct {
		list       []device.Mount
		recent     []device.Mount
		history    []recentFolder
		roots      []string
		watched    string // folder that was open when checked
		gone       bool   // it no longer exists: the device was unplugged
		lostPath   string // unplugged destination that was checked
		back       bool   // it exists again: the device was plugged back in
		space      device.SpaceInfo
		spaceKnown bool
	}
)

// Run opens the TUI; with an empty dest it starts on the device picker.
func Run(dest string, opt engine.Options, format string) error {
	in := textinput.New()
	in.Prompt = hi.Render("› ")
	in.CharLimit = 4096
	overlay := newCoverOverlay(os.Stdout)
	defer overlay.close()
	m := model{
		opt: opt, format: format, input: in, width: 80, height: 24,
		bar:         progress.New(progress.WithGradient("#7c3aed", "#f0abfc"), progress.WithWidth(24)),
		marked:      map[string]bool{},
		run:         run{jobs: map[string]*job{}},
		overlay:     overlay,
		historyPath: destinationHistoryPath(),
	}
	if dest != "" {
		m.history = readDestinationHistory(m.historyPath)
		m.mountRoots = device.RemovableRoots()
		if err := m.openDest(dest); err != nil {
			return err
		}
		m.setSection(secDetails)
	} else {
		m.startup = true
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(overlay)).Run()
	if fm, ok := final.(model); ok && fm.run.cancel != nil {
		fm.run.cancel()
	}
	if fm, ok := final.(model); ok && fm.library.cancel != nil {
		fm.library.cancel()
	}
	return err
}

func (m model) Init() tea.Cmd {
	var initial tea.Cmd
	if m.screen == screenLibrary && m.eng != nil {
		initial = func() tea.Msg { return startLibraryMsg{m.eng} }
	}
	return tea.Batch(catTick(), m.detect(0), initial)
}
func (m model) viewSignature() string {
	return fmt.Sprintf("%d:%d:%d:%s:%s:%s:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v",
		m.screen, m.ws.section, m.ws.focus, m.ws.albumKey, m.ws.folderDir, m.ws.sourceID,
		m.artist.searching, m.artist.loading, m.artist.artist != nil,
		m.library.tools, m.library.plan != nil, m.library.details != nil, m.library.report != nil, m.library.candidates != nil, m.library.confirm != nil, m.library.albums,
		m.shortcutHelp,
		m.libraryOverlayVisible(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevSig := m.viewSignature()
	nextModel, cmd := m.updateInner(msg)
	if nm, ok := nextModel.(model); ok && nm.overlay != nil && nm.viewSignature() != prevSig {
		return nm, tea.Batch(cmd, tea.ClearScreen)
	}
	return nextModel, cmd
}

func (m model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case startLibraryMsg:
		if m.screen == screenLibrary && msg.eng == m.eng && !m.library.scanning {
			cmd := m.openInitialLibrary()
			return m, cmd
		}
		return m, nil
	case libraryDiscoveredMsg:
		if m.activeLibraryScan(msg.scan) {
			if msg.scan.ctx.Err() == nil {
				m.applyLibraryDiscovery(msg.rows)
			}
			return m, msg.scan.next
		}
		return m, nil
	case libraryScannedMsg:
		if m.activeLibraryScan(msg.scan) {
			if err := msg.scan.ctx.Err(); err != nil {
				msg.err = err
			}
			if m.screen != screenLibrary {
				cursor := m.cursor
				m.cursor = m.library.catalogCursor
				m.finishLibraryScan(msg)
				m.library.catalogCursor, m.cursor = m.cursor, cursor
				return m, nil
			}
			m.finishLibraryScan(msg)
			if m.library.settingsAction != "" {
				action := m.library.settingsAction
				m.library.settingsAction = ""
				if msg.err == nil && (action == "organize" || action == "convert") {
					m.library.tools = true
					if action == "organize" {
						m.library.layout = m.opt.Folders
					}
					if action == "convert" {
						m.library.targetFormat = m.eng.Format()
					}
				}
			}
			if msg.err == nil && m.screen == screenLibrary && !m.library.tools {
				cmd := m.requestCover(m.libraryCoverKey())
				return m, cmd
			}
		}
		return m, nil
	case libraryPlannedMsg:
		if msg.eng == m.eng {
			if m.library.cancel != nil {
				m.library.cancel()
			}
			m.library.cancel, m.library.busy = nil, false
			if msg.err != nil {
				m.fail(msg.err)
			} else {
				m.library.plan = &msg.plan
				m.library.tools = false
				m.cursor = 0
			}
		}
		return m, nil
	case libraryDeletedMsg:
		if msg.eng == m.eng {
			if msg.err != nil {
				msg.report.Items = append(msg.report.Items, engine.FileResult{Outcome: "Operation error", Error: msg.err.Error()})
			}
			if msg.scanErr != nil {
				msg.report.Items = append(msg.report.Items, engine.FileResult{Outcome: "Folder scan incomplete", Error: msg.scanErr.Error()})
			}
			if m.library.cancel != nil {
				m.library.cancel()
			}
			m.library.cancel = nil
			m.library.busy = false
			m.library.complete = msg.scanErr == nil
			if m.library.complete {
				m.library.all, m.library.inventoryKnown = msg.tracks, true
				m.library.allGen++
				m.library.cachedValid = false
				m.library.scanNotice = ""
			} else {
				m.library.scanNotice = "Scan incomplete"
			}
			m.library.report = &msg.report
			m.library.marked = map[string]bool{}
			m.cursor = 0
			m.refreshWorkspace()
			m.log(msg.report.Summary())
			if msg.err != nil {
				m.fail(msg.err)
			}
		}
		return m, nil
	case workspaceRemovalMsg:
		return m.finishWorkspaceRemoval(msg)
	case physicalSyncDoneMsg:
		return m.finishPhysicalSync(msg)
	case coverOpenedMsg:
		if msg.err != nil && (m.screen == screenCollection || m.screen == screenArtist) {
			m.fail(fmt.Errorf("could not open cover: %w", msg.err))
		}
		return m, nil
	case coverLoadedMsg:
		m.storeCover(msg)
		return m, nil
	case artistSearchMsg:
		if m.screen != screenArtist || m.artist.query != msg.query {
			return m, nil
		}
		m.artist.searching = false
		if msg.err != nil {
			m.artist.err = msg.err.Error()
			return m, nil
		}
		m.artist.results = msg.results
		m.cursor = 0
		if len(msg.results) == 1 {
			return m, m.openArtistQuery(msg.results[0].URL)
		}
		return m, m.requestCover(m.artistHighlightedCover())
	case artistLoadedMsg:
		if m.screen != screenArtist {
			return m, nil
		}
		m.artist.searching = false
		m.artist.loading = false
		if msg.err != nil {
			m.artist.err = msg.err.Error()
			return m, nil
		}
		m.artist.artist = msg.artist
		m.artist.tab = 0
		m.cursor = 0
		return m, m.requestCover(m.artistHighlightedCover())
	case inspectLoadedMsg:
		if msg.eng == nil || msg.eng != m.eng {
			return m, nil
		}
		m.artist.inspecting = false
		if msg.from != m.screen {
			return m, nil
		}
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		if msg.save {
			id, err := m.eng.SaveSourceMetadata(msg.listing, msg.url)
			if err != nil {
				m.fail(err)
				return m, nil
			}
			m.log(ok.Render("Saved metadata: " + msg.listing.Title))
			m.refreshWorkspace()
			m.collection = collectionState{id: id, sourceCursor: m.cursor, returnTo: m.screen}
			return m, m.requestCover(msg.listing.Cover)
		}
		m.collection = collectionState{
			preview:      msg.listing,
			previewURL:   msg.url,
			sourceCursor: m.cursor,
			returnTo:     m.screen,
		}
		m.screen, m.cursor = screenCollection, 0
		return m, m.requestCover(msg.listing.Cover)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = max(10, min(30, msg.Width/4))
		m.input.Width = max(1, msg.Width-6)
		m.scrollLogs(0)
		if m.screen == screenLibrary {
			cmd := m.requestCover(m.libraryCoverKey())
			return m, cmd
		}
	case tea.MouseMsg:
		return m.mouse(msg)
	case catTickMsg:
		m.cat.frame++
		if m.isAnimating() {
			return m, catTick()
		}
		return m, idleTick()
	case mountsMsg:
		delay := 2 * time.Second
		m.mountRoots = msg.roots
		m.replaceMounts(msg.list, msg.recent)
		if m.startup {
			m.history, m.startup = msg.history, false
			delay = 0
		}
		if m.eng != nil && msg.watched == m.eng.Root() {
			m.space, m.spaceKnown = msg.space, msg.spaceKnown
		}
		// only if the same folder is still open; otherwise the next check covers the new one
		if msg.gone && msg.watched == m.watched() {
			m.unplugged(msg.watched)
		}
		var cmd tea.Cmd
		if m.opening == "" && msg.back && msg.lostPath == m.lostPath() {
			cmd = m.reconnect()
		}
		return m, tea.Batch(cmd, m.detect(delay))
	case recentOpenedMsg:
		m.opening = ""
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.acceptDestination(msg.destinationOpen)
		cmd := m.openDestinationDetails()
		return m, cmd
	case eventMsg:
		if msg.ch == m.run.ch {
			m.apply(msg.Event)
		}
		return m, wait(msg.ch) // keep draining a stale run so its goroutine can finish
	case doneMsg:
		if msg.ch != m.run.ch {
			return m, nil
		}
		cmd := m.finish(msg.err)
		return m, cmd
	case altMsg:
		cmd := m.altResolved(msg)
		return m, cmd
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) log(s string) {
	if m.logScroll > 0 {
		m.logScroll += len(m.wrapLog(s))
	}
	m.logs = append(m.logs, s)
	if len(m.logs) > logLimit {
		m.logs = m.logs[len(m.logs)-logLimit:]
	}
	if m.logScroll > 0 {
		m.scrollLogs(0)
	}
}

func (m *model) fail(err error) { m.log(bad.Render("✗ " + err.Error())) }
