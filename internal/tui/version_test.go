package tui

import (
	"fmt"
	"strings"
	"testing"

	"ytmanager/internal/buildinfo"
)

func TestSettingsShowsAppVersion(t *testing.T) {
	for _, size := range [][2]int{{30, 12}, {80, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := sized(scrollModel(t), size[0], size[1])
			m.screen, m.cursor = screenSettings, 10
			if view := assertFits(t, m); !strings.Contains(view, "Version: "+buildinfo.Version) {
				t.Fatalf("settings do not show the release version:\n%s", view)
			}
		})
	}
}
