package downloader

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
	"github.com/yeixio/boxoffice/internal/usenet/yenc"
)

// postFiles posts files, by name as posted, and returns their NZB.
func postFiles(srv *nntptest.Server, title string, files [][2]any) []byte {
	var x bytes.Buffer
	fmt.Fprintf(&x, `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">%s</meta></head>`, title)
	for fi, f := range files {
		name, data := f[0].(string), f[1].([]byte)
		const chunk = 4000
		parts := (len(data) + chunk - 1) / chunk
		fmt.Fprintf(&x, `<file poster="t" date="1" subject="%s [%d/%d] - &quot;%s&quot; yEnc (1/%d)"><groups><group>a.b.test</group></groups><segments>`, title, fi+1, len(files), name, parts)
		for i := range parts {
			start, end := i*chunk, min((i+1)*chunk, len(data))
			id := fmt.Sprintf("%s.%d.%d@test", title, fi, i)
			body := yenc.Encode(name, i+1, parts, int64(start), int64(len(data)), data[start:end])
			srv.Add(id, body)
			fmt.Fprintf(&x, `<segment bytes="%d" number="%d">%s</segment>`, len(body), i+1, id)
		}
		x.WriteString(`</segments></file>`)
	}
	x.WriteString(`</nzb>`)
	return x.Bytes()
}

// A release posted the way Mike's were: RAR volumes under hashes, the
// first a .rar and the rest not named .r00, .r01…
func TestDownloadObfuscatedRAR(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	var files [][2]any
	for i, to := range []string{"39f5d2cb5d2e2f132f5f3091c5087f1c.rar", "77aa01bc", "e1e1e1e1e1.rar"} {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", fmt.Sprintf("multi.part%d.rar", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, [2]any{to, b})
	}
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, postFiles(srv, "Amerikatsi.2022", files))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	left := listFiles(filepath.Join(e.Dir, d.Folder))
	if len(left) != 2 || left[0] != "movie.mkv" {
		t.Fatalf("left %v", left)
	}
}

func windowsProgram() []byte {
	b := make([]byte, 600)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	return b
}

func TestDownloadQuarantined(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{Import: true})
	failed := make(chan string, 1)
	e.Failed = func(_ context.Context, _ store.Download, why string) { failed <- why }
	run(t, e)
	nzb := postFiles(srv, "Some.Movie.2024", [][2]any{{"Some.Movie.2024.mkv.exe", windowsProgram()}, {"readme.txt", []byte("codec needed")}})
	d, err := e.Add(context.Background(), store.Download{Source: "search", Kind: "movie", TitleID: 1}, nzb)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !strings.Contains(d.Message, "pretending to be a video") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	if why := <-failed; !strings.HasPrefix(why, "Quarantined") {
		t.Fatalf("failed hook heard %q", why)
	}
	if d.Quarantined != filepath.Join(e.Dir, QuarantineDir, d.Folder) {
		t.Fatalf("quarantined at %q", d.Quarantined)
	}
	info, err := os.Stat(filepath.Join(d.Quarantined, "Some.Movie.2024.mkv.exe"))
	if err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("quarantined file: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatal("still in the downloads folder")
	}
	// Removing it with its files leaves quarantine alone.
	if err := e.Remove(context.Background(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Quarantined); err != nil {
		t.Fatal("quarantine was deleted")
	}
}

func TestDownloadNoVideo(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	nzb := postFiles(srv, "Other.Movie.2024", [][2]any{{"other.movie.2024.nfo", []byte("visit our site")}})
	d, err := e.Add(context.Background(), store.Download{Source: "search", Kind: "movie", TitleID: 1}, nzb)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !strings.Contains(d.Message, "no video") || d.Quarantined == "" {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
}

type flakyImporter struct{ fail bool }

func (f *flakyImporter) Import(_ context.Context, _ store.Download, _ string, _ []string) (string, error) {
	if f.fail {
		return "", errors.New("the library is offline")
	}
	return "/library/Amerikatsi (2022)", nil
}

// A download that fails after it's fetched can be unpacked again without
// downloading it again.
func TestReprocess(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	var files [][2]any
	for i, to := range []string{"39f5d2cb5d2e2f132f5f3091c5087f1c.rar", "77aa01bc", "e1e1e1e1e1.rar"} {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", fmt.Sprintf("multi.part%d.rar", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, [2]any{to, b})
	}
	e, st := newEngine(t, srv)
	imp := &flakyImporter{fail: true}
	e.Importer = imp
	enable(t, st, Settings{Import: true})
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{Source: "search", Kind: "movie", TitleID: 1}, postFiles(srv, "Amerikatsi.2022", files))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !e.CanReprocess(d) {
		t.Fatalf("%s: %s (can reprocess: %v)", d.Status, d.Message, e.CanReprocess(d))
	}
	fetched := srv.Fetches.Load()
	imp.fail = false
	if err := e.Reprocess(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || d.ImportedTo == "" {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	if n := srv.Fetches.Load(); n != fetched {
		t.Fatalf("fetched %d more articles", n-fetched)
	}
	// Gone files can't be unpacked again.
	os.RemoveAll(filepath.Join(e.Dir, d.Folder))
	d.Status = store.DownloadFailed
	if e.CanReprocess(d) {
		t.Fatal("reprocessable with no files")
	}
}
