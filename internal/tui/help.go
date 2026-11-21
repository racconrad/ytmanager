package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) help() string {
	if m.shortcutHelp {
		return "esc close · ↑↓ scroll"
	}
	switch m.screen {
	case screenDevices:
		help := "enter open · e edit path · ? keys"
		if m.lost != nil {
			help = "x stop waiting · " + help
		}
		return help
	case screenBrowse:
		return "u use folder · ? keys · esc back · enter open"
	case screenSettings:
		if m.cursor >= moveFiles {
			return "enter run · ? keys · ↑↓ move · esc back"
		}
		return "←→ change · ? keys · ↑↓ move · esc back"
	case screenLibrary:
		if m.library.removeSource != nil {
			return "enter remove source, keep files · esc cancel"
		}
		if m.library.help || m.library.report != nil || m.library.details != nil {
			return "↑↓ scroll · esc back"
		}
		if m.library.purge {
			return "enter empty trash · esc cancel"
		}
		if m.library.restore {
			return "enter restore · esc cancel"
		}
		if m.library.plan != nil {
			return "enter apply · esc cancel · i details"
		}
		if m.library.busy {
			return "x cancel"
		}
		if m.library.candidates != nil {
			action := "enter move to trash"
			if m.library.reset {
				action = "enter reset"
			}
			return action + " · esc cancel · i details · h shared"
		}
		if m.library.tools {
			return "↑↓ choose · ←→ change · enter preview · esc back"
		}
		if m.library.search {
			return "type to search · enter apply · esc cancel"
		}
		if m.library.scanning {
			if m.library.settingsAction == "" {
				return "g sources · x cancel scan"
			}
			return "x cancel scan"
		}
		return "g sources · ? keys · d trash · u restore · s scan folder · / search · b albums · O settings"
	case screenCollection:
		if m.collection.preview != nil {
			return "enter download · ? keys · m save metadata · esc back"
		}
		return "m refresh metadata · esc back · ? keys · c cover"
	case screenArtist:
		if m.artist.searching || m.artist.loading {
			return "esc back · ? keys"
		}
		if m.artist.artist == nil && len(m.artist.saved) > 0 && len(m.artist.results) == 0 {
			return "enter search artist · f new search · esc back · ? keys"
		}
		if m.artist.artist == nil {
			return "enter open artist · f search · esc back · ? keys"
		}
		return "v preview · enter download · esc back · ? keys · tab filter · space mark"
	case screenMissing:
		if m.eng == nil || len(m.eng.Missing()) == 0 {
			return "esc back · ? keys"
		}
		return "enter set alternative · r retry · esc back · ? keys"
	default:
		if m.run.busy && (m.run.verb == "repaired" || m.run.verb == "exported") {
			return "x cancel · q quit"
		}
		if m.run.busy {
			return "p pause/resume · x cancel · v tracks · ? keys · a add link"
		}
		return "a add link · l library · ? keys · f find artist · v tracks · enter resume · o settings"
	}
}

func (m model) shortcutList() (string, []string) {
	var title, actions string
	switch m.screen {
	case screenDevices:
		title, actions = "Device", "↑↓ choose device · enter open · e edit path · paste path · r refresh devices · esc return to open folder"
		if m.lost != nil {
			actions += " · x stop waiting"
		}
	case screenBrowse:
		title, actions = "Folder", "↑↓ choose folder · → open · ← parent folder · u use folder · n new folder · e edit path · d devices · esc back"
	case screenSettings:
		title, actions = "Settings", "↑↓ choose setting · ←→ change value · enter run selected action or close · esc back"
	case screenCollection:
		title, actions = "Track list", "↑↓ move · pgup/pgdn page · enter download · c open cover in browser · esc back"
		if m.collection.preview != nil {
			actions += " · m save metadata"
		} else {
			actions += " · m refresh metadata"
		}
	case screenArtist:
		title, actions = "Artist", "↑↓ move · enter open or download · f new search · c open cover in browser · esc back"
		if m.artist.artist != nil {
			actions += " · tab/1-3 filter releases · space mark · a mark visible releases · v preview tracks · m save metadata"
		}
	case screenMissing:
		title, actions = "Unavailable tracks", "↑↓ move · enter set alternative · paste alternative link · r retry · esc back"
	default:
		title, actions = "Source", "↑↓ move · space mark source · a add link · l open local library · f find artist · F find selected artist · v view tracks · enter resume downloads · s sync sources and download · d remove source, keep audio · D review source files · r repair library · e export playlists · n unavailable tracks · o settings · c change folder"
		if m.run.busy {
			actions = "p pause/resume · x cancel downloads · v view tracks · a add link · f find artist · F find selected artist · n unavailable tracks · o settings"
		}
	}
	actions += " · ctrl+c quit"
	if m.screen != screenArtist && m.screen != screenCollection {
		actions += " · pgup/pgdn scroll log · ctrl+end latest log"
	}
	return title + " shortcuts", strings.Split(actions, " · ")
}

func (m model) shortcutKey(k tea.KeyMsg) model {
	_, entries := m.shortcutList()
	switch k.String() {
	case "esc", "?":
		m.shortcutHelp = false
	case "up", "k":
		m.shortcutCursor--
	case "down", "j":
		m.shortcutCursor++
	case "pgup":
		m.shortcutCursor -= max(1, m.terminalHeight()-7)
	case "pgdown":
		m.shortcutCursor += max(1, m.terminalHeight()-7)
	case "home":
		m.shortcutCursor = 0
	case "end":
		m.shortcutCursor = len(entries) - 1
	}
	m.shortcutCursor = max(0, min(len(entries)-1, m.shortcutCursor))
	return m
}

func (m model) shortcutView(b *strings.Builder, w int) {
	title, entries := m.shortcutList()
	m.logs = nil
	b.WriteString(hi.Render(title) + "\n\n")
	for i, entry := range entries {
		entries[i] = pick(i == m.shortcutCursor, styleHelp(entry))
	}
	m.listPanel(b, w, entries, m.shortcutCursor, 1, 0)
	m.footer(b, m.help())
}
