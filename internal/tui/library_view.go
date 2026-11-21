package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/engine"
)

func (m *model) libraryView(b *strings.Builder, w int) {
	if m.library.removeSource != nil {
		m.logs = nil
		header(b, "Remove source?")
		panel(b, w, []string{m.library.removeSource.Title, "Audio files will be kept.", "Use D on Sources to review its files."})
		m.footer(b, m.help())
		return
	}
	// Review/report text must have space even on the minimum supported terminal.
	if m.library.candidates != nil || m.library.plan != nil || m.library.report != nil || m.library.help || m.library.purge || m.library.restore {
		m.logs = nil
	}
	if m.library.details != nil || m.library.report != nil || m.library.help {
		m.logs = nil
		label := "Library help"
		if m.library.details != nil {
			label = "Track details / duplicate comparison"
		}
		if m.library.report != nil {
			label = "File operation report"
		}
		header(b, label)
		if m.library.report != nil {
			b.WriteString(ansi.Truncate(m.library.report.Summary(), w, "…") + "\n")
		}
		lines := m.libraryReadLines()
		if m.library.details != nil {
			m.renderWithCoverPanel(b, w, lines, min(m.cursor, len(lines)-1), 1, m.libraryCoverKey(), nil)
		} else {
			m.listPanel(b, w, lines, min(m.cursor, len(lines)-1), 1, 0)
		}
		m.footer(b, m.help())
		return
	}
	if m.library.purge || m.library.restore {
		label := "Empty trash permanently?"
		text := "All saved trash batches. No undo."
		if m.library.restore {
			label = "Restore latest trash batch?"
			text = "Existing files will be kept; conflicts are reported."
		}
		header(b, label)
		panel(b, w, []string{text})
		m.footer(b, m.help())
		return
	}
	if m.library.tools && m.library.plan == nil {
		header(b, "Format and folder tools")
		b.WriteString(m.libraryActivity(w))
		b.WriteString(fmt.Sprintf("%d target files · no marks = all results\n", len(m.librarySelection(true))))
		layout := m.library.layout
		if layout == "" {
			layout = "Artist/Album"
		}
		rows := []string{"Format          ‹ " + m.library.targetFormat + " ›", "Folders         ‹ " + layout + " ›", "Preview changes", "Restore latest trash batch", "Empty trash permanently", "Reset: trash files, download again", "Help with formats and organization"}
		for i := range rows {
			rows[i] = pick(i == m.cursor, rows[i])
		}
		m.listPanel(b, w, rows, m.cursor, 1, 0)
		m.footer(b, m.help())
		return
	}
	if m.library.plan != nil {
		header(b, fmt.Sprintf("Preview %d changes", len(m.library.plan.Changes)))
		b.WriteString("Conversion cannot improve source quality.\n")
		var lines []string
		for i, c := range m.library.plan.Changes {
			action := "move"
			if c.Convert {
				action = "convert; original → trash"
			}
			lines = append(lines, pick(i == m.cursor, c.Track.Path+" → "+c.To), dim.Render(action+" "+c.Problem))
		}
		if len(lines) == 0 {
			lines = []string{"No changes needed for these settings."}
		}
		m.listPanel(b, w, lines, m.cursor*2, 2, 0)
		m.footer(b, m.help())
		return
	}
	if m.library.candidates == nil {
		m.libraryCatalogView(b, w)
		return
	}
	m.libraryTrashView(b, w)
}

func (m model) libraryTrashView(b *strings.Builder, w int) {
	if m.library.reset {
		header(b, "Reset "+countLabel(len(m.library.confirm), "file")+"?")
		b.WriteString("Audio + lyrics go to trash. u restores.\n")
		b.WriteString("Next sync downloads them again. X frees space.\n")
	} else {
		header(b, "Trash "+countLabel(len(m.library.confirm), "file")+"?")
		b.WriteString("Audio + lyrics. u restores.\n")
		b.WriteString("Skipped by future syncs.\n")
	}
	if !m.library.includeShared && len(m.library.candidates) > len(m.library.confirm) {
		b.WriteString(fmt.Sprintf("%d shared kept · h toggles\n", len(m.library.candidates)-len(m.library.confirm)))
	}
	byID := map[string]engine.LibraryTrack{}
	for _, r := range m.library.candidates {
		byID[r.ID] = r
	}
	var lines []string
	for i, id := range m.library.confirm {
		r := byID[id]
		lines = append(lines, pick(i == m.cursor, oneLine(r.Title)), dim.Render("    "+destinationLabel(oneLine(r.Path), max(1, w-8))))
	}
	if len(lines) == 0 {
		b.WriteString("Shared tracks kept. h: include shared.\n")
	} else {
		m.listPanel(b, w, lines, m.cursor*2, 2, 0)
	}
	m.footer(b, m.help())
}

