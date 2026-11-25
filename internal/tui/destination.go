package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/device"
	"ytmanager/internal/engine"
)

func (m *model) changeDestination() {
	if m.run.busy {
		m.log(dim.Render("Wait for the current operation to finish, or cancel it with x, before changing destination."))
		return
	}
	m.browseReturn, m.browseFrom, m.browseCursor = true, m.screen, m.cursor
	m.openBrowser(m.eng.Root())
}

func (m *model) returnToDestination() bool {
	if m.eng == nil || !m.browseReturn {
		return false
	}
	m.screen, m.cursor, m.browseReturn = m.browseFrom, m.browseCursor, false
	return true
}

type destinationOpen struct {
	eng        *engine.Engine
	space      device.SpaceInfo
	spaceKnown bool
	err        error
}

func loadDestination(path string, opt engine.Options, format string, existing bool) destinationOpen {
	open := engine.Open
	if existing {
		open = engine.OpenExisting
	}
	eng, err := open(path, opt)
	if err != nil {
		return destinationOpen{err: err}
	}
	if format != "" {
		if err := eng.SetFormat(format); err != nil {
			return destinationOpen{err: err}
		}
	}
	space, err := device.Space(eng.Root())
	return destinationOpen{eng: eng, space: space, spaceKnown: err == nil}
}

func (m *model) openDest(p string) error {
	dest := loadDestination(p, m.opt, m.format, false)
	if dest.err == nil {
		m.acceptDestination(dest)
	}
	return dest.err
}

func (m *model) acceptDestination(dest destinationOpen) {
	if m.library.cancel != nil {
		m.library.cancel()
	}
	m.eng, m.screen, m.cursor, m.logs = dest.eng, screenSources, 0, nil
	m.logScroll = 0
	m.shortcutHelp = false
	m.library = libraryState{}
	m.marked = map[string]bool{}
	m.artist = artistState{}
	m.collection = collectionState{}
	m.browseReturn = false
	m.space, m.spaceKnown = dest.space, dest.spaceKnown
	m.lost = nil // opening any destination ends the wait for an unplugged one
	m.initWorkspace()
	m.rememberDestination(dest.eng.Root())
}

func (m *model) use(p string) tea.Cmd {
	if err := m.openDest(p); err != nil {
		m.fail(err)
		return nil
	}
	return m.openDestinationDetails()
}

func (m *model) openDestinationDetails() tea.Cmd {
	m.setSection(secDetails)
	return m.openInitialLibrary()
}
