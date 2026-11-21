// Package ytmusic resolves the audio recording linked by YouTube Music.
package ytmusic

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var (
	innertubeBaseURL = "https://music.youtube.com/youtubei/v1/"
	transport        = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:       6 * time.Second,
			KeepAlive:     30 * time.Second,
			FallbackDelay: 250 * time.Millisecond,
		}).DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       20 * time.Second,
		MaxIdleConnsPerHost:   8,
		// Keep HTTP/1.1 so one stalled stream never blocks concurrent workers on a shared HTTP/2 socket.
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	client = &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
	}
)

const (
	// fallbackVersion is used when the current client version cannot be read from YT Music's page.
	fallbackVersion = "1.20261020.10.00"
	browserUA       = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

var (
	versionOnce sync.Once
	version     string
	versionRe   = regexp.MustCompile(`"INNERTUBE_CLIENT_VERSION":"([^"]+)"`)
)

// clientVersion reads the innertube client version YT Music currently serves, once per run,
// because old versions eventually get rejected.
func clientVersion(ctx context.Context) string {
	versionOnce.Do(func() {
		version = fallbackVersion
		vctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(vctx, "GET", "https://music.youtube.com/", nil)
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Cookie", "SOCS=CAI") // skips the EU consent page
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		page, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if m := versionRe.FindSubmatch(page); m != nil {
			version = string(m[1])
		}
	})
	return version
}

func innertube(ctx context.Context, endpoint string, body any) (any, error) {
	b, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt > 0 {
			transport.CloseIdleConnections()
			delay := time.Duration(attempt) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		v, retry, err := innertubeOnce(ctx, endpoint, b)
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func innertubeOnce(ctx context.Context, endpoint string, payload []byte) (any, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", innertubeBaseURL+endpoint+"?prettyPrint=false", bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://music.youtube.com")
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		retry := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || (errors.As(err, &netErr) && netErr.Timeout())
		// Always retry unknown network dial/reset errors when parent context is still alive.
		if ctx.Err() == nil {
			retry = true
		}
		return nil, retry, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("youtubei %s: http %d", endpoint, resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("youtubei %s: http %d", endpoint, resp.StatusCode)
	}
	var v any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&v); err != nil {
		return nil, ctx.Err() == nil, err
	}
	return v, false, nil
}
