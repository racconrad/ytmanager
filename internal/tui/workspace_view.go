package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/buildinfo"
	"ytmanager/internal/device"
	"ytmanager/internal/engine"
	"ytmanager/internal/ytdlp"
)

func (m *model) workspaceView(b *strings.Builder) {
	m.syncWorkspaceFromState()
	W := m.terminalWidth()
	H := m.terminalHeight()
	help := m.workspaceHelp()
	footH := m.footerHeight(help)
	wide := W >= 72 && H >= 16

	topRight := m.workspaceStatusRight()
	brand := logo.Render("ytmanager")
	topRow := 2
	headerLines := 1

	if wide && W >= 108 {
		// Single sleek header combining brand, destination, top navigation tabs, and live status.
		tabs := m.renderTopTabs(W - ansi.StringWidth(brand) - ansi.StringWidth(topRight) - 24)
		destW := max(0, W-ansi.StringWidth(brand)-ansi.StringWidth(tabs)-ansi.StringWidth(topRight)-6)
		left := brand
		if destW >= 8 {
			left += " " + dim.Render(destinationLabel(m.eng.Root(), min(22, destW)))
		}
		midGap := max(2, (W-ansi.StringWidth(left)-ansi.StringWidth(tabs)-ansi.StringWidth(topRight))/2)
		line := left + strings.Repeat(" ", midGap) + tabs
		rightGap := max(1, W-ansi.StringWidth(line)-ansi.StringWidth(topRight))
		if topRight != "" {
			line += strings.Repeat(" ", rightGap) + topRight
		}
		b.WriteString(ansi.Truncate(line, W, "…") + "\n")
	} else {
		// Top identity & live status line.
		topLeft := brand
		destW := W - ansi.StringWidth(brand) - ansi.StringWidth(topRight) - 2
		if topRight == "" {
			destW = max(8, W-ansi.StringWidth(brand)-1)
		}
		if destW >= 6 {
			topLeft += " " + dim.Render(destinationLabel(m.eng.Root(), destW))
		}
		gap := max(1, W-ansi.StringWidth(topLeft)-ansi.StringWidth(topRight))
		topLine := topLeft
		if topRight != "" {
			topLine = topLeft + strings.Repeat(" ", gap) + topRight
		}
		b.WriteString(ansi.Truncate(topLine, W, "…") + "\n")
		if wide {
			// Dedicated horizontal Top Navigation Bar for medium-width terminals (72..107 cols).
			b.WriteString(" " + ansi.Truncate(m.renderTopTabs(W-2), W-1, "…") + "\n")
			headerLines++
			topRow++
		}
	}

	bodyH := max(5, H-footH-headerLines)

	if !wide {
		m.renderCompactWorkspace(b, W, bodyH)
		m.workspaceFooter(b, help)
		return
	}

	// Clean 2-column canvas: spacious List on left (~62%), Inspector & Actions on right (~38%).
	rightW := min(44, max(30, W*38/100))
	leftW := max(26, W-rightW-1)
	innerH := max(3, bodyH-2) // rounded border top + bottom = 2

	leftPane := m.renderMainPane(leftW, innerH)
	rightPane := m.renderSidePanel(rightW, innerH, leftW+2, topRow)

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane) + "\n")
	m.workspaceFooter(b, help)
}

func (m model) workspaceStatusRight() string {
	sp := m.spinnerFrame()
	switch {
	case m.library.busy:
		return hi.Render(sp + " Applying…")
	case m.library.scanning:
		done, total := m.eng.ScanProgress()
		if total > 0 {
			return hi.Render(fmt.Sprintf("%s Scanning %d/%d", sp, done, total))
		}
		return hi.Render(sp + " Scanning…")
	case m.artist.searching || m.artist.loading || m.artist.inspecting:
		return hi.Render(sp + " Loading…")
	case m.library.scanNotice != "":
		return bad.Render(m.library.scanNotice)
	case m.spaceKnown:
		return dim.Render(engine.Human(m.space.Available) + " free")
	default:
		return ""
	}
}

func (m model) renderTopTabs(maxW int) string {
	var parts []string
	for i, name := range sectionNames {
		active := i == int(m.ws.section.tab())
		switch {
		case active && m.ws.focus == paneMenu:
			parts = append(parts, logo.Render("› "+name))
		case active:
			parts = append(parts, tabOn.Render(name))
		default:
			parts = append(parts, tabOff.Render(name))
		}
	}
	return ansi.Truncate(strings.Join(parts, " "), max(10, maxW), "…")
}

