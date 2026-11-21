package tui

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ytmanager/internal/ytdlp"
)

const maxCoverCache = 48

type coverState struct {
	images  map[string]image.Image
	order   []string
	pending map[string]bool
	failed  map[string]bool
}

type coverLoadedMsg struct {
	key string
	img image.Image
	err error
}

var coverHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, FallbackDelay: 250 * time.Millisecond}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	},
}

func (m *model) coverImage(key string) image.Image {
	if key == "" || m.covers.images == nil {
		return nil
	}
	return m.covers.images[key]
}

func (m *model) requestCover(key string) tea.Cmd {
	if key == "" {
		return nil
	}
	if m.covers.images != nil && m.covers.images[key] != nil {
		return nil
	}
	if (m.covers.pending != nil && m.covers.pending[key]) || (m.covers.failed != nil && m.covers.failed[key]) {
		return nil
	}
	if m.covers.pending == nil {
		m.covers.pending = map[string]bool{}
	}
	m.covers.pending[key] = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		img, err := loadCoverImage(ctx, key)
		return coverLoadedMsg{key: key, img: img, err: err}
	}
}

func (m *model) storeCover(msg coverLoadedMsg) {
	if m.covers.pending != nil {
		delete(m.covers.pending, msg.key)
	}
	if msg.err != nil || msg.img == nil {
		if m.covers.failed == nil {
			m.covers.failed = map[string]bool{}
		}
		m.covers.failed[msg.key] = true
		return
	}
	if m.covers.images == nil {
		m.covers.images = map[string]image.Image{}
	}
	if _, exists := m.covers.images[msg.key]; !exists {
		if len(m.covers.order) >= maxCoverCache {
			oldest := m.covers.order[0]
			m.covers.order = m.covers.order[1:]
			delete(m.covers.images, oldest)
		}
		m.covers.order = append(m.covers.order, msg.key)
	}
	m.covers.images[msg.key] = msg.img
}

func loadCoverImage(ctx context.Context, key string) (image.Image, error) {
	if strings.HasPrefix(key, "https://") {
		req, err := http.NewRequestWithContext(ctx, "GET", key, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := coverHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("cover http %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 6<<20))
		if err != nil {
			return nil, err
		}
		return decodeCoverBytes(ctx, data)
	}
	ext := strings.ToLower(filepath.Ext(key))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		data, err := os.ReadFile(key)
		if err != nil {
			return nil, err
		}
		return decodeCoverBytes(ctx, data)
	default:
		cover := filepath.Join(filepath.Dir(key), "cover.jpg")
		if data, err := os.ReadFile(cover); err == nil {
			if img, err := decodeCoverBytes(ctx, data); err == nil {
				return img, nil
			}
		}
		tmp, err := os.CreateTemp("", "ytm-tui-cover-*.jpg")
		if err != nil {
			return nil, err
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)
		if err := ytdlp.ExtractCover(ctx, key, tmpPath); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return nil, err
		}
		return decodeCoverBytes(ctx, data)
	}
}

func decodeCoverBytes(ctx context.Context, data []byte) (image.Image, error) {
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return img, nil
	}
	// Fallback via ffmpeg handles WebP/AVIF thumbnails without extra Go dependencies.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-i", "pipe:0", "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, errors.New("unsupported cover image")
	}
	return png.Decode(bytes.NewReader(out))
}

// renderCoverArt renders img as ANSI truecolor half-block characters (cols wide, rows tall).
func renderCoverArt(img image.Image, cols, rows int) []string {
	if img == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	// Center-crop to square so 16:9 video thumbnails match album artwork framing.
	side := min(w, h)
	ox := b.Min.X + (w-side)/2
	oy := b.Min.Y + (h-side)/2

	sample := func(cx, cy, totX, totY int) (uint8, uint8, uint8) {
		x0 := ox + cx*side/totX
		x1 := max(x0+1, ox+(cx+1)*side/totX)
		y0 := oy + cy*side/totY
		y1 := max(y0+1, oy+(cy+1)*side/totY)
		var sr, sg, sb, n uint64
		for py := y0; py < y1; py++ {
			for px := x0; px < x1; px++ {
				r, g, bl, _ := img.At(px, py).RGBA()
				sr += uint64(r >> 8)
				sg += uint64(g >> 8)
				sb += uint64(bl >> 8)
				n++
			}
		}
		if n == 0 {
			return 0, 0, 0
		}
		return uint8(sr / n), uint8(sg / n), uint8(sb / n)
	}

	pixH := rows * 2
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		for c := 0; c < cols; c++ {
			tr, tg, tb := sample(c, r*2, cols, pixH)
			br, bg, bb := sample(c, r*2+1, cols, pixH)
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
		}
		sb.WriteString("\x1b[0m")
		lines[r] = sb.String()
	}
	return lines
}
