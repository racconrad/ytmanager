package engine

import (
	"context"
	"fmt"

	"ytmanager/internal/ytdlp"
	"ytmanager/internal/ytmusic"
)

// TrackResolution describes the verified media target for a single track download.
type TrackResolution struct {
	DownloadID string
	TargetURL  string
	LyricsID   string
}

// TrackProvider defines provider-specific resolution and metadata hooks.
type TrackProvider struct {
	Resolve             func(ctx context.Context, requestedID, title, mediaURL string, stage func(string), log func(string)) (TrackResolution, error)
	PrepareInfo         func(info map[string]any)
	NeedsAlternativeErr string
}

func (e *Engine) defaultProviders() map[string]TrackProvider {
	return map[string]TrackProvider{
		ytdlp.YouTube: {
			Resolve:             e.resolveYouTubeMusic,
			NeedsAlternativeErr: ytmusic.ErrNoSong.Error(),
		},
		"soundcloud": {
			PrepareInfo: func(info map[string]any) {
				if len(creditedArtists(info)) == 0 {
					info["artist"] = info["uploader"]
				}
			},
		},
	}
}

// RegisterProvider registers or overrides a track provider handler on the engine.
func (e *Engine) RegisterProvider(name string, provider TrackProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.providers == nil {
		e.providers = e.defaultProviders()
	}
	e.providers[name] = provider
}

func (e *Engine) provider(name string) (TrackProvider, bool) {
	e.mu.Lock()
	providers := e.providers
	e.mu.Unlock()
	if providers == nil {
		providers = e.defaultProviders()
	}
	p, ok := providers[name]
	return p, ok
}

func (e *Engine) resolveTrack(ctx context.Context, provider, id, title, target, alt string, stage func(string), emit func(Event)) (TrackResolution, error) {
	requested := id
	if alt != "" {
		requested = alt
	}
	p, ok := e.provider(provider)
	if ok && p.Resolve != nil {
		res, err := p.Resolve(ctx, requested, title, target, stage, func(msg string) {
			emit(Event{Kind: EvLog, ID: id, Msg: msg})
		})
		if err != nil {
			return TrackResolution{}, err
		}
		if res.TargetURL == "" {
			return TrackResolution{}, fmt.Errorf("provider %s returned no download URL", provider)
		}
		if res.DownloadID == "" {
			res.DownloadID = requested
		}
		return res, nil
	}
	if target == "" {
		return TrackResolution{}, fmt.Errorf("missing download URL for %s track %s", provider, requested)
	}
	return TrackResolution{DownloadID: requested, TargetURL: target}, nil
}

func (e *Engine) resolveYouTubeMusic(ctx context.Context, requestedID, title, _ string, stage func(string), log func(string)) (TrackResolution, error) {
	stage("Checking YouTube Music")
	resolve := e.resolveSong
	if resolve == nil {
		resolve = ytmusic.Song
	}
	dl, err := resolve(ctx, requestedID)
	if err == nil && dl == "" {
		err = ytmusic.ErrNoSong
	}
	if err != nil {
		return TrackResolution{}, err
	}
	if dl != requestedID {
		log("Using YouTube Music audio: " + title + " (" + dl + ")")
	}
	return TrackResolution{
		DownloadID: dl,
		TargetURL:  ytdlp.MusicURL(dl),
		LyricsID:   dl,
	}, nil
}

func (e *Engine) prepareProviderInfo(provider string, info map[string]any) {
	if p, ok := e.provider(provider); ok && p.PrepareInfo != nil {
		p.PrepareInfo(info)
	}
}

func (e *Engine) isAlternativeError(errText string) bool {
	if errText == "" {
		return false
	}
	providers := e.providers
	if providers == nil {
		providers = e.defaultProviders()
	}
	for _, p := range providers {
		if p.NeedsAlternativeErr != "" && errText == p.NeedsAlternativeErr {
			return true
		}
	}
	return false
}
