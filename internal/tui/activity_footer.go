package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) wideActivityFooter() bool {
	return m.terminalWidth() >= 88 && m.terminalHeight() >= 24
}

func (m model) activityColumns() (cat, logs int) {
	cat = min(56, m.terminalWidth()/2)
	return cat, m.terminalWidth() - cat - 2
}

func (m model) activityFooterHeight() int {
	if m.wideActivityFooter() {
		return max(5, m.logPage()+1)
	}
	return 2
}

func (m model) writeActivityFooter(b *strings.Builder) {
	status := m.downloadStatus()
	if m.wideActivityFooter() {
		logs := []string{dim.Render("Logs")}
		if len(m.logs) > 0 {
			logs = strings.Split(m.logsView(), "\n")
		}
		catW, logW := m.activityColumns()
		cat := append([]string{status}, strings.Split(m.renderCat(catW, false), "\n")...)
		for i := range m.activityFooterHeight() {
			left := ansi.Truncate(footerRow(cat, i), catW, "…")
			right := ansi.Truncate(footerRow(logs, i), logW, "…")
			b.WriteString(left + strings.Repeat(" ", catW-ansi.StringWidth(left)+2) + right + "\n")
		}
		return
	}
	b.WriteString(m.compactLog() + "\n")
	b.WriteString(ansi.Truncate(ok.Render("=^.^=")+" "+status, m.terminalWidth(), "…") + "\n")
}

func footerRow(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return ""
}

func (m model) downloadStatus() string {
	switch {
	case m.run.busy && m.eng.Paused():
		if m.run.total > 0 {
			processed := m.run.done + m.run.failed + m.run.unavailable
			return warn.Render(fmt.Sprintf("Paused %d/%d · p resume", processed, m.run.total))
		}
		return warn.Render("Paused · p to resume")
	case m.run.busy && m.run.total > 0:
		processed := m.run.done + m.run.failed + m.run.unavailable
		line := hi.Render(fmt.Sprintf("%d/%d processed", processed, m.run.total))
		if len(m.run.order) > 0 {
			if job := m.run.jobs[m.run.order[0]]; job != nil {
				line += dim.Render(" · " + jobStatus(job) + " · " + oneLine(job.title))
			}
		}
		return line
	case m.run.busy && m.run.readingURL != "":
		return hi.Render(m.readingStatus())
	case m.run.busy && len(m.run.order) > 0:
		if active := strings.TrimSpace(m.activeAlbums(m.terminalWidth() - 6)); active != "" {
			return active
		}
		return hi.Render(m.spinnerFrame() + " Working…")
	case m.run.busy && m.run.verb == "downloaded":
		return hi.Render(m.spinnerFrame() + " Preparing downloads…")
	case m.run.busy:
		return hi.Render(m.spinnerFrame() + " Working…")
	case time.Now().Before(m.cat.moodUntil) && m.cat.sad:
		return bad.Render("Finished with errors")
	case time.Now().Before(m.cat.moodUntil):
		return ok.Render("Finished")
	default:
		return dim.Render("Ready")
	}
}
