package engine

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"ytmanager/internal/manifest"
	"ytmanager/internal/ytdlp"
)

func TestSaveCollectionPreviewKeepsAllTracksForResume(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the HTTPS fixture uses SSL_CERT_FILE to trust a local proxy")
	}
	const fixtureProcess = "YTMANAGER_TEST_COLLECTION_PROXY"
	if os.Getenv(fixtureProcess) != "1" {
		stubCollectionBrowse(t)
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), fixtureProcess+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("preview fixture failed: %v\n%s", err, out)
		}
		return
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case " $* " in
*' --flat-playlist -J '*) printf '%s\n' '{"_type":"playlist","id":"PLfixture","title":"Playlist","entries":[{"id":"one","title":"First"},{"id":"two","title":"Second"}]}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	root := t.TempDir()
	eng, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	const id, url = "PLfixture", "https://music.youtube.com/playlist?list=PLfixture"
	eng.m.Merge("playlist", id, "Playlist", url, []manifest.Entry{{ID: "one"}, {ID: "two"}})
	if err := eng.m.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	preview, err := eng.Inspect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SaveSourceMetadata(preview, url); err != nil {
		t.Fatal(err)
	}
	saved, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Pending([]string{id}); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("pending tracks after saving preview = %v, want [one two]", got)
	}
}

func TestSaveCollectionPreviewReusesLegacyAlbumSource(t *testing.T) {
	eng, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	const browseURL = "https://music.youtube.com/browse/MPREb_fixture"
	eng.m.Merge("album", "MPREb_fixture", "Album", browseURL, []manifest.Entry{{ID: "one"}})
	preview := &ytdlp.Listing{
		Type: "album", ID: "OLAK5uy_fixture", Title: "Album",
		Entries: []ytdlp.Entry{{ID: "one"}, {ID: "two"}},
	}
	id, err := eng.SaveSourceMetadata(preview, browseURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(eng.Snapshot()) != 1 || id != "MPREb_fixture" {
		t.Fatalf("saving album preview should reuse its legacy source; got %q and %d sources", id, len(eng.Snapshot()))
	}
	if got := eng.m.Pending([]string{id}); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("pending tracks in legacy album = %v, want [one two]", got)
	}
}

func stubCollectionBrowse(t *testing.T) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"music.youtube.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		fmt.Fprint(buf, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := buf.Flush(); err != nil {
			return
		}
		stream := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
		req, err := http.ReadRequest(bufio.NewReader(stream))
		if err != nil {
			return
		}
		body := `{}`
		switch req.URL.Path {
		case "/":
			body = `{"INNERTUBE_CLIENT_VERSION":"1.0.0"}`
		case "/youtubei/v1/browse":
			body = `{"header":{"musicDetailHeaderRenderer":{"title":{"runs":[{"text":"Playlist"}]}}},"contents":[{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"one"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First"}]}}}]}},{"continuationItemRenderer":{"continuationEndpoint":{"continuationCommand":{"token":"next"}}}}]}`
		}
		fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("SSL_CERT_FILE", ca)
}
