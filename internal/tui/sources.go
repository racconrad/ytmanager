package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/device"
	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

var (
	tags    = map[string]string{"playlist": "PLAYLIST", "album": "ALBUM", "mix": "MIX", "track": "SONG"}
	formats = []string{"opus", "m4a", "mp3"}
)

func (m model) sourcesKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	srcs := m.eng.Snapshot()
	var cmd tea.Cmd
	switch k.String() {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = max(0, min(len(srcs)-1, m.cursor+1))
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = max(0, len(srcs)-1)
	case " ":
		if len(srcs) > 0 {
			id := srcs[m.cursor].ID
			m.marked[id] = !m.marked[id]
		}
	case "a":
		cmd = m.ask(promptURLs, "Paste YouTube, SoundCloud or Bandcamp links", "")
	case "f":
		cmd = m.ask(promptArtist, "Artist name, link, or Enter for downloaded artists", "")
	case "F":
		if len(srcs) > 0 && srcs[m.cursor].Creator != "" {
			cmd = m.openArtistQuery(srcs[m.cursor].Creator)
		} else {
			cmd = m.openSavedArtists()
		}
	case "v":
		if len(srcs) > 0 {
			m.collection = collectionState{id: srcs[m.cursor].ID, sourceCursor: m.cursor, returnTo: screenSources}
			m.screen, m.cursor = screenCollection, 0
			cmd = m.requestCover(srcs[m.cursor].Cover)
		}
	case "enter":
		if !m.run.busy && (len(srcs) > 0 || m.eng.HasPending()) {
			cmd = m.download(m.pickedURLs(srcs))
		}
	case "r":
		if !m.run.busy {
			cmd = m.start(nil, "repaired", m.eng.Repair)
		}
	case "e":
		if !m.run.busy {
			cmd = m.start(nil, "exported", m.eng.ExportM3U)
		}
	case "s":
		if !m.run.busy && len(srcs) > 0 {
			cmd = m.sync(m.pickedURLs(srcs))
		}
	case "p":
		m.togglePause()
	case "x":
		if m.run.busy {
			m.run.cancel() // paused workers wake up on cancel too
			m.log(dim.Render("Cancelling…"))
		}
	case "d":
		if m.library.scanning {
			m.log("Wait for the folder scan to stop.")
			break
		}
		if !m.run.busy && len(srcs) > 0 {
			m.library = libraryState{removeSource: &srcs[m.cursor]}
			m.screen, m.cursor = screenLibrary, 0
		}
	case "l":
		if m.run.busy {
			m.log(dim.Render("Wait for downloads to finish or cancel them before managing music."))
			break
		}
		cmd = m.resumeLibrary()
	case "D":
		if m.library.scanning {
			m.log("Wait for the folder scan to stop.")
			break
		}
		if !m.run.busy && len(srcs) > 0 {
			ids := m.eng.SourceTrackIDs(srcs[m.cursor].ID)
			cmd = m.openLibrary("")
			m.library.sourceTracks = ids
			m.library.albums = true
		}
	case "o", "O":
		m.openSettings()
	case "n":
		m.screen, m.cursor = screenMissing, 0
	case "c", "esc", "backspace":
		m.changeDestination()
	}
	return m, cmd
}

// pickedURLs returns the marked sources, or all of them when none is marked.
func (m model) pickedURLs(srcs []engine.SourceView) []string {
	var picked, all []string
	for _, s := range srcs {
		all = append(all, s.URL)
		if m.marked[s.ID] {
			picked = append(picked, s.URL)
		}
	}
	if len(picked) == 0 {
		return all
	}
	return picked
}

func (m model) sourcesView(b *strings.Builder, w int) {
	info := fmt.Sprintf(" · %s · %d jobs", m.eng.Format(), m.eng.Jobs())
	if m.opt.Cookies != "" {
		info += " · cookies " + m.opt.Cookies
	}
	b.WriteString(hi.Render("Sources") + "\n")
	dest := "Folder: " + destinationLabel(m.eng.Root(), max(1, w-8))
	if ansi.StringWidth(dest+info) <= w {
		dest += info
	}
	b.WriteString(dim.Render(dest) + "\n")
	if m.terminalHeight() >= 23 || (m.spaceKnown && m.space.Available < device.SpaceReserve) {
		b.WriteString(m.spaceView(w) + "\n")
	}
	if m.terminalHeight() >= 30 && !m.run.busy {
		h := m.eng.Health()
		if h.Done+h.Missing+h.Pending+h.Failed+h.Removed > 0 {
			b.WriteString(dim.Render(ansi.Truncate(fmt.Sprintf("%d saved · %d missing · %d pending · %d failed · %d removed", h.Done, h.Missing, h.Pending, h.Failed, h.Removed), max(1, w), "…")) + "\n")
		}
	}

	activity := m.sourceActivity(w)
	if m.terminalHeight() >= 18 && (activity != "" || m.prompt == promptNone) {
		queue := "Download queue"
		if activity == "" {
			queue += " · idle"
			if queued := m.eng.QueuedSources(); queued > 0 {
				queue = "Download queue · " + countLabel(queued, "source") + " waiting"
			}
		}
		b.WriteString(dim.Render(ansi.Truncate(queue, w, "…")) + "\n")
	}
	b.WriteString(activity)
	if m.terminalHeight() >= 18 {
		b.WriteString(dim.Render("Saved sources") + "\n")
	}
	rows := m.sourceRows(w)
	if len(rows) == 0 {
		message := "No sources yet. Paste a music link."
		if queued := m.eng.QueuedSources(); queued > 0 {
			message = "Press Enter to resume " + countLabel(queued, "source") + "."
		}
		rows = []string{dim.Render(message)}
	}
	m.listPanel(b, w, rows, m.cursor*2, 2, 0)
	m.footer(b, m.help())
}

