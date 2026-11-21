package ytmusic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInnertubeRetriesTransientFailureAndRespectsCancel(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	origBase := innertubeBaseURL
	origClient := client
	defer func() {
		innertubeBaseURL = origBase
		client = origClient
	}()
	innertubeBaseURL = srv.URL + "/"
	client = &http.Client{Timeout: 2 * time.Second}

	got, err := innertube(context.Background(), "next", map[string]any{"videoId": "abc"})
	if err != nil {
		t.Fatalf("expected retry to succeed, got err: %v", err)
	}
	m, _ := got.(map[string]any)
	if m["ok"] != true || hits.Load() != 2 {
		t.Fatalf("unexpected response/hits: %v hits=%d", got, hits.Load())
	}

	// Cancelled parent context must fail fast without retrying.
	hits.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := innertube(ctx, "next", map[string]any{}); err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if hits.Load() != 0 {
		t.Fatalf("expected 0 requests for pre-cancelled ctx, got %d", hits.Load())
	}
}