func (m *model) workspaceFooter(b *strings.Builder, help string) {
	m.writeActivityFooter(b)
	if m.prompt != promptNone {
		b.WriteString(ansi.Truncate(m.input.View(), m.terminalWidth(), "…") + "\n")
	}
	b.WriteString(strings.Join(m.helpLines(help), "\n"))
}

func (m model) workspaceHelp() string {
	if m.ws.searching {
		return "type to filter · enter apply · esc cancel"
	}
	switch m.ws.focus {
	case paneMenu:
		if m.terminalWidth() >= 72 && m.terminalHeight() >= 16 {
			return "←→ switch tab · ↓/enter list · ? keys · q quit"
		}
		return "↑↓ section · ? keys · →/enter open · q quit"
	case paneSide:
		return "↑↓ action · ? keys · enter run · ←/esc list · q quit"
	default:
		if m.ws.section == secDetails {
			if len(m.ws.issues) > 0 {
				return "4 review · enter review · 1 albums · 3 sources · c folder"
			}
			return "1 albums · 3 sources · 4 review · ↑↓ details · c folder · q quit"
		}
		if m.ws.section == secAlbums && m.ws.albumKey != "" || m.ws.section == secSources && m.ws.sourceID != "" {
			help := "space mark · ctrl+a all · d delete"
			if alb, ok := m.activeAlbum(); m.ws.section == secSources || ok && alb.HasSource {
				help += " · - from list"
			}
			return help + " · ↑↓ move · / filter · ? keys"
		}
		switch m.ws.section {
		case secAlbums, secFolders:
			return "space mark · ctrl+a all · ↑↓ move · ? keys · enter open · → actions"
		case secSources:
			return "space mark · ctrl+a all · a add link · ? keys · enter open · → actions"
		case secIssues:
			return m.problemHelp()
		}
		if m.terminalWidth() < 96 {
			return "↑↓ move · ? keys · enter open · → actions · ← menu · q quit"
		}
		return "↑↓ move · enter open · → actions · [/] tabs · space mark · / filter · ? keys · q quit"
	}
}

func paneBorder(focused bool, width, height int) lipgloss.Style {
	color := gray
	if focused {
		color = accent
	}
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Width(max(6, width-2)).
		Height(height)
	if width >= 36 {
		st = st.Padding(0, 1)
	}
	return st
}

func paneInnerWidth(w int) int {
	pad := 0
	if w >= 36 {
		pad = 2
	}
	return max(6, w-2-pad)
}

