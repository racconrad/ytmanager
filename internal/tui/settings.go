package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/engine"
)

var (
	browsers = []string{"", "firefox", "chrome", "chromium", "brave", "edge", "vivaldi", "opera"}
	artists  = []string{"", "main", "all"}
	folders  = []string{"", "artist", "flat"}
	settings = []string{"Format", "Parallel tracks", "Browser cookies", "Artists", "Folders", "Video titles", "Parallel albums", "Organize existing files (preview)", "Download everything again", "Choose destination folder", "Convert existing audio (preview)", "Library management help"}
)

const (
	moveFiles         = 7
	reorganize        = 8
	chooseDestination = 9
	libraryGuide      = 11
)

// labels shows the empty default of artists and folders.
var labels = map[string]string{"": "feat. in title", "main": "main only", "all": "all in artist"}
var folderLabels = map[string]string{"": "Artist/Album", "artist": "Artist", "flat": "no folders"}

func (m *model) openSettings() {
	m.settingsFrom, m.settingsCursor = m.screen, m.cursor
	m.screen, m.cursor = screenSettings, 0
}

func (m model) settingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	step := 0
	switch k.String() {
	case "enter":
		switch m.cursor {
		case moveFiles:
			return m, m.moveFiles()
		case reorganize:
			return m, m.reorganize()
		case chooseDestination:
			m.changeDestination()
			return m, nil
		case 10:
			if !m.run.busy {
				return m, m.openLibrary("convert")
			}
		case libraryGuide:
			if m.library.scanning {
				m.log("Wait for the folder scan to stop.")
				return m, nil
			}
			m.library.help, m.library.settingsAction = true, "help"
			m.screen, m.cursor = screenLibrary, 0
			return m, nil
		}
		m.closeSettings()
	case "esc", "o":
		m.closeSettings()
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(settings)-1, m.cursor+1)
	case "left", "h":
		step = -1
	case "right", "l", " ":
		step = 1
	}
	if step != 0 {
		m.changeSetting(step)
	}
	return m, nil
}

func (m *model) closeSettings() {
	if m.settingsFrom == screenLibrary {
		m.screen, m.cursor = screenLibrary, m.settingsCursor
	} else {
		m.screen, m.cursor = screenSources, m.settingsCursor
	}
}

func (m *model) changeSetting(step int) {
	if m.run.busy {
		m.log(dim.Render("Wait for the download to finish before changing settings."))
		return
	}
	var err error
	switch m.cursor {
	case 0:
		err = m.eng.SetFormat(cycle(formats, m.eng.Format(), step))
	case 1:
		m.opt.Jobs = min(8, max(1, m.opt.Jobs+step))
	case 2:
		m.opt.Cookies = cycle(browsers, m.opt.Cookies, step)
	case 3:
		m.opt.Artists = cycle(artists, m.opt.Artists, step)
	case 4:
		m.opt.Folders = cycle(folders, m.opt.Folders, step)
	case 5:
		m.opt.RawTitles = !m.opt.RawTitles
	case 6:
		m.opt.AlbumJobs = min(4, max(1, max(1, m.opt.AlbumJobs)+step))
	case moveFiles, reorganize, chooseDestination, 10, 11:
		return
	}
	if m.cursor > 0 {
		m.eng.SetOptions(m.opt)
		err = engine.SaveOptions(m.opt)
	}
	if err != nil {
		m.fail(err)
	}
}

// moveFiles rearranges the downloaded files to the Folders setting without downloading them.
func (m *model) moveFiles() tea.Cmd {
	if m.run.busy {
		m.log(dim.Render("Wait for the download to finish before moving files."))
		return nil
	}
	return m.openLibrary("organize")
}

// reorganize re-downloads the whole library with the current settings and goes back to the
// sources screen to show the progress.
func (m *model) reorganize() tea.Cmd {
	if m.run.busy {
		m.log(dim.Render("Wait for the download to finish before reorganizing."))
		return nil
	}
	var urls []string
	for _, s := range m.eng.Snapshot() {
		urls = append(urls, s.URL)
	}
	n, err := m.eng.Reorganize()
	if err != nil {
		m.fail(err)
		return nil
	}
	m.screen, m.cursor = screenSources, 0
	if n == 0 || len(urls) == 0 {
		m.log(dim.Render("Nothing downloaded yet to reorganize."))
		return nil
	}
	m.log(dim.Render(fmt.Sprintf("Reorganizing %d tracks: each old file is replaced as its new one arrives.", n)))
	return m.download(urls)
}

// cycle returns the value step positions away from cur in list, wrapping around.
func cycle(list []string, cur string, step int) string {
	for i, v := range list {
		if v == cur {
			return list[(i+step+len(list))%len(list)]
		}
	}
	return list[0]
}

func (m model) settingsView(b *strings.Builder, w int) {
	cookies := m.opt.Cookies
	if cookies == "" {
		cookies = "none"
	}
	titles := "clean"
	if m.opt.RawTitles {
		titles = "as uploaded"
	}
	vals := []string{m.eng.Format(), fmt.Sprint(m.opt.Jobs), cookies, labels[m.opt.Artists], folderLabels[m.opt.Folders], titles, fmt.Sprint(max(1, m.opt.AlbumJobs))}
	notes := []string{
		"Saved in data.music", "1 to 8 tracks across sources", "Private playlists / Liked Music",
		"How to tag collaborators", "Folder layout for new files", "Clean removes video labels", "1 to 4 sources; shares track limit",
		"Move files without downloading", "Replace downloaded files", "", "Originals go to trash", "Formats, duplicates and undo",
	}
	note := notes[m.cursor]
	if m.cursor == chooseDestination {
		note = "Current: " + destinationLabel(m.eng.Root(), m.terminalWidth()-9)
	}
	if m.run.busy && m.cursor != libraryGuide {
		note = "Wait for downloads to finish"
	}
	available := m.terminalHeight() - m.footerHeight(m.help()) - strings.Count(b.String(), "\n")
	wide := w >= 48 && available >= 8
	title := hi.Render("Settings")
	start, end := 0, len(settings)
	if !wide {
		if m.cursor < moveFiles {
			title += dim.Render(" · Downloads")
			end = moveFiles
		} else {
			title += dim.Render(" · Files and folders")
			start = moveFiles
		}
	}
	b.WriteString(title + "\n" + dim.Render(ansi.Truncate(note, m.terminalWidth(), "…")) + "\n")
	if !wide {
		visible := min(end-start, max(1, available-2))
		start = min(max(start, m.cursor-visible/2), end-visible)
		end = start + visible
	}
	rows := make([]string, 0, end-start+2)
	for i := start; i < end; i++ {
		s := settings[i]
		if wide && i == 0 {
			rows = append(rows, dim.Render("  Downloads"))
		} else if wide && i == moveFiles {
			rows = append(rows, dim.Render("  Files and folders"))
		}
		if i < moveFiles {
			if wide {
				s = fmt.Sprintf("%-20s %s", s, hi.Render(vals[i]))
			} else {
				s += ": " + hi.Render(vals[i])
			}
		}
		rows = append(rows, pick(i == m.cursor, s))
	}
	if wide {
		selectedRow := m.cursor + 1
		if m.cursor >= moveFiles {
			selectedRow++
		}
		m.listPanel(b, w, rows, selectedRow, 1, 0)
	} else {
		b.WriteString(strings.Join(rows, "\n") + "\n")
	}
	m.footer(b, m.help())
}
