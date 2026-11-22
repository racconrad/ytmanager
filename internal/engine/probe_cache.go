package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"ytmanager/internal/device"
	"ytmanager/internal/ytdlp"
)

const probeCacheFile = ".ytmanager-cache.json"

// probeEntry stores what ffprobe and hashing found for one file version.
type probeEntry struct {
	Size     int64           `json:"size"`
	Modified int64           `json:"modified"`
	Info     ytdlp.AudioInfo `json:"info"`
	Hash     string          `json:"hash,omitempty"`
}

// probeCache skips ffprobe and hashing for files whose size and mtime are unchanged,
// the same evidence rsync trusts. It is persisted so reopening a folder stays fast.
type probeCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]probeEntry
	dirty   bool
}

func loadProbeCache(root string) *probeCache {
	c := &probeCache{path: filepath.Join(root, probeCacheFile), entries: map[string]probeEntry{}}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &c.entries) // a corrupt cache only costs a full rescan
	}
	return c
}

func (c *probeCache) get(rel string, stat os.FileInfo) (probeEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[rel]
	if !ok || entry.Size != stat.Size() || entry.Modified != stat.ModTime().UnixNano() {
		return probeEntry{}, false
	}
	return entry, true
}

func (c *probeCache) put(rel string, stat os.FileInfo, info ytdlp.AudioInfo, hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[rel] = probeEntry{stat.Size(), stat.ModTime().UnixNano(), info, hash}
	c.dirty = true
}

// prune drops files that a complete walk did not find.
func (c *probeCache) prune(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for rel := range c.entries {
		if !seen[rel] {
			delete(c.entries, rel)
			c.dirty = true
		}
	}
}

func (c *probeCache) save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}
	b, err := json.Marshal(c.entries)
	if err != nil {
		return err
	}
	if device.IsMTP(c.path) { // MTP cannot rename over an existing file
		err = os.WriteFile(c.path, b, 0o644)
	} else if err = os.WriteFile(c.path+".tmp", b, 0o644); err == nil {
		err = os.Rename(c.path+".tmp", c.path)
	}
	if err != nil {
		return err
	}
	c.dirty = false
	return nil
}
