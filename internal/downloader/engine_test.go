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
	"sync"
	"sync/atomic"
	"syscall"
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
	name  string // as posted (may be obfuscated)
	data  []byte
	ids   []string
	sizes []int64
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
	srv := nntptest.New("u", "p")
	defer srv.Close()
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
	if !strings.Contains(d.Message, "Renamed 1 obfuscated file") {
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
	if d.Status != store.DownloadCompleted || !strings.Contains(d.Message, "Repaired") {
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
	events := make(chan string, 10)
	e.Notify = func(_ context.Context, d store.Download, event, why string) { events <- event }
	enable(t, st, Settings{DeleteFailed: true})
	run(t, e)
	d, _ := e.Add(context.Background(), store.Download{}, data)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !strings.Contains(d.Message, "files were deleted") || !d.Missing {
		t.Fatalf("%s (missing %v): %s", d.Status, d.Missing, d.Message)
	}
	// Past repair, so its files are gone.
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatalf("its folder is still there: %v", err)
	}
	// Gone from the news servers: nobody is told.
	nextEvent(t, events, store.EventGrabbed)
	// Try again, with the articles back.
	_, _, _ = release(t, srv, false)
	if err := e.Retry(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || d.Missing {
		t.Fatalf("retry: %s", d.Message)
	}
	// Added by hand, so it stays in the downloads folder.
	nextEvent(t, events, store.EventDownloaded)
}

func nextEvent(t *testing.T, events chan string, want string) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("event %s, want %s", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no %s event", want)
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

// A part file an earlier try left, which can't be opened again (as when a
// network share still has it locked), doesn't stop the download: each try
// writes part files of its own.
func TestStalePartFile(t *testing.T) {
	srv := nntptest.New("u", "p")
	defer srv.Close()
	data, want, _ := release(t, srv, true)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	d.Folder = "resumed"
	st.SaveDownloadProgress(ctx, d)
	for i := range 4 {
		// A folder in the way can't be opened as a file, nor cleared.
		os.MkdirAll(filepath.Join(e.Dir, "resumed", fmt.Sprintf(".boxoffice-%d.part", i)), 0o755)
	}
	run(t, e)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	for i := range 4 {
		os.Remove(filepath.Join(e.Dir, "resumed", fmt.Sprintf(".boxoffice-%d.part", i)))
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}

// While one download is unpacked, the next downloads; one that's done
// downloading meanwhile waits its turn to unpack, and can be removed then,
// but not once its turn comes.
func TestUnpackWaitsItsTurn(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	ctx := context.Background()
	e.init()
	e.post <- struct{}{} // something else is being unpacked
	run(t, e)
	a, _ := e.Add(ctx, store.Download{Name: "First"}, data)
	b, _ := e.Add(ctx, store.Download{Name: "Second"}, data)
	waitFor(t, st, a.ID, store.DownloadDownloaded)
	// The first waiting to unpack doesn't hold up the second.
	waitFor(t, st, b.ID, store.DownloadDownloaded)
	if err := e.Pause(ctx, b.ID); err == nil {
		t.Fatal("paused a download with nothing left to download")
	}
	if err := e.Remove(ctx, b.ID, true); err != nil {
		t.Fatalf("removing one waiting to unpack: %v", err)
	}
	<-e.post
	a = waitFor(t, st, a.ID, store.DownloadCompleted, store.DownloadFailed)
	if a.Status != store.DownloadCompleted {
		t.Fatalf("%s: %s", a.Status, a.Message)
	}
	if _, err := st.Download(ctx, b.ID); err == nil {
		t.Fatal("the removed download is back")
	}
}

func TestRemoveLockedWhileUnpacking(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{store.DownloadVerifying, store.DownloadRepairing, store.DownloadUnpacking, store.DownloadImporting} {
		st.SetDownloadStatus(ctx, d.ID, s, "")
		if err := e.Remove(ctx, d.ID, true); err == nil {
			t.Fatalf("removed while %s", s)
		}
	}
	st.SetDownloadStatus(ctx, d.ID, store.DownloadQueued, "")
	if err := e.Remove(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
}

// When the downloads folder (a network share) stops answering as a file is
// saved, the download waits and tries again, keeping what it finished;
// if it keeps happening, it fails saying so.
func TestShareDropsOut(t *testing.T) {
	for _, c := range []struct {
		name  string
		fails int
		want  string
	}{{"comes back", 2, store.DownloadCompleted}, {"stays down", 100, store.DownloadPaused}} {
		t.Run(c.name, func(t *testing.T) {
			srv := nntptest.New()
			defer srv.Close()
			data, want, _ := release(t, srv, false)
			e, st := newEngine(t, srv)
			enable(t, st, Settings{})
			var n atomic.Int32
			closePart, shareWait = func(f *os.File) error {
				err := f.Close()
				if n.Add(1) <= int32(c.fails) {
					return &os.PathError{Op: "close", Path: f.Name(), Err: syscall.EHOSTDOWN}
				}
				return err
			}, time.Millisecond
			t.Cleanup(func() { closePart, shareWait = (*os.File).Close, 15*time.Second })
			run(t, e)
			var failed atomic.Int32
			e.Failed = func(context.Context, store.Download, string) { failed.Add(1) }
			d, _ := e.Add(context.Background(), store.Download{}, data)
			d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed, store.DownloadPaused)
			if d.Status != c.want {
				t.Fatalf("%s: %s", d.Status, d.Message)
			}
			if c.want == store.DownloadCompleted {
				checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
				return
			}
			// The folder's trouble isn't the release's: it waits, paused,
			// and the release isn't blocked.
			if !strings.Contains(d.Message, "kept dropping out") || !strings.Contains(d.Message, "host is down") || d.Missing || failed.Load() != 0 {
				t.Fatalf("missing %v, failed %d: %s", d.Missing, failed.Load(), d.Message)
			}
			// Its files never all came, so there's nothing to unpack again.
			if d.Fetched || e.CanReprocess(d) {
				t.Fatal("offered to unpack again a download that never finished fetching")
			}
			// Tried again each time, then gave up (several files can be
			// closing at once, so each try can count more than one).
			if got := n.Load(); got < shareTries+1 {
				t.Fatalf("tried %d times", got)
			}
		})
	}
}

// While the downloads folder is away, the queue waits and says why; no
// download is paused for it. Downloads an older version paused for it go
// back in the queue once it answers.
func TestFolderAwayWaits(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	e.NZBDir = filepath.Join(t.TempDir(), "nzb")
	enable(t, st, Settings{})
	ctx := context.Background()
	// A share that dropped: reading the folder fails, and making it says
	// it exists.
	mkdirAll = func(p string, _ os.FileMode) error {
		return &os.PathError{Op: "mkdir", Path: p, Err: syscall.EEXIST}
	}
	t.Cleanup(func() { mkdirAll = os.MkdirAll })
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatalf("couldn't grab while the downloads folder was away: %v", err)
	}
	old, _ := e.Add(ctx, store.Download{}, data)
	st.SetDownloadStatus(ctx, old.ID, store.DownloadPaused, "Box Office can't make its downloads folder /downloads: mkdir /downloads: file exists")
	e.init()
	var wg sync.WaitGroup
	e.startNext(ctx, &wg)
	if d, _ = st.Download(ctx, d.ID); d.Status != store.DownloadQueued {
		t.Fatalf("download is %s: %s", d.Status, d.Message)
	}
	if !strings.Contains(e.Waiting(), "isn't answering") {
		t.Fatalf("waiting: %q", e.Waiting())
	}
	mkdirAll = os.MkdirAll
	run(t, e)
	for _, id := range []int64{d.ID, old.ID} {
		got := waitFor(t, st, id, store.DownloadCompleted, store.DownloadFailed)
		if got.Status != store.DownloadCompleted {
			t.Fatalf("%d: %s", id, got.Message)
		}
		checkFiles(t, filepath.Join(e.Dir, got.Folder), want)
	}
}

// A download that gets nowhere (a share that hung mid-write) is stopped
// and tried again, and doesn't hold up the queue for good.
func TestStalledDownload(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	var hung atomic.Bool
	block := make(chan struct{})
	closePart, shareWait, stallWait, stallGrace = func(f *os.File) error {
		if hung.CompareAndSwap(false, true) {
			<-block // the first close never comes back while the test runs
		}
		return f.Close()
	}, time.Millisecond, 200*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		close(block)
		closePart, shareWait, stallWait, stallGrace = (*os.File).Close, 15*time.Second, 10*time.Minute, time.Minute
	})
	run(t, e)
	d, _ := e.Add(context.Background(), store.Download{}, data)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed, store.DownloadPaused)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}

// A folder of the download's name that Box Office can't write in (made by
// another app as another user) is passed over for a fresh one; and when it
// can't write at all, the download waits, paused, and the release isn't
// blocked.
func TestFolderNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	ctx := context.Background()
	var failed atomic.Int32
	e.Failed = func(context.Context, store.Download, string) { failed.Add(1) }
	d, _ := e.Add(ctx, store.Download{}, data)
	d.Folder = "Made.By.NZBGet"
	os.Mkdir(filepath.Join(e.Dir, d.Folder), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(e.Dir, "Made.By.NZBGet"), 0o755) })
	st.SaveDownloadProgress(ctx, d)
	run(t, e)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed, store.DownloadPaused)
	if d.Status != store.DownloadCompleted || d.Folder == "Made.By.NZBGet" {
		t.Fatalf("%s in %s: %s", d.Status, d.Folder, d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	if failed.Load() != 0 {
		t.Fatal("blocked the release")
	}
}
