package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"ytmanager/internal/engine"
)

type physicalSyncState struct {
	plan   engine.PhysicalSyncPlan
	opts   engine.PhysicalSyncOptions
	cursor int
}

type physicalSyncDoneMsg struct {
	eng    *engine.Engine
	report engine.PhysicalSyncReport
	err    error
}

func (m *model) openPhysicalSync() (tea.Model, tea.Cmd) {
	if m.ws.section != secFolders {
		m.log("Open Folders and select a folder before syncing changes to sources.")
		return *m, nil
	}
	if m.run.busy || m.library.busy {
		m.log("Wait for the current operation to finish.")
		return *m, nil
	}
	if m.library.scanning || !m.library.complete {
		m.log("Scanning device files before comparing with sources…")
		if m.library.scanning {
			return *m, nil
		}
		return *m, m.scanLibrary()
	}
	rows := m.physicalSyncRows()
	if len(rows) == 0 {
		m.log("Select a folder with scanned audio files first.")
		return *m, nil
	}
	plan := m.eng.PreviewPhysicalSync(rows)
	if len(plan.Changes) == 0 {
		m.log(ok.Render("Physical files and sources already match."))
		return *m, nil
	}
	opts := engine.PhysicalSyncOptions{
		Relink:     plan.Count(engine.ChangeRelink) > 0,
		Metadata:   plan.Count(engine.ChangeMetadata) > 0,
		AdoptLocal: plan.Count(engine.ChangeAdoptLocal) > 0,
	}
	m.ws.syncPlan = &physicalSyncState{plan: plan, opts: opts}
	return *m, nil
}

func (m model) physicalSyncRows() []engine.LibraryTrack {
	if folder, ok := m.activeFolder(); ok {
		return folder.Files
	}
	folders := m.filteredFolders()
	if m.ws.listCursor >= 0 && m.ws.listCursor < len(folders) {
		return folders[m.ws.listCursor].Files
	}
	return nil
}

func (m *model) physicalSyncKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.ws.syncPlan
	if st == nil || k.Paste {
		return *m, nil
	}
	enabled := st.plan.EnabledChanges(st.opts)
	page := max(1, m.terminalHeight()-10)
	switch k.String() {
	case "esc":
		m.ws.syncPlan = nil
	case "1":
		if st.plan.Count(engine.ChangeRelink) > 0 {
			st.opts.Relink = !st.opts.Relink
			st.cursor = 0
		}
	case "2":
		if st.plan.Count(engine.ChangeMetadata) > 0 {
			st.opts.Metadata = !st.opts.Metadata
			st.cursor = 0
		}
	case "3":
		if st.plan.Count(engine.ChangeAdoptLocal) > 0 {
			st.opts.AdoptLocal = !st.opts.AdoptLocal
			st.cursor = 0
		}
	case "4":
		if st.plan.Count(engine.ChangeAdoptDelete) > 0 {
			st.opts.AdoptDelete = !st.opts.AdoptDelete
			st.cursor = 0
		}
	case "up", "k":
		st.cursor = max(0, st.cursor-1)
	case "down", "j":
		if len(enabled) > 0 {
			st.cursor = min(len(enabled)-1, st.cursor+1)
		}
	case "pgup":
		st.cursor = max(0, st.cursor-page)
	case "pgdown":
		if len(enabled) > 0 {
			st.cursor = min(len(enabled)-1, st.cursor+page)
		}
	case "home":
		st.cursor = 0
	case "end":
		if len(enabled) > 0 {
			st.cursor = len(enabled) - 1
		}
	case "enter":
		if len(enabled) == 0 {
			m.log("Enable at least one category (1-4) to apply.")
			return *m, nil
		}
		plan, opts, eng := st.plan, st.opts, m.eng
		m.ws.syncPlan = nil
		m.library.busy = true
		return *m, func() tea.Msg {
			report, err := eng.ApplyPhysicalSync(plan, opts)
			return physicalSyncDoneMsg{eng: eng, report: report, err: err}
		}
	}
	return *m, nil
}

