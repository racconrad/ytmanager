package tui

import (
	"strings"
	"testing"
)

func TestDownloadFailurePointsToProblemReview(t *testing.T) {
	m := scrollModel(t)
	m.run = run{verb: "downloaded", failed: 4}
	m.finish(nil)
	log := strings.Join(m.logs, "\n")
	if !strings.Contains(log, "4 failed (4 to review)") || strings.Contains(log, "v or n") {
		t.Fatalf("wrong recovery hint: %s", log)
	}
	if m = press(m, "4"); m.ws.section != secIssues {
		t.Fatal("advertised shortcut did not open problem review")
	}
}
