package downloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
	"github.com/yeixio/boxoffice/internal/usenet/yenc"
)

// gen is the par2 package's fixture generator: the files its testdata par2
// set protects.
func gen(n, size int) []byte {
	x := uint32(uint64(n) * 2654435761)
	out := make([]byte, size)
	for i := range out {
		x = x*1103515245 + 12345
		out[i] = byte(x >> 16)
	}
	return out
}

type post struct {
	name   string // as posted (may be obfuscated)
	data   []byte
	ids    []string
	sizes  []int64
}

// release posts the par2 fixture set to a fake news server, in 4000-byte
// articles, and returns its NZB.
func release(t *testing.T, srv *nntptest.Server, obfuscate bool) ([]byte, map[string][]byte, []post) {
	want := map[string][]byte{}
	var posts []post
	for i, f := range []struct {
		name string
		size int
	}{{"movie.mkv", 50000}, {"sample.mkv", 9000}, {"info.nfo", 300}} {
		data := gen(i+1, f.size)
		want[f.name] = data
		name := f.name
		if obfuscate && name == "movie.mkv" {
			name = "q9w8e7r6t5y4"
		}
		posts = append(posts, post{name: name, data: data})
	}
	for _, p := range []string{"set.par2", "set.vol0+4.par2", "set.vol4+5.par2"} {
		b, err := os.ReadFile(filepath.Join("..", "usenet", "par2", "testdata", p))
		if err != nil {
			t.Fatal(err)
		}
		posts = append(posts, post{name: p, data: b})
	}
	var x bytes.Buffer
	x.WriteString(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">Test.Movie.2024.1080p.WEB-DL</meta></head>`)
	for fi := range posts {
		p := &posts[fi]
		const chunk = 4000
		parts := (len(p.data) + chunk - 1) / chunk
		fmt.Fprintf(&x, `<file poster="t" date="1" subject="test [%d/%d] - &quot;%s&quot; yEnc (1/%d)"><groups><group>a.b.test</group></groups><segments>`, fi+1, len(posts), p.name, parts)
		for i := range parts {
			start, end := i*chunk, min((i+1)*chunk, len(p.data))
			id := fmt.Sprintf("%d.%d@test", fi, i)
			body := yenc.Encode(p.name, i+1, parts, int64(start), int64(len(p.data)), p.data[start:end])
			srv.Add(id, body)
			p.ids = append(p.ids, id)
			fmt.Fprintf(&x, `<segment bytes="%d" number="%d">%s</segment>`, len(body), i+1, id)
		}
		x.WriteString(`</segments></file>`)
	}
	x.WriteString(`</nzb>`)
	return x.Bytes(), want, posts
}

func newEngine(t *testing.T, srv *nntptest.Server) (*Engine, *store.Store) {
	st := storetest.Open(t)
	ctx := context.Background()
	if _, err := st.CreateUsenetServer(ctx, store.UsenetServer{Name: "fake", Host: srv.Host(), Port: srv.Port(), Connections: 4, Enabled: true,
		Username: srv.Username, Password: srv.Password}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: st, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return e, st
}

func waitFor(t *testing.T, st *store.Store, id int64, statuses ...string) store.Download {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		d, err := st.Download(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range statuses {
			if d.Status == s {
				return d
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	d, _ := st.Download(context.Background(), id)
	t.Fatalf("download is %s (%s), want %v", d.Status, d.Message, statuses)
	return d
}

func run(t *testing.T, e *Engine) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestOffByDefault(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if d, _ = st.Download(context.Background(), d.ID); d.Status != store.DownloadQueued || srv.Fetches.Load() != 0 {
		t.Fatalf("downloading while off: %s, %d fetches", d.Status, srv.Fetches.Load())
	}
}

func enable(t *testing.T, st *store.Store, s Settings) {
	s.Enabled = true
	if err := SaveSettings(context.Background(), st, s); err != nil {
		t.Fatal(err)
	}
}

func checkFiles(t *testing.T, dir string, want map[string][]byte) {
	t.Helper()
	for name, data := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(got))
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".par2") || strings.HasPrefix(e.Name(), ".boxoffice-") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func TestDownloadWhole(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	srv.Username, srv.Password = "u", "p"
	data, want, _ := release(t, srv, true)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Test.Movie.2024.1080p.WEB-DL" {
		t.Fatalf("name %q", d.Name)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	if !strings.Contains(d.Message, "renamed 1 obfuscated file") {
		t.Fatalf("message %q", d.Message)
	}
	// The recovery volumes weren't needed, so they weren't fetched.
	if n := srv.Fetches.Load(); n != 13+3+1+1 {
		t.Fatalf("%d articles fetched", n)
	}
	if srv.MaxConns.Load() > 4 {
		t.Fatalf("%d connections at once, over the server's 4", srv.MaxConns.Load())
	}
}

func TestDownloadRepair(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, posts := release(t, srv, false)
	// Two articles of the movie expired, and the nfo is gone completely.
	srv.Remove(posts[0].ids[3])
	srv.Remove(posts[0].ids[8])
	srv.Remove(posts[2].ids[0])
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, _ := e.Add(context.Background(), store.Download{}, data)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || !strings.Contains(d.Message, "repaired") {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}

func TestDownloadTooBroken(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, posts := release(t, srv, false)
	for _, id := range posts[0].ids {
		srv.Remove(id)
	}
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, _ := e.Add(context.Background(), store.Download{}, data)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	// Try again, with the articles back.
	_, _, _ = release(t, srv, false)
	if err := e.Retry(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("retry: %s", d.Message)
	}
}

func TestPauseResumeRemove(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 20}) // 20 KB/s: slow enough to catch
	run(t, e)
	ctx := context.Background()
	d, _ := e.Add(ctx, store.Download{}, data)
	waitFor(t, st, d.ID, store.DownloadDownloading)
	if err := e.Pause(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadPaused)
	enable(t, st, Settings{})
	if err := e.Resume(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	if err := e.Remove(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatal("files are still there")
	}
}

func TestFolderInsideLibrary(t *testing.T) {
	e := &Engine{Dir: "/data/media/movies/downloads", Libraries: func(context.Context) ([]string, error) { return []string{"/data/media/movies"}, nil }}
	if err := e.CheckFolder(context.Background()); err == nil {
		t.Fatal("a downloads folder inside the library passed")
	}
}
