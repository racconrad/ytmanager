package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
)

const kittyImageID = 7741

type coverPlacement struct {
	key  string
	img  image.Image
	row  int
	col  int
	cols int
	rows int
}

// coverOverlay pins a native Kitty Graphics Protocol image on top of the reserved
// box after Bubble Tea flushes each frame, using synchronized output to prevent
// flicker and deleting stale placements before painting new screens.
type coverOverlay struct {
	out            io.Writer
	file           *os.File
	enabled        bool
	mu             sync.Mutex
	want           coverPlacement
	transmittedKey string
	placed         bool
	lastPos        [4]int
	encoded        map[string]string
	encodedOrder   []string
}

func newCoverOverlay(out *os.File) *coverOverlay {
	return &coverOverlay{out: out, file: out, enabled: kittySupported()}
}

func (o *coverOverlay) Read(p []byte) (int, error) {
	if o.file != nil {
		return o.file.Read(p)
	}
	return 0, io.EOF
}

func (o *coverOverlay) Close() error {
	if o.file != nil {
		return o.file.Close()
	}
	return nil
}

func (o *coverOverlay) Fd() uintptr {
	if o.file != nil {
		return o.file.Fd()
	}
	return ^uintptr(0)
}

func kittySupported() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(term, "kitty") {
		return true
	}
	prog := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return prog == "ghostty" || prog == "wezterm"
}

func (o *coverOverlay) setPlacement(p *coverPlacement) {
	if o == nil || !o.enabled {
		return
	}
	o.mu.Lock()
	if p == nil {
		o.want = coverPlacement{}
	} else {
		o.want = *p
	}
	o.mu.Unlock()
}

func (o *coverOverlay) Write(p []byte) (int, error) {
	if !o.enabled || len(p) == 0 {
		return o.out.Write(p)
	}
	o.mu.Lock()
	want := o.want
	transmitted := o.transmittedKey
	placed := o.placed
	lastPos := o.lastPos
	o.mu.Unlock()

	pos := [4]int{want.row, want.col, want.cols, want.rows}
	var pre, post strings.Builder

	if want.key == "" {
		if placed {
			pre.WriteString(kittyDeleteAllSeq())
			o.mu.Lock()
			o.placed = false
			o.lastPos = [4]int{}
			o.mu.Unlock()
		}
	} else {
		if placed && (want.key != transmitted || pos != lastPos) {
			pre.WriteString(kittyDeleteAllSeq())
		}
		if want.key != transmitted {
			seq, ok := o.cachedTransmit(want.key)
			if !ok {
				pngBytes, pngErr := encodeSquarePNG(want.img)
				if pngErr == nil {
					seq = encodeKittyTransmit(kittyImageID, pngBytes)
					o.storeTransmit(want.key, seq)
					ok = true
				}
			}
			if ok {
				post.WriteString(seq)
				o.mu.Lock()
				o.transmittedKey = want.key
				o.mu.Unlock()
			}
		}
		post.WriteString(kittyPlaceSeq(kittyImageID, want.row, want.col, want.cols, want.rows))
		o.mu.Lock()
		o.placed = true
		o.lastPos = pos
		o.mu.Unlock()
	}

	var frame bytes.Buffer
	frame.WriteString("\x1b[?2026h")
	frame.WriteString(pre.String())
	frame.Write(p)
	frame.WriteString(post.String())
	frame.WriteString("\x1b[?2026l")

	if _, err := o.out.Write(frame.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o *coverOverlay) cachedTransmit(key string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.encoded == nil {
		return "", false
	}
	seq, ok := o.encoded[key]
	return seq, ok
}

func (o *coverOverlay) storeTransmit(key, seq string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.encoded == nil {
		o.encoded = map[string]string{}
	}
	if _, exists := o.encoded[key]; !exists {
		if len(o.encodedOrder) >= 16 {
			oldest := o.encodedOrder[0]
			o.encodedOrder = o.encodedOrder[1:]
			delete(o.encoded, oldest)
		}
		o.encodedOrder = append(o.encodedOrder, key)
	}
	o.encoded[key] = seq
}

func (o *coverOverlay) close() {
	if o == nil || !o.enabled {
		return
	}
	o.mu.Lock()
	placed := o.placed
	o.placed = false
	o.want = coverPlacement{}
	o.mu.Unlock()
	if placed {
		_, _ = io.WriteString(o.out, kittyDeleteAllSeq())
	}
}

func encodeSquarePNG(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	side := min(w, h)
	ox := b.Min.X + (w-side)/2
	oy := b.Min.Y + (h-side)/2
	sq := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			sq.Set(x, y, img.At(ox+x, oy+y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sq); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeKittyTransmit(id int, pngData []byte) string {
	b64 := base64.StdEncoding.EncodeToString(pngData)
	const chunk = 4096
	var out strings.Builder
	for i := 0; i < len(b64); i += chunk {
		end := min(len(b64), i+chunk)
		more := 1
		if end == len(b64) {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&out, "\x1b_Ga=t,f=100,i=%d,q=2,m=%d;%s\x1b\\", id, more, b64[i:end])
		} else {
			fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, b64[i:end])
		}
	}
	return out.String()
}

func kittyPlaceSeq(id, row, col, cols, rows int) string {
	return fmt.Sprintf("\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\\x1b8", row, col, id, cols, rows)
}

func kittyDeleteAllSeq() string {
	return "\x1b_Ga=d,d=a,q=2\x1b\\"
}
