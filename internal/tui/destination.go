package tui

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