func (m model) renderCompactMenu(w, innerH int) string {
	innerW := paneInnerWidth(w)
	lines := []string{hi.Render(ansi.Truncate("Menu", innerW, "…"))}
	if innerH >= len(sectionNames)+2 {
		lines = append(lines, "")
	}
	counts := [...]string{
		"",
		fmt.Sprintf("%d", len(m.ws.albums)),
		fmt.Sprintf("%d", len(m.ws.folders)),
		fmt.Sprintf("%d", len(m.ws.sources)),
		"",
		"",
	}
	available := max(1, innerH-len(lines))
	start := max(0, m.ws.menuCursor-available+1)
	for i := start; i < min(len(sectionNames), start+available); i++ {
		name := sectionNames[i]
		active := i == int(m.ws.section.tab())
		prefix := "  "
		if active {
			prefix = "› "
		}
		cnt := counts[i]
		label := name
		if cnt != "" {
			space := max(1, innerW-len(prefix)-len(name)-len(cnt))
			label = name + strings.Repeat(" ", space) + cnt
		}
		label = ansi.Truncate(prefix+label, innerW, "…")
		switch {
		case active:
			lines = append(lines, hi.Render(label))
		default:
			lines = append(lines, dim.Render(label))
		}
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	return paneBorder(m.ws.focus == paneMenu, w, innerH).Render(strings.Join(lines, "\n"))
}

func (m *model) renderMainPane(w, innerH int) string {
	innerW := paneInnerWidth(w)
	head, sub, rows, group, cur := m.workspaceCenterContent(innerW)
	var out []string
	if m.terminalHeight() <= 13 && m.ws.section != secDetails && !m.ws.searching && sub != "" {
		status := " · " + sub
		head = ansi.Truncate(head, innerW-min(innerW/2, ansi.StringWidth(status)), "…")
		out = append(out, hi.Render(head)+dim.Render(ansi.Truncate(status, innerW-ansi.StringWidth(head), "…")))
	} else if !m.ws.searching && sub != "" && innerW >= 36 && ansi.StringWidth(head)+ansi.StringWidth(sub)+2 <= innerW {
		gap := max(2, innerW-ansi.StringWidth(head)-ansi.StringWidth(sub))
		out = append(out, hi.Render(head)+strings.Repeat(" ", gap)+dim.Render(sub))
		if innerH >= 12 {
			out = append(out, "")
		}
	} else {
		out = append(out, hi.Render(ansi.Truncate(head, innerW, "…")))
		if m.ws.searching {
			out = append(out, ansi.Truncate(m.input.View(), innerW, "…"))
		} else if sub != "" {
			out = append(out, dim.Render(ansi.Truncate(sub, innerW, "…")))
		}
	}
	listAvail := max(1, innerH-len(out))
	visible := max(1, listAvail/group)
	total := (len(rows) + group - 1) / group
	if total > visible {
		ind := 0
		if listAvail >= group+1 {
			ind = 1
		}
		visible = max(1, (listAvail-ind)/group)
		start := min(max(0, cur/group-visible/2), max(0, total-visible))
		end := min(total, start+visible)
		slice := append([]string{}, rows[start*group:min(len(rows), end*group)]...)
		if ind > 0 {
			slice = append(slice, dim.Render(fmt.Sprintf("%d-%d of %d", start+1, end, total)))
		}
		rows = slice
	}
	for _, r := range rows {
		if len(out) >= innerH {
			break
		}
		out = append(out, ansi.Truncate(r, innerW, "…"))
	}
	for len(out) < innerH {
		out = append(out, "")
	}
	return paneBorder(m.ws.focus == paneList, w, innerH).Render(strings.Join(out, "\n"))
}

func (m *model) workspaceCenterContent(innerW int) (head, sub string, rows []string, group, cur int) {
	group = 1
	switch m.ws.section {
	case secDetails:
		rows := m.destinationDetailRows(innerW)
		for i, row := range rows {
			rows[i] = pick(i == m.ws.listCursor, row)
		}
		return "Destination details", m.destinationDetailStatus(), rows, group, m.ws.listCursor
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			head = "Albums › " + oneLine(alb.Title)
			sub = alb.Artist
			if sub != "" {
				sub += " · "
			}
			sub += alb.Summary()
			tracks := m.filteredAlbumTracks(alb)
			marked := workspaceMarkCount(tracks, func(tr engine.UnifiedTrack) bool { return m.albumTrackMarked(tr.ID) })
			if marked > 0 {
				sub = fmt.Sprintf("%d marked · ", marked) + sub
			}
			for i, tr := range tracks {
				mark := ""
				if marked > 0 {
					mark = "[ ] "
					if m.albumTrackMarked(tr.ID) {
						mark = "[x] "
					}
				}
				badge := unifiedTrackBadge(tr)
				if j := m.run.jobs[tr.ID]; j != nil {
					badge = hi.Render(jobStatus(j))
				}
				line1 := fmt.Sprintf("%s%02d %s · %s", mark, i+1, oneLine(tr.Title), badge)
				line2 := dim.Render("    Not on disk")
				if tr.Error != "" && (tr.Status == "failed" || tr.Status == "missing" || tr.Status == "unavailable") {
					line2 = bad.Render("    " + oneLine(tr.Error))
				} else if tr.Path != "" {
					line2 = dim.Render("    " + oneLine(tr.Path))
				}
				rows = append(rows, pick(i == m.ws.listCursor, line1), line2)
			}
			if len(rows) == 0 {
				rows = []string{dim.Render("No tracks in this album.")}
				return head, sub, rows, 1, 0
			}
			return head, sub, rows, 2, m.ws.listCursor * 2
		}
		albs := m.filteredAlbums()
		head = "Albums"
		if m.ws.query != "" {
			head += " · " + m.ws.query
		}
		sub = countLabel(len(albs), "album")
		if marked := workspaceMarkCount(albs, func(a engine.UnifiedAlbum) bool { return m.ws.markedAlbums[a.Key] }); marked > 0 {
			sub = selectionCount(marked, len(albs))
		}
		if m.library.scanning {
			sub += " · " + m.spinnerFrame() + " scanning…"
		}
		selecting := anyMarked(m.ws.markedAlbums)
		for i, alb := range albs {
			mark := ""
			if selecting {
				mark = "[ ] "
				if m.ws.markedAlbums[alb.Key] {
					mark = "[x] "
				}
			}
			badge := ok.Render("●")
			if alb.Failed > 0 || alb.Missing > 0 {
				badge = bad.Render("!")
			} else if alb.Pending > 0 || alb.ExtraDisk > 0 {
				badge = warn.Render("◌")
			} else if !alb.HasSource {
				badge = dim.Render("○")
			}
			line1 := fmt.Sprintf("%s%s %s", mark, badge, oneLine(alb.Title))
			detail := alb.Summary()
			if alb.Artist != "" {
				detail = oneLine(alb.Artist) + " · " + detail
			}
			rows = append(rows, pick(i == m.ws.listCursor, line1), dim.Render("    "+detail))
		}
		if len(rows) == 0 {
			msg := "No albums yet."
			if m.library.scanning {
				msg = m.spinnerFrame() + " Scanning device folders…"
			} else if !m.library.complete && m.library.scanNotice != "" {
				msg = m.library.scanNotice
			}
			rows = []string{dim.Render(msg)}
			return head, sub, rows, 1, 0
		}
		return head, sub, rows, 2, m.ws.listCursor * 2

	case secFolders:
		if fld, hasFld := m.activeFolder(); hasFld {
			head = "Folders › " + fld.Dir
			files := m.filteredFolderFiles(fld)
			sub = fmt.Sprintf("%s · %s", countLabel(len(files), "file"), engine.Human(uint64(fld.TotalBytes)))
			if marked := workspaceMarkCount(files, func(f engine.LibraryTrack) bool { return m.ws.markedFiles[f.Path] }); marked > 0 {
				sub = selectionCount(marked, len(files))
			}
			selecting := anyMarked(m.ws.markedFiles)
			for i, f := range files {
				mark := ""
				if selecting {
					mark = "[ ] "
					if m.ws.markedFiles[f.Path] {
						mark = "[x] "
					}
				}
				status := ok.Render("synced")
				switch {
				case f.Status == "not scanned":
					status = dim.Render("checking")
				case f.Missing:
					status = bad.Render("missing")
				case f.Invalid || f.FormatMismatch || f.Status == "unreadable":
					status = bad.Render("corrupt")
				case f.Duplicate:
					status = bad.Render("duplicate")
				case f.Orphan:
					status = warn.Render("unlisted")
				}
				line1 := fmt.Sprintf("%s%s · %s", mark, filepath.Base(f.Path), status)
				info := f.Title
				if f.Codec != "" {
					info += " · " + f.Codec
				}
				if f.Sources != "" {
					info += " · " + f.Sources
				}
				rows = append(rows, pick(i == m.ws.listCursor, line1), dim.Render("    "+oneLine(info)))
			}
			if len(rows) == 0 {
				rows = []string{dim.Render("Folder is empty.")}
				return head, sub, rows, 1, 0
			}
			return head, sub, rows, 2, m.ws.listCursor * 2
		}
		flds := m.filteredFolders()
		head = "Folders on device"
		sub = countLabel(len(flds), "folder")
		if marked := workspaceMarkCount(flds, func(f engine.PhysicalFolder) bool { return m.ws.markedFolders[f.Dir] }); marked > 0 {
			sub = selectionCount(marked, len(flds))
		}
		selecting := anyMarked(m.ws.markedFolders)
		for i, f := range flds {
			mark := ""
			if selecting {
				mark = "[ ] "
				if m.ws.markedFolders[f.Dir] {
					mark = "[x] "
				}
			}
			tag := ok.Render("●")
			if f.Issues > 0 {
				tag = bad.Render("!")
			} else if f.Orphans > 0 || f.HasRelinkReady {
				tag = warn.Render("◌")
			}
			name := f.Dir
			if name == "." {
				name = "(root folder)"
			}
			line1 := fmt.Sprintf("%s%s %s", mark, tag, name)
			detail := fmt.Sprintf("%s · %s", countLabel(len(f.Files), "file"), engine.Human(uint64(f.TotalBytes)))
			if f.Orphans > 0 {
				detail += fmt.Sprintf(" · %d unlisted", f.Orphans)
			}
			if f.Issues > 0 {
				detail += fmt.Sprintf(" · %d issues", f.Issues)
			}
			rows = append(rows, pick(i == m.ws.listCursor, line1), dim.Render("    "+detail))
		}
		if len(rows) == 0 {
			msg := "No audio folders scanned yet."
			if m.library.scanning {
				msg = m.spinnerFrame() + " Scanning device folders…"
			} else if !m.library.complete && m.library.scanNotice != "" {
				msg = m.library.scanNotice
			}
			rows = []string{dim.Render(msg)}
			return head, sub, rows, 1, 0
		}
		return head, sub, rows, 2, m.ws.listCursor * 2

	case secSources:
		if m.ws.sourceID != "" {
			c := m.ws.sourceDetail
			head = "Sources › " + oneLine(c.Title)
			sub = fmt.Sprintf("%d/%d synced", c.Synced, len(c.Tracks))
			if c.Failed > 0 {
				sub += fmt.Sprintf(" · %d failed", c.Failed)
			}
			if c.Missing > 0 {
				sub += fmt.Sprintf(" · %d missing", c.Missing)
			}
			tracks := m.filteredSourceTracks()
			marked := workspaceMarkCount(tracks, func(tr engine.CollectionTrack) bool { return m.sourceTrackMarked(tr.ID) })
			if marked > 0 {
				sub = fmt.Sprintf("%d marked · ", marked) + sub
			}
			for i, tr := range tracks {
				mark := ""
				if marked > 0 {
					mark = "[ ] "
					if m.sourceTrackMarked(tr.ID) {
						mark = "[x] "
					}
				}
				j := m.run.jobs[tr.ID]
				badge := collectionSyncBadge(tr, trackStatus(tr.Status), j != nil)
				line1 := fmt.Sprintf("%s%02d %s · %s", mark, i+1, oneLine(tr.Title), badge)
				line2 := dim.Render("    Not downloaded yet")
				if j != nil {
					line2 = hi.Render("    " + jobStatus(j))
				} else if tr.Error != "" && (tr.Status == "failed" || tr.Status == "unavailable") {
					line2 = bad.Render("    " + oneLine(tr.Error))
				} else if tr.Path != "" {
					line2 = dim.Render("    " + oneLine(tr.Path))
				}
				rows = append(rows, pick(i == m.ws.listCursor, line1), line2)
			}
			if len(rows) == 0 {
				rows = []string{dim.Render("No tracks in source.")}
				return head, sub, rows, 1, 0
			}
			return head, sub, rows, 2, m.ws.listCursor * 2
		}
		srcs := m.filteredSources()
		head = "Sources"
		sub = countLabel(len(srcs), "source")
		if marked := workspaceMarkCount(srcs, func(s engine.SourceView) bool { return m.marked[s.ID] }); marked > 0 {
			sub = selectionCount(marked, len(srcs))
		}
		selecting := anyMarked(m.marked)
		for i, s := range srcs {
			mark := sourceStatus(false, s.Total > 0 && s.Done == s.Total && s.Failed == 0 && s.Missing == 0, s.Failed > 0 || s.Missing > 0, s.Done)
			if selecting {
				mark = "○"
				if m.marked[s.ID] {
					mark = "●"
				}
			}
			line1 := fmt.Sprintf("%s %s · %d/%d", mark, oneLine(s.Title), s.Done, s.Total)
			if s.Failed > 0 {
				line1 += bad.Render(fmt.Sprintf(" · %d failed", s.Failed))
			}
			if s.Missing > 0 {
				line1 += bad.Render(fmt.Sprintf(" · %d missing", s.Missing))
			}
			origin := s.URL
			if s.Creator != "" {
				origin = "By " + s.Creator
			}
			rows = append(rows, pick(i == m.ws.listCursor, line1), dim.Render("    "+oneLine(origin)))
		}
		if len(rows) == 0 {
			rows = []string{dim.Render("No sources yet. Paste a link or open Actions.")}
			return head, sub, rows, 1, 0
		}
		return head, sub, rows, 2, m.ws.listCursor * 2

	case secIssues:
		iss := m.filteredIssues()
		head = "Review problems"
		sub = countLabel(len(iss), "issue")
		if marked := workspaceMarkCount(iss, func(it engine.IssueItem) bool { return m.ws.markedIssues[it.TrackID+":"+it.Path] }); marked > 0 {
			sub = selectionCount(marked, len(iss))
		}
		if m.library.scanning && m.terminalHeight() > 13 {
			sub += " · " + m.spinnerFrame() + " scanning…"
		}
		selecting := anyMarked(m.ws.markedIssues)
		for i, it := range iss {
			mark := ""
			if selecting {
				mark = "[ ] "
				if m.ws.markedIssues[it.TrackID+":"+it.Path] {
					mark = "[x] "
				}
			}
			badgeStyle := bad
			if it.Kind == "unlisted" || it.Kind == "relink_ready" {
				badgeStyle = warn
			}
			line1 := fmt.Sprintf("%s%s · %s", mark, badgeStyle.Render(it.Badge), oneLine(it.Title))
			detail := it.Error
			if detail == "" {
				detail = it.Subtitle
			}
			rows = append(rows, pick(i == m.ws.listCursor, line1), dim.Render("    "+oneLine(detail)))
		}
		if len(rows) == 0 {
			rows = []string{ok.Render("✓ Everything matches cleanly across sources and disk.")}
			return head, sub, rows, 1, 0
		}
		return head, sub, rows, 2, m.ws.listCursor * 2

	case secArtists:
		head = "Artists"
		if m.artist.artist != nil {
			head = oneLine(m.artist.artist.Name)
			sub = m.discographyTabs(innerW)
			rels := m.artistReleases()
			for i, r := range rels {
				mark := "○ "
				if m.artist.marked[r.URL] {
					mark = "● "
				}
				badge := releaseStatusBadge(m.artist.installedSrcs, m.artist.installedAlbums, r)
				if badge == "" {
					badge = dim.Render("Not saved")
				}
				rows = append(rows, pick(i == m.ws.listCursor, mark+oneLine(r.Title)), "    "+badge+dim.Render(" · "+r.Type+" "+r.Year))
			}
			if len(rows) == 0 {
				rows = []string{dim.Render("No releases found.")}
				return head, sub, rows, 1, 0
			}
			return head, sub, rows, 2, m.ws.listCursor * 2
		}
		if len(m.artist.results) > 0 {
			sub = "Results for " + m.artist.query
			for i, r := range m.artist.results {
				rows = append(rows, pick(i == m.ws.listCursor, oneLine(r.Name)), dim.Render("    "+oneLine(r.Subscribers)))
			}
			return head, sub, rows, 2, m.ws.listCursor * 2
		}
		sub = "Saved artists"
		for i, name := range m.artist.saved {
			rows = append(rows, pick(i == m.ws.listCursor, oneLine(name)))
		}
		if len(rows) == 0 {
			rows = []string{dim.Render("Use Search artist in Actions or press f.")}
		}
		return head, sub, rows, 1, m.ws.listCursor

	case secSettings:
		head = "Settings"
		sub = "Version: " + buildinfo.Version
		cookies := m.opt.Cookies
		if cookies == "" {
			cookies = "none"
		}
		titles := "clean"
		if m.opt.RawTitles {
			titles = "as uploaded"
		}
		vals := []string{m.eng.Format(), fmt.Sprint(m.opt.Jobs), cookies, labels[m.opt.Artists], folderLabels[m.opt.Folders], titles, fmt.Sprint(max(1, m.opt.AlbumJobs))}
		for i, s := range settings {
			line := s
			if i < moveFiles {
				if innerW < 34 {
					line = s + ": " + hi.Render(vals[i])
				} else {
					line = fmt.Sprintf("%-20s %s", s, hi.Render(vals[i]))
				}
			}
			rows = append(rows, pick(i == m.ws.listCursor, line))
		}
		return head, sub, rows, 1, m.ws.listCursor
	}
	return head, sub, rows, 1, 0
}