func (m model) finishPhysicalSync(msg physicalSyncDoneMsg) (tea.Model, tea.Cmd) {
	if msg.eng != m.eng {
		return m, nil
	}
	m.library.busy = false
	if msg.err != nil {
		m.fail(msg.err)
		return m, nil
	}
	m.log(ok.Render(msg.report.Summary()))
	m.refreshWorkspace()
	if m.library.complete && !m.library.scanning {
		return m, m.scanLibrary()
	}
	return m, nil
}

func (m model) physicalSyncView(b *strings.Builder) {
	st := m.ws.syncPlan
	if st == nil {
		return
	}
	w := max(26, m.terminalWidth()-2)
	enabled := st.plan.EnabledChanges(st.opts)
	header(b, fmt.Sprintf("Apply disk changes (%d)", len(enabled)))

	toggle := func(num string, on bool, label string, n int) string {
		if n == 0 {
			return dim.Render(fmt.Sprintf("[·] %s %s (0)", num, label))
		}
		box := "[ ]"
		style := dim
		if on {
			box = "[x]"
			style = hi
		}
		return style.Render(fmt.Sprintf("%s %s %s (%d)", box, num, label, n))
	}
	toggles := []string{
		toggle("1", st.opts.Relink, "Relink", st.plan.Count(engine.ChangeRelink)),
		toggle("2", st.opts.Metadata, "Tags", st.plan.Count(engine.ChangeMetadata)),
		toggle("3", st.opts.AdoptLocal, "Existing album", st.plan.Count(engine.ChangeAdoptLocal)),
		toggle("4", st.opts.AdoptDelete, "Deleted", st.plan.Count(engine.ChangeAdoptDelete)),
	}
	if w >= 72 {
		b.WriteString(ansi.Truncate(strings.Join(toggles, "  "), w, "…") + "\n")
	} else {
		b.WriteString(ansi.Truncate(toggles[0]+" "+toggles[1], w, "…") + "\n")
		b.WriteString(ansi.Truncate(toggles[2]+" "+toggles[3], w, "…") + "\n")
	}
	b.WriteString(dim.Render("Selected folder only. New audio joins existing albums; no sources are created. A data.music backup is made before writing.") + "\n\n")

	avail := max(1, m.terminalHeight()-10)
	if len(enabled) == 0 {
		b.WriteString(dim.Render("No categories enabled. Press 1-4 to include changes.") + "\n")
	} else {
		st.cursor = max(0, min(len(enabled)-1, st.cursor))
		page := min(len(enabled), avail)
		start := min(max(0, st.cursor-page/2), len(enabled)-page)
		for i, c := range enabled[start : start+page] {
			tag := physicalChangeTag(c.Kind)
			line := fmt.Sprintf("%s %s · %s", tag, oneLine(c.Title), oneLine(c.Detail))
			b.WriteString(pick(start+i == st.cursor, ansi.Truncate(line, max(10, w-2), "…")) + "\n")
		}
		if len(enabled) > page {
			b.WriteString(dim.Render(fmt.Sprintf("  %d-%d of %d", start+1, start+page, len(enabled))) + "\n")
		}
	}
	help := "1-4 toggle · enter apply · esc cancel"
	if len(enabled) > avail {
		help = "↑↓ review · " + help
	}
	b.WriteString("\n" + strings.Join(m.helpLines(help), "\n"))
}

func physicalChangeTag(kind engine.PhysicalChangeKind) string {
	switch kind {
	case engine.ChangeRelink:
		return hi.Render("[Relink]")
	case engine.ChangeMetadata:
		return ok.Render("[Tags]")
	case engine.ChangeAdoptLocal:
		return warn.Render("[Existing album]")
	case engine.ChangeAdoptDelete:
		return bad.Render("[Deleted]")
	default:
		return dim.Render("[Change]")
	}
}
