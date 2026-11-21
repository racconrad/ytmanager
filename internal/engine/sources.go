package engine

import (
	"context"
	"fmt"
	"strings"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

const cookieHint = " (if it is private, set cookies in settings: o)"

type sourceResult struct {
	ids []string
	err error
}

func (e *Engine) readSource(ctx context.Context, request manifest.SourceRequest, emit func(Event)) sourceResult {
	fail := func(err error) sourceResult {
		if ctx.Err() == nil {
			msg := "✗ " + request.URL + ": " + err.Error()
			if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "cookies") {
				msg += cookieHint
			}
			emit(Event{Kind: EvLog, Msg: msg, Err: err})
		}
		return sourceResult{err: err}
	}

	e.mu.Lock()
	if source := e.sourceForURL(request.URL); !request.Refresh && source != nil && len(source.Tracks) > 0 {
		id, title, settled := source.ID, source.Title, e.sourceSettled(source)
		e.completeRequest(request.URL)
		err := e.m.Save()
		e.mu.Unlock()
		if err != nil {
			return fail(fmt.Errorf("could not save queue: %w", err))
		}
		if settled {
			emit(Event{Kind: EvLog, Msg: "Already downloaded: " + title + "; skipped."})
			return sourceResult{}
		}
		emit(Event{Kind: EvLog, Msg: "Using saved tracks: " + title})
		return sourceResult{ids: []string{id}}
	}
	e.mu.Unlock()

	emit(Event{Kind: EvReading, ID: request.URL})
	defer emit(Event{Kind: EvReadDone, ID: request.URL})
	emit(Event{Kind: EvLog, Msg: "Reading " + request.URL})
	listing, err := e.Inspect(ctx, request.URL)
	if ctx.Err() != nil {
		return sourceResult{err: ctx.Err()}
	}
	if err != nil {
		return fail(err)
	}
	if len(listing.Entries) == 0 || listing.ID == "" {
		return fail(fmt.Errorf("no tracks%s", cookieHint))
	}
	e.mu.Lock()
	source := e.mergeSourceListing(listing, request.URL)
	e.completeRequest(request.URL)
	err = e.m.Save()
	id := source.ID
	e.mu.Unlock()
	if err != nil {
		return fail(fmt.Errorf("could not save data.music: %w", err))
	}
	emit(Event{Kind: EvLog, Msg: fmt.Sprintf("Queued %s (%s)", listing.Title, tracks(len(listing.Entries)))})
	return sourceResult{ids: []string{id}}
}

// mergeSourceListing is called with e.mu held.
func (e *Engine) mergeSourceListing(listing *ytdlp.Listing, rawURL string) *manifest.Source {
	entries := make([]manifest.Entry, len(listing.Entries))
	for i, entry := range listing.Entries {
		entries[i] = manifest.Entry{ID: entry.ID, Title: entry.Title, MediaURL: entry.URL, Duration: entry.Duration, Unavailable: entry.Unavailable}
	}
	id := listing.ID
	if listing.Type == "album" && strings.HasPrefix(id, "OLAK5uy_") && e.m.Source(id) == nil {
		// Older previews stored the browse ID; yt-dlp resolves that same URL to a playlist ID.
		if saved := e.sourceForURL(rawURL); saved != nil && saved.URL == rawURL && saved.Type == "album" && strings.HasPrefix(saved.ID, "MPREb") {
			id = saved.ID
		}
	}
	source := e.m.Merge(listing.Type, id, listing.Title, rawURL, entries)
	if listing.Cover != "" {
		source.Cover = listing.Cover
	}
	if listing.Creator != "" {
		source.Creator = listing.Creator
	}
	if listing.Year != "" {
		source.Year = listing.Year
	}
	return source
}