func (m model) sourceRows(w int) []string {
	var rows []string
	selecting := anyMarked(m.marked)
	for i, s := range m.eng.Snapshot() {
		active := false
		for _, j := range m.run.jobs {
			if j.sourceID == s.ID {
				active = true
				break
			}
		}
		complete := s.Total > 0 && s.Done == s.Total
		mark := sourceStatus(active, complete, s.Done)
		if selecting {
			mark = dim.Render("○")
			if m.marked[s.ID] {
				mark = hi.Render("●")
			}
		}
		count := "Listed"
		if s.Total > 0 {
			count = fmt.Sprintf("%d/%d", s.Done, s.Total)
		}
		if complete {
			count = ok.Render(count)
		}
		if w >= 70 && s.Total > 0 {
			count = progressBar(s.Done, s.Total, 8, complete) + " " + count
		}
		if w >= 60 && s.Removed > 0 {
			count += dim.Render(fmt.Sprintf(" · %d removed", s.Removed))
		}
		if w >= 60 && s.Unavailable > 0 {
			count += dim.Render(fmt.Sprintf(" · %d unavail.", s.Unavailable))
		}
		if active {
			if w < 60 {
				count = hi.Render("Active")
			} else {
				count = hi.Render("Downloading") + " · " + count
			}
		}
		prefix := mark + " "
		if w >= 55 {
			prefix = fmt.Sprintf("%s %-9s ", mark, tags[s.Type])
		}
		tw := max(1, w-7-ansi.StringWidth(prefix)-ansi.StringWidth(count))
		title := ansi.Truncate(strings.Join(strings.Fields(s.Title), " "), tw, "…")
		rows = append(rows, pick(i == m.cursor, prefix+title+strings.Repeat(" ", max(0, tw-ansi.StringWidth(title)))+" "+count))
		origin := s.URL
		if s.Creator != "" {
			origin = "By " + s.Creator
			if p := ytdlp.Provider(s.URL); ytdlp.IsExternalProvider(p) {
				if label := ytdlp.ProviderLabel(p); label != "" {
					origin += " · " + label
				}
			}
		}
		rows = append(rows, dim.Render("    "+ansi.Truncate(strings.Join(strings.Fields(origin), " "), max(1, w-8), "…")))
	}
	return rows
}

func (m model) sourceActivity(w int) string {
	if !m.run.busy {
		if m.terminalHeight() >= 36 && time.Now().Before(m.cat.moodUntil) {
			return m.catView(w) + "\n"
		}
		return ""
	}
	if m.terminalHeight() < 16 && m.prompt != promptNone {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.readingActivity(w))
	switch {
	case m.run.total > 0:
		state := "Downloading"
		if m.run.verb == "moved" {
			state = "Moving files"
		}
		if m.eng.Paused() {
			state = "Paused"
		}
		processed := m.run.done + m.run.failed + m.run.unavailable
		line := fmt.Sprintf("  %s · %d/%d processed", state, processed, m.run.total)
		if w >= 72 {
			line += "  " + m.bar.ViewAs(m.run.progress())
		}
		b.WriteString(ansi.Truncate(line, w, "…") + "\n")
	case m.run.readingURL == "" && m.run.verb == "downloaded":
		b.WriteString("  Preparing downloads…\n")
	case m.run.readingURL == "" && m.run.verb == "moved":
		b.WriteString("  Planning file moves…\n")
	}
	b.WriteString(m.activeAlbums(w))
	if m.terminalHeight() < 24 {
		return b.String()
	}
	limit := 1
	if m.terminalHeight() >= 32 {
		limit = 3
	}
	for i, id := range m.run.order {
		if i >= limit {
			b.WriteString(dim.Render(fmt.Sprintf("  %d more active tracks", len(m.run.order)-limit)) + "\n")
			break
		}
		if j := m.run.jobs[id]; j != nil {
			title := j.title
			if j.sourceTitle != "" {
				title = j.sourceTitle + ": " + title
			}
			status := jobStatus(j)
			if j.hasProgress && w >= 70 && j.pct < 1 {
				status += " " + m.bar.ViewAs(j.pct)
			}
			b.WriteString(fmt.Sprintf("  %s  %s\n", status, cut(title, max(1, w-ansi.StringWidth(status)-6))))
		}
	}
	return b.String()
}

// activeAlbums stays visible even when the source list is scrolled away.
func (m model) activeAlbums(w int) string {
	var names []string
	seen := map[string]bool{}
	for _, id := range m.run.order {
		j := m.run.jobs[id]
		if j != nil && j.sourceTitle != "" && !seen[j.sourceID] {
			seen[j.sourceID] = true
			names = append(names, j.sourceTitle)
		}
	}
	if len(names) == 0 {
		return ""
	}
	label := "Downloading"
	if m.eng != nil && m.eng.Paused() {
		label = "Paused"
	}
	return "  " + hi.Render(ansi.Truncate(label+": "+strings.Join(names, " · "), max(1, w-4), "…")) + "\n"
}

// sourceStatus gives each source a glyph that reads without color: downloading, complete, partial or not started.
func sourceStatus(active, complete bool, done int) string {
	switch {
	case active:
		return hi.Render("↓")
	case complete:
		return ok.Render("✓")
	case done > 0:
		return dim.Render("◐")
	}
	return dim.Render("·")
}

func progressBar(done, total, width int, complete bool) string {
	filled := min(width, done*width/max(1, total))
	bar := strings.Repeat("█", filled)
	if complete {
		bar = ok.Render(bar)
	} else {
		bar = hi.Render(bar)
	}
	return bar + dim.Render(strings.Repeat("░", width-filled))
}
