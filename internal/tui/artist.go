package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
	"ytmanager/internal/ytmusic"
)

type artistState struct {
	returnTo        screen
	returnCursor    int
	query           string
	searching       bool
	loading         bool
	inspecting      bool
	err             string
	saved           []string
	results         []ytmusic.ArtistResult
	artist          *ytdlp.ArtistListing
	tab             int // 0: all, 1: albums, 2: singles
	marked          map[string]bool
	listCursor      int
	installedAlbums map[string]bool
	installedSrcs   []engine.SourceView
}

type artistSearchMsg struct {
	query   string
	results []ytmusic.ArtistResult
	err     error
}

type artistLoadedMsg struct {
	query  string
	artist *ytdlp.ArtistListing
	err    error
}

type inspectLoadedMsg struct {
	eng     *engine.Engine
	from    screen
	url     string
	listing *ytdlp.Listing
	save    bool
	err     error
}

func (m *model) refreshArtistCatalog() {
	if m.eng == nil {
		return
	}
	albums, saved := m.eng.InstalledCatalog()
	m.artist.installedAlbums = albums
	m.artist.installedSrcs = m.eng.Snapshot()
	m.artist.saved = saved
}

func (m *model) openSavedArtists() tea.Cmd {
	m.enterArtist(artistState{})
	return nil
}

func (m *model) enterArtist(state artistState) {
	state.returnTo, state.returnCursor = m.screen, m.cursor
	if m.screen == screenArtist {
		state.returnTo, state.returnCursor = m.artist.returnTo, m.artist.returnCursor
	}
	state.marked = map[string]bool{}
	m.screen, m.cursor, m.artist = screenArtist, 0, state
	m.refreshArtistCatalog()
}

func (m *model) openArtistQuery(q string) tea.Cmd {
	q = trim(q)
	if q == "" {
		return m.openSavedArtists()
	}
	if ytdlp.IsArtistURL(q) {
		m.enterArtist(artistState{query: q, loading: true})
		cookies := m.opt.Cookies
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
			defer cancel()
			art, err := ytdlp.ListArtist(ctx, q, cookies)
			return artistLoadedMsg{query: q, artist: art, err: err}
		}
	}
	m.enterArtist(artistState{query: q, searching: true})
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15_000_000_000)
		defer cancel()
		res, err := ytmusic.SearchArtists(ctx, q)
		return artistSearchMsg{query: q, results: res, err: err}
	}
}

func (m model) artistReleases() []ytdlp.ArtistRelease {
	if m.artist.artist == nil {
		return nil
	}
	switch m.artist.tab {
	case 1:
		return m.artist.artist.Albums
	case 2:
		return m.artist.artist.Singles
	default:
		out := make([]ytdlp.ArtistRelease, 0, len(m.artist.artist.Albums)+len(m.artist.artist.Singles))
		out = append(out, m.artist.artist.Albums...)
		out = append(out, m.artist.artist.Singles...)
		return out
	}
}

func (m model) artistHighlightedCover() string {
	if m.artist.artist == nil {
		if len(m.artist.results) > 0 && m.cursor < len(m.artist.results) {
			return m.artist.results[m.cursor].Cover
		}
		return ""
	}
	rels := m.artistReleases()
	if len(rels) > 0 && m.cursor < len(rels) && rels[m.cursor].Cover != "" {
		return rels[m.cursor].Cover
	}
	return m.artist.artist.Cover
}

func (m model) artistPickedReleases() []ytdlp.ArtistRelease {
	rels := m.artistReleases()
	var picked []ytdlp.ArtistRelease
	for _, r := range rels {
		if m.artist.marked[r.URL] {
			picked = append(picked, r)
		}
	}
	if len(picked) > 0 {
		return picked
	}
	if m.artist.artist != nil {
		for _, r := range append(append([]ytdlp.ArtistRelease(nil), m.artist.artist.Albums...), m.artist.artist.Singles...) {
			if m.artist.marked[r.URL] {
				picked = append(picked, r)
			}
		}
	}
	if len(picked) > 0 {
		return picked
	}
	if len(rels) > 0 && m.cursor < len(rels) {
		return []ytdlp.ArtistRelease{rels[m.cursor]}
	}
	return nil
}

func releaseStatusBadge(srcs []engine.SourceView, albums map[string]bool, r ytdlp.ArtistRelease) string {
	relKey := releaseKey(r.URL, r.ID)
	relTitle := strings.ToLower(strings.TrimSpace(r.Title))
	for _, s := range srcs {
		srcKey := releaseKey(s.URL, s.ID)
		matched := s.URL == r.URL || (relKey != "" && srcKey == relKey)
		if !matched && relTitle != "" && strings.EqualFold(strings.TrimSpace(s.Title), relTitle) {
			matched = s.Creator == "" || r.Artist == "" || strings.EqualFold(strings.TrimSpace(s.Creator), strings.TrimSpace(r.Artist))
		}
		if !matched {
			continue
		}
		switch {
		case s.Total > 0 && s.Done > 0 && s.Done+s.Unavailable+s.Removed >= s.Total:
			return ok.Render(fmt.Sprintf("✓ %d/%d", s.Done, s.Total))
		case s.Done > 0:
			return hi.Render(fmt.Sprintf("◐ %d/%d", s.Done, s.Total))
		case s.Total > 0:
			return dim.Render(fmt.Sprintf("· %d/%d", s.Done, s.Total))
		default:
			return dim.Render("· saved")
		}
	}
	if relTitle != "" && albums[relTitle] {
		return ok.Render("✓ Saved")
	}
	return ""
}