func (m *model) renderSidePanel(w, innerH, colStart, rowStart int) string {
	innerW := paneInnerWidth(w)
	details := m.workspaceDetailLines(innerW)
	acts := m.workspaceActions()

	var lines []string
	coverKey := m.workspaceCoverKey()
	img := m.coverImage(coverKey)
	artH := min(10, max(0, innerH-len(acts)-len(details)-2))
	if img != nil && artH >= 3 {
		artW := min(innerW, artH*2)
		if m.overlay != nil && m.overlay.enabled {
			for range artH {
				lines = append(lines, strings.Repeat(" ", artW))
			}
			m.nextCover = &coverPlacement{key: coverKey, img: img, row: rowStart, col: colStart + 1, cols: artW, rows: artH}
		} else {
			lines = append(lines, m.renderedCover(coverKey, img, artW, artH)...)
		}
	}

	maxDetail := max(2, innerH-len(acts)-2-len(lines))
	for i, d := range details {
		if i >= maxDetail {
			break
		}
		lines = append(lines, ansi.Truncate(d, innerW, "…"))
	}
	lines = append(lines, dim.Render(strings.Repeat("─", innerW)))
	lines = append(lines, hi.Render("Actions"))
	availActs := max(1, innerH-len(lines))
	actStart := 0
	if len(acts) > availActs {
		actStart = min(max(0, m.ws.sideCursor-availActs/2), len(acts)-availActs)
	}
	for i := actStart; i < len(acts) && len(lines) < innerH; i++ {
		selected := m.ws.focus == paneSide && i == m.ws.sideCursor
		lines = append(lines, ansi.Truncate(pick(selected, acts[i].label), innerW, "…"))
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	return paneBorder(m.ws.focus == paneSide, w, innerH).Render(strings.Join(lines, "\n"))
}

func (m model) workspaceDetailLines(innerW int) []string {
	var out []string
	wrap := func(prefix, text string, style lipgloss.Style) {
		if text == "" {
			return
		}
		for i, line := range strings.Split(ansi.Wrap(oneLine(text), max(8, innerW-len(prefix)), ""), "\n") {
			if i == 0 {
				out = append(out, style.Render(prefix+line))
			} else {
				out = append(out, style.Render(strings.Repeat(" ", len(prefix))+line))
			}
		}
	}

	switch m.ws.section {
	case secDetails:
		return m.destinationSourceNames()
	case secAlbums:
		if alb, ok := m.activeAlbum(); ok {
			tracks := m.filteredAlbumTracks(alb)
			if m.ws.listCursor < len(tracks) {
				tr := tracks[m.ws.listCursor]
				out = append(out, hi.Render(oneLine(tr.Title)), unifiedTrackBadge(tr))
				wrap("File: ", tr.Path, dim)
				if tr.LibraryRow != nil && tr.LibraryRow.Size > 0 {
					out = append(out, dim.Render(fmt.Sprintf("%s · %s · ~%dk", tr.LibraryRow.Codec, engine.Human(uint64(tr.LibraryRow.Size)), tr.LibraryRow.Bitrate/1000)))
				}
				wrap("Error: ", tr.Error, bad)
				return out
			}
		}
		albs := m.filteredAlbums()
		if m.ws.listCursor < len(albs) {
			alb := albs[m.ws.listCursor]
			out = append(out, hi.Render("Selected album"), oneLine(alb.Title))
			if alb.Artist != "" {
				out = append(out, dim.Render(oneLine(alb.Artist)))
			}
			out = append(out, dim.Render(alb.Summary()))
			if len(alb.Folders) > 0 {
				wrap("Folder: ", strings.Join(alb.Folders, ", "), dim)
			}
		}
	case secFolders:
		if fld, hasFld := m.activeFolder(); hasFld {
			files := m.filteredFolderFiles(fld)
			if m.ws.listCursor < len(files) {
				f := files[m.ws.listCursor]
				out = append(out, hi.Render(oneLine(f.Title)), dim.Render(oneLine(f.Artist)))
				wrap("Path: ", f.Path, dim)
				if f.Codec != "" {
					out = append(out, dim.Render(fmt.Sprintf("%s · %s · ~%dk", f.Codec, engine.Human(uint64(f.Size)), f.Bitrate/1000)))
				}
				if f.Sources != "" {
					wrap("Sources: ", f.Sources, ok)
				} else {
					out = append(out, warn.Render("Unlisted on sources"))
				}
				wrap("Note: ", strings.TrimLeft(f.Note, " ·"), bad)
				return out
			}
		}
		flds := m.filteredFolders()
		if m.ws.listCursor < len(flds) {
			f := flds[m.ws.listCursor]
			out = append(out, hi.Render("Folder"), oneLine(f.Dir))
			out = append(out, dim.Render(fmt.Sprintf("%s · %s", countLabel(len(f.Files), "file"), engine.Human(uint64(f.TotalBytes)))))
			if f.LinkedSources != "" {
				wrap("Sources: ", f.LinkedSources, ok)
			}
			if f.Orphans > 0 {
				out = append(out, warn.Render(fmt.Sprintf("%d unlisted files", f.Orphans)))
			}
			if f.Issues > 0 {
				out = append(out, bad.Render(fmt.Sprintf("%d files with issues", f.Issues)))
			}
		}
	case secSources:
		if m.ws.sourceID != "" {
			tracks := m.filteredSourceTracks()
			if m.ws.listCursor < len(tracks) {
				tr := tracks[m.ws.listCursor]
				out = append(out, hi.Render(oneLine(tr.Title)), collectionSyncBadge(tr, trackStatus(tr.Status), false))
				wrap("File: ", tr.Path, dim)
				wrap("Error: ", tr.Error, bad)
			}
			return out
		}
		srcs := m.filteredSources()
		if m.ws.listCursor < len(srcs) {
			s := srcs[m.ws.listCursor]
			out = append(out, hi.Render(oneLine(s.Title)), dim.Render(fmt.Sprintf("%s · %d/%d done", s.Type, s.Done, s.Total)))
			if s.Creator != "" {
				out = append(out, dim.Render("By "+oneLine(s.Creator)))
			}
			if s.Failed > 0 {
				out = append(out, bad.Render(fmt.Sprintf("%d failed", s.Failed)))
			}
			if s.Missing > 0 {
				out = append(out, bad.Render(fmt.Sprintf("%d missing on disk", s.Missing)))
			}
			if p := ytdlp.ProviderLabel(ytdlp.Provider(s.URL)); p != "" {
				out = append(out, dim.Render("Provider: "+p))
			}
		}
	case secIssues:
		iss := m.filteredIssues()
		if m.ws.listCursor < len(iss) {
			it := iss[m.ws.listCursor]
			badgeStyle := bad
			if it.Kind == "unlisted" || it.Kind == "relink_ready" {
				badgeStyle = warn
			}
			out = append(out, badgeStyle.Render(it.Badge), hi.Render(oneLine(it.Title)))
			if it.SourceTitle != "" {
				wrap("Source: ", it.SourceTitle, dim)
			}
			wrap("Path: ", it.Path, dim)
			wrap("Detail: ", it.Error, bad)
			if it.LibraryRow != nil && it.LibraryRow.Size > 0 {
				out = append(out, dim.Render(fmt.Sprintf("%s · %s", it.LibraryRow.Codec, engine.Human(uint64(it.LibraryRow.Size)))))
			}
		} else {
			out = append(out, ok.Render("Library & sources match cleanly."))
		}
	case secArtists:
		out = append(out, hi.Render("Artist explorer"))
		if m.artist.artist != nil {
			out = append(out, dim.Render(oneLine(m.artist.artist.Name)))
		}
	case secSettings:
		settingNotes := []string{
			"Saved in data.music", "1 to 8 tracks across sources", "Private playlists / Liked Music",
			"How to tag collaborators", "Folder layout for new files", "Clean removes video labels",
			"1 to 4 sources; shares track limit", "Move files without downloading", "Replace downloaded files",
			"Current: " + device.Label(m.eng.Root()), "Originals go to trash", "Formats, duplicates and undo",
		}
		if m.ws.listCursor < len(settings) {
			out = append(out, hi.Render(settings[m.ws.listCursor]))
		}
		if m.ws.listCursor < len(settingNotes) {
			wrap("", settingNotes[m.ws.listCursor], dim)
		}
	}
	return out
}

func (m *model) renderCompactWorkspace(b *strings.Builder, W, bodyH int) {
	innerH := max(3, bodyH-2)
	switch m.ws.focus {
	case paneMenu:
		b.WriteString(m.renderCompactMenu(W, innerH) + "\n")
	case paneSide:
		b.WriteString(m.renderSidePanel(W, innerH, 2, 2) + "\n")
	default:
		b.WriteString(m.renderMainPane(W, innerH) + "\n")
	}
}

func unifiedTrackBadge(tr engine.UnifiedTrack) string {
	switch tr.Status {
	case "synced":
		if tr.Duplicate {
			return bad.Render("duplicate")
		}
		return ok.Render("✓ Synced")
	case "failed":
		return bad.Render("✗ Failed")
	case "missing":
		return bad.Render("! Missing")
	case "unavailable":
		return dim.Render("- Unavail.")
	case "unlisted":
		return warn.Render("○ Unlisted")
	case "checking":
		return dim.Render("Checking…")
	case "removed":
		return dim.Render("Removed")
	default:
		return dim.Render("· Pending")
	}
}
