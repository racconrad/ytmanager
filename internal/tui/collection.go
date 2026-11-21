package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

type collectionState struct {
	id           string
	sourceCursor int
	preview      *ytdlp.Listing
	previewURL   string
	returnTo     screen
}

func (m model) activeCollection() engine.Collection {
	if m.collection.preview != nil {
		p := m.collection.preview
		tracks := make([]engine.CollectionTrack, 0, len(p.Entries))
		for _, e := range p.Entries {
			st := "preview"
			if e.Unavailable {
				st = "unavailable"
			}
			tracks = append(tracks, engine.CollectionTrack{
				ID: e.ID, Title: e.Title, Status: st, Duration: e.Duration,
			})
		}
		return engine.Collection{
			Title: p.Title, Creator: p.Creator, URL: m.collection.previewURL, Cover: p.Cover, Year: p.Year, Tracks: tracks,
		}
	}
	return m.eng.Collection(m.collection.id)
}

func (m model) collectionKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.activeCollection()
	last := max(0, len(c.Tracks)-1)
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "backspace":
		if m.collection.returnTo == screenArtist {
			m.screen, m.cursor = screenArtist, m.collection.sourceCursor
			m.collection = collectionState{}
			return m, m.requestCover(m.artistHighlightedCover())
		}
		m.screen, m.cursor = screenSources, max(0, min(len(m.eng.Snapshot())-1, m.collection.sourceCursor))
		m.collection = collectionState{}
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= m.collectionPageSize()
	case "pgdown":
		m.cursor += m.collectionPageSize()
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = last
	case "enter":
		target := c.URL
		if m.collection.previewURL != "" {
			target = m.collection.previewURL
		}
		if target != "" {
			m.collection = collectionState{}
			m.screen, m.cursor = screenSources, 0
			return m, m.add([]string{target})
		}
	case "m":
		if m.collection.preview != nil {
			id, err := m.eng.SaveSourceMetadata(m.collection.preview, m.collection.previewURL)
			if err != nil {
				m.fail(err)
				break
			}
			m.log(ok.Render("Saved metadata: " + m.collection.preview.Title))
			m.collection.id = id
			m.collection.preview = nil
			break
		}
		if c.URL != "" && !m.artist.inspecting {
			m.artist.inspecting = true
			eng := m.eng
			rawURL := c.URL
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
				defer cancel()
				listing, err := eng.Inspect(ctx, rawURL)
				return inspectLoadedMsg{eng: eng, from: screenCollection, url: rawURL, listing: listing, save: true, err: err}
			}
		}
	case "c":
		if c.Cover == "" {
			m.log("No cover saved. Press m to fetch metadata or s from Sources.")
			break
		}
		state := m.collection
		return m, func() tea.Msg { return coverOpenedMsg{state, openCover(c.Cover)} }
	case "ctrl+home":
		m.scrollLogs(len(m.logLines()))
	case "ctrl+end":
		m.logScroll = 0
	case "p":
		m.togglePause()
	case "x":
		if m.run.busy {
			m.run.cancel()
		}
	}
	if m.screen == screenCollection {
		m.cursor = max(0, min(last, m.cursor))
	}
	return m, nil
}

func (m model) collectionPageSize() int {
	return max(1, m.terminalHeight()-m.footerHeight(m.help())-7)
}

func trackStatus(status string) string {
	switch status {
	case "done":
		return "Saved"
	case "failed":
		return "Failed"
	case "unavailable":
		return "Unavailable"
	case "removed":
		return "Removed"
	case "missing":
		return "File missing"
	case "preview":
		return "Preview"
	default:
		return "Pending"
	}
}

func jobStatus(j *job) string {
	switch {
	case j.hasProgress && j.pct >= 1:
		return "Finishing"
	case j.hasProgress:
		return fmt.Sprintf("Downloading %.0f%%", math.Floor(j.pct*100))
	case j.stage != "":
		return j.stage
	default:
		return "Starting"
	}
}

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}