func releaseKey(rawURL, id string) string {
	if u, err := url.Parse(rawURL); err == nil {
		if list := u.Query().Get("list"); list != "" {
			return list
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "browse" {
			return parts[1]
		}
	}
	return id
}

func (m model) artistKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "f", "/":
		return m, m.ask(promptArtist, "Artist name, link, or Enter for downloaded artists", "")
	case "esc", "backspace":
		if m.artist.artist != nil && len(m.artist.results) > 1 {
			m.artist.artist = nil
			m.artist.err = ""
			m.cursor = min(m.artist.listCursor, len(m.artist.results)-1)
			return m, m.requestCover(m.artistHighlightedCover())
		}
		m.screen, m.cursor = screenSources, m.artist.returnCursor
		if m.artist.returnTo == screenLibrary {
			m.screen = screenLibrary
		}
		return m, nil
	}

	if m.artist.searching || m.artist.loading {
		return m, nil
	}

	// Saved/downloaded artists picker when no search results are active
	if m.artist.artist == nil && len(m.artist.results) == 0 && len(m.artist.saved) > 0 {
		last := len(m.artist.saved) - 1
		switch k.String() {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(last, m.cursor+1)
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = last
		case "enter":
			return m, m.openArtistQuery(m.artist.saved[m.cursor])
		}
		return m, nil
	}

	// Artist search hit picker
	if m.artist.artist == nil {
		last := max(0, len(m.artist.results)-1)
		switch k.String() {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(last, m.cursor+1)
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = last
		case "enter":
			if len(m.artist.results) > 0 {
				hit := m.artist.results[m.cursor]
				m.artist.listCursor = m.cursor
				m.artist.loading = true
				m.artist.err = ""
				cookies := m.opt.Cookies
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
					defer cancel()
					art, err := ytdlp.ListArtist(ctx, hit.URL, cookies)
					return artistLoadedMsg{query: hit.Name, artist: art, err: err}
				}
			}
		case "c":
			if cov := m.artistHighlightedCover(); cov != "" {
				return m, func() tea.Msg { return coverOpenedMsg{m.collection, openCover(cov)} }
			}
		}
		return m, m.requestCover(m.artistHighlightedCover())
	}

	// Artist discography explorer
	rels := m.artistReleases()
	last := max(0, len(rels)-1)
	switch k.String() {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(last, m.cursor+1)
	case "pgup":
		m.cursor = max(0, m.cursor-max(1, m.terminalHeight()/3))
	case "pgdown":
		m.cursor = min(last, m.cursor+max(1, m.terminalHeight()/3))
	case "home":
		m.cursor = 0
	case "end":
		m.cursor = last
	case "tab", "right":
		m.artist.tab = (m.artist.tab + 1) % 3
		m.cursor = 0
	case "left":
		m.artist.tab = (m.artist.tab + 2) % 3
		m.cursor = 0
	case "1":
		m.artist.tab, m.cursor = 0, 0
	case "2":
		m.artist.tab, m.cursor = 1, 0
	case "3":
		m.artist.tab, m.cursor = 2, 0
	case " ":
		if len(rels) > 0 {
			u := rels[m.cursor].URL
			m.artist.marked[u] = !m.artist.marked[u]
		}
	case "a":
		if len(rels) > 0 {
			allMarked := true
			for _, r := range rels {
				if !m.artist.marked[r.URL] {
					allMarked = false
					break
				}
			}
			for _, r := range rels {
				m.artist.marked[r.URL] = !allMarked
			}
		}
	case "c":
		if cov := m.artistHighlightedCover(); cov != "" {
			return m, func() tea.Msg { return coverOpenedMsg{m.collection, openCover(cov)} }
		}
	case "v", "m":
		if m.artist.inspecting || len(rels) == 0 {
			break
		}
		saveOnly := k.String() == "m"
		target := rels[m.cursor]
		m.artist.inspecting = true
		eng := m.eng
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20_000_000_000)
			defer cancel()
			listing, err := eng.Inspect(ctx, target.URL)
			if err == nil && listing != nil {
				if listing.Cover == "" {
					listing.Cover = target.Cover
				}
				if listing.Year == "" {
					listing.Year = target.Year
				}
				if listing.Creator == "" {
					listing.Creator = target.Artist
				}
				if listing.AlbumArtist == "" {
					listing.AlbumArtist = target.Artist
				}
			}
			return inspectLoadedMsg{eng: eng, from: screenArtist, url: target.URL, listing: listing, save: saveOnly, err: err}
		}
	case "enter":
		picked := m.artistPickedReleases()
		if len(picked) == 0 {
			break
		}
		urls := make([]string, 0, len(picked))
		for _, r := range picked {
			urls = append(urls, r.URL)
			delete(m.artist.marked, r.URL)
		}
		m.screen, m.cursor = screenSources, 0
		return m, m.add(urls)
	}
	return m, m.requestCover(m.artistHighlightedCover())
}