func (m model) libraryReadLines() []string {
	w := max(1, m.terminalWidth()-8)
	var raw []string
	if m.library.details != nil {
		r := m.library.details
		text := fmt.Sprintf("Title: %s\nArtist: %s\nAlbum: %s\nPath: %s\nStatus: %s\nSources: %s\nCodec: %s\nContainer: %s\nBitrate: ~%d kbps (estimate if unavailable)\nSample rate: %d Hz · %d channels\nDuration: %.2fs\nSize: %s\nSHA-256: %s\nEvidence: %s", r.Title, r.Artist, r.Album, r.Path, r.Status, r.Sources, r.Codec, r.Container, r.Bitrate/1000, r.SampleRate, r.Channels, r.Duration, engine.Human(uint64(r.Size)), r.Hash, r.Note)
		raw = strings.Split(text, "\n")
	} else if m.library.report != nil {
		for _, r := range m.library.report.Items {
			raw = append(raw, r.Outcome+" · "+r.Path+" "+r.Error)
		}
		if len(raw) == 0 {
			raw = []string{"No files changed."}
		}
	} else {
		raw = []string{
			"g: open Sources and downloads. O: settings. c: change folder.",
			"Paste a source link to open it through Sources.",
			"s: scan the selected folder for added, missing or modified music.",
			"d: review files, then Enter moves audio + lyrics to trash.",
			"u: restore latest trash batch, even after restarting.",
			"b: browse by artist/album; Enter opens an album.",
			"/: search; space: mark; a: mark visible results.",
			"i: inspect file and audio details; F: find the artist online.",
			"Album deletion keeps shared tracks; h includes them.",
			"f: compare possible duplicates; identical SHA-256 means identical bytes.",
			"o: files outside all saved sources and local playlists.",
			"!: missing, unreadable, invalid or mismatched formats.",
			"X: permanently empty trash, after confirmation.",
			"Trash remains on this destination; empty it to free space.",
			"t: preview format conversion and folder organization.",
			"t → Reset: trash files and mark them for a clean download.",
			"Use ! first to reset only broken files; no filter resets all.",
			"No marks in tools: all visible files are targeted.",
			"Opus: compact; M4A: Apple; MP3: older players.",
			"Re-encoding cannot improve quality. Originals go to trash.",
			"Folder changes move audio without re-encoding.",
			"Conflicting destinations are blocked; nothing is overwritten.",
			"Sources d removes the source; Library d removes files.",
			"Sources r relinks moved files using embedded YouTube IDs.",
		}
	}
	var lines []string
	for _, line := range raw {
		lines = append(lines, strings.Split(ansi.Wrap(line, w, ""), "\n")...)
	}
	return lines
}

// libraryActivity shows a scan or file operation is still running, redrawn by the UI tick.
func (m model) libraryActivity(w int) string {
	if !m.library.scanning && !m.library.busy {
		return ""
	}
	elapsed := max(time.Duration(0), time.Since(m.library.since)).Truncate(time.Second)
	frame := []string{"|", "/", "-", "\\"}[m.cat.frame%4]
	label := fmt.Sprintf("%s Working on files · %s · x cancels", frame, elapsed)
	if m.library.scanning {
		done, total := m.eng.ScanProgress()
		label = fmt.Sprintf("%s Scanning files %d/%d · %s · x cancels", frame, done, total, elapsed)
	}
	return hi.Render(ansi.Truncate(label, max(1, w), "…")) + "\n"
}
