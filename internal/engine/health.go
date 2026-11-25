package engine

import "ytmanager/internal/manifest"

type Health struct{ Done, Pending, Failed, Unavailable, Removed, Missing int }

func (e *Engine) CatalogSummary() manifest.Summary {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m.Summary()
}

func (e *Engine) Health() Health {
	e.mu.Lock()
	defer e.mu.Unlock()
	var h Health
	referenced := e.m.ReferencedTracks()
	for id, t := range e.m.Tracks {
		if t == nil {
			continue
		}
		switch t.Status {
		case manifest.StatusDone:
			if !e.trackPresent(t) {
				h.Missing++
			} else {
				h.Done++
			}
		case manifest.StatusPending:
			if referenced[id] {
				h.Pending++
			}
		case manifest.StatusFailed:
			if referenced[id] {
				h.Failed++
			}
		case manifest.StatusUnavailable:
			if referenced[id] {
				h.Unavailable++
			}
		case manifest.StatusRemoved:
			h.Removed++
		}
	}
	return h
}
