package ytdlp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDownloadPreservesExistingFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "stage", "Song.opus")
	dst := filepath.Join(root, "Song.opus")
	if err := os.MkdirAll(filepath.Dir(src), 0700); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(root, "Song (2).lrc")
	for path, data := range map[string]string{src: "new", dst: "old", sidecar: "custom lyrics"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := copyDownload(src, dst)
	if err != nil || path != filepath.Join(root, "Song (3).opus") {
		t.Fatalf("copy path = %q, err = %v", path, err)
	}
	for path, want := range map[string]string{path: "new", dst: "old", sidecar: "custom lyrics"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q, %v", path, data, err)
		}
	}
}
