package downloader

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
	"github.com/yeixio/boxoffice/internal/usenet/yenc"
)

// restartable is an Engine on the same database, downloads folder and NZBs
// as e, as Box Office is after it restarts.
func restartable(e *Engine) *Engine {
	return &Engine{Store: e.Store, Dir: e.Dir, NZBDir: e.NZBDir, Log: e.Log}
}

// Files fetched before Box Office stopped aren't fetched again after it
// starts, even when the name they were saved as (from their posts) isn't
// the one in the NZB's subject, as with obfuscated releases.
func TestRestartKeepsFetchedFiles(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	// The subject names the movie one way, its posts another.
	data = bytes.Replace(data, []byte("&quot;movie.mkv&quot;"), []byte("&quot;a1b2c3d4e5f6&quot;"), 1)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 10}) // 10 KB/s: slow enough to stop midway
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	stopCtx, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { e.Run(stopCtx); close(stopped) }()
	d = waitFor(t, st, d.ID, store.DownloadDownloading)
	deadline := time.Now().Add(20 * time.Second)
	for {
		d, _ = st.Download(ctx, d.ID)
		if _, err := os.Stat(filepath.Join(e.Dir, d.Folder, "movie.mkv")); err == nil && d.Folder != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the movie was never fetched")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	<-stopped
	if d, _ = st.Download(ctx, d.ID); d.Status == store.DownloadCompleted {
		t.Skip("finished before it could be stopped")
	}
	before := srv.Fetches.Load()

	enable(t, st, Settings{})
	e2 := restartable(e)
	run(t, e2)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	// The movie is 13 articles; the rest of what's needed is 5.
	if n := srv.Fetches.Load() - before; n > 5 {
		t.Fatalf("%d articles fetched after the restart: the movie was fetched again", n)
	}
	if d.DoneBytes > d.Size+d.Size/10 {
		t.Fatalf("progress counted twice: %d of %d bytes", d.DoneBytes, d.Size)
	}
}

// A download that was fully fetched, and was being checked, unpacked or
// moved when Box Office stopped, carries on from there after it starts,
// without downloading anything again, even though unpacking already
// deleted some of its files.
func TestRestartAfterFetching(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	stopCtx, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { e.Run(stopCtx); close(stopped) }()
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	stop()
	<-stopped
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	// As if Box Office stopped while unpacking: the par2 files are gone.
	if err := st.SetDownloadStatus(ctx, d.ID, store.DownloadUnpacking, ""); err != nil {
		t.Fatal(err)
	}
	before := srv.Fetches.Load()
	run(t, restartable(e))
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	if n := srv.Fetches.Load() - before; n != 0 {
		t.Fatalf("%d articles fetched again", n)
	}
}

// Saving progress while other files finish doesn't read the list of
// finished files as it's written to: with a map, that stops Box Office
// dead, and the download starts over when it's back. (Run with -race.)
func TestProgressSavedWhileFilesFinish(t *testing.T) {
	old := progressEvery
	progressEvery = 0 // save on every file
	t.Cleanup(func() { progressEvery = old })
	srv := nntptest.New()
	defer srv.Close()
	var x bytes.Buffer
	x.WriteString(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">Many.Files</meta></head>`)
	want := map[string][]byte{}
	const files = 60
	for i := range files {
		name := fmt.Sprintf("f%02d.bin", i)
		data := gen(i+7, 500)
		want[name] = data
		id := fmt.Sprintf("m%d@test", i)
		body := yenc.Encode(name, 1, 1, 0, int64(len(data)), data)
		srv.Add(id, body)
		fmt.Fprintf(&x, `<file poster="t" date="1" subject="m [%d/%d] - &quot;%s&quot; yEnc (1/1)"><groups><group>a.b.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`,
			i+1, files, name, len(body), id)
	}
	x.WriteString(`</nzb>`)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{}, x.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	if len(d.DoneNames) != files {
		t.Fatalf("%d of %d names saved", len(d.DoneNames), files)
	}
}
