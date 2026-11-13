package downloader

import (
	"context"
	"math/bits"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// articlesKept counts the articles a paused download kept in its part
// files.
func articlesKept(d store.Download) int {
	n := 0
	for _, p := range d.Partial {
		for _, b := range p.Segs {
			n += bits.OnesCount8(b)
		}
	}
	return n
}

// waitFetches waits for the news server to have handed out n articles.
func waitFetches(t *testing.T, srv *nntptest.Server, n int32) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for srv.Fetches.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d articles fetched", srv.Fetches.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A download paused part-way through a file carries on from the articles
// it already saved when it's resumed, not from the start of the file.
func TestResumeKeepsArticles(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 10}) // 10 KB/s: slow enough to pause midway
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, st, d.ID, store.DownloadDownloading)
	waitFetches(t, srv, 5) // part-way through the movie's 13 articles
	if err := e.Pause(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadPaused)
	if d, _ = st.Download(ctx, d.ID); d.Status == store.DownloadCompleted {
		t.Skip("finished before it could be paused")
	}
	kept := articlesKept(d)
	if kept == 0 {
		t.Fatal("pausing kept nothing of the file it was part-way through")
	}
	for _, p := range d.Partial {
		if _, err := os.Stat(filepath.Join(e.Dir, d.Folder, p.Part)); err != nil {
			t.Fatalf("the part file kept is gone: %v", err)
		}
	}
	if d.DoneBytes == 0 {
		t.Fatal("pausing lost the progress")
	}
	before := srv.Fetches.Load()

	enable(t, st, Settings{})
	if err := e.Resume(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
	// The release is 18 articles, but for the par2 volumes held back.
	if n := int(srv.Fetches.Load() - before); n > 18-kept {
		t.Fatalf("%d articles fetched after resuming, though %d were kept: it started over", n, kept)
	}
	if len(d.Partial) != 0 {
		t.Fatalf("part files still recorded once it's done: %v", d.Partial)
	}
}

// A download's kept part file that's gone (or damaged) by the time it's
// resumed is just fetched again.
func TestResumeWithoutPartFile(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 10})
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, st, d.ID, store.DownloadDownloading)
	waitFetches(t, srv, 5)
	if err := e.Pause(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadPaused)
	if len(d.Partial) == 0 {
		t.Skip("nothing kept")
	}
	for _, p := range d.Partial {
		os.Remove(filepath.Join(e.Dir, d.Folder, p.Part))
	}
	enable(t, st, Settings{})
	e.Resume(ctx, d.ID)
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}

// Moving a download to the top while another downloads has that one make
// way: it waits, keeping what it fetched, while the one moved up
// downloads, and carries on by itself once that's done.
func TestMoveToTopMakesWay(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 10})
	run(t, e)
	ctx := context.Background()
	a, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Add(ctx, store.Download{}, data)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, st, a.ID, store.DownloadDownloading)
	waitFetches(t, srv, 5)
	if err := e.MoveToTop(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, st, b.ID, store.DownloadDownloading)
	a, _ = st.Download(ctx, a.ID)
	if a.Status != store.DownloadQueued {
		t.Fatalf("the download that made way is %s, want queued", a.Status)
	}
	if a.Message == "" || a.DoneBytes == 0 || articlesKept(a) == 0 {
		t.Fatalf("the download that made way lost its progress or says nothing: %d bytes, %d articles kept, %q", a.DoneBytes, articlesKept(a), a.Message)
	}
	enable(t, st, Settings{})
	b = waitFor(t, st, b.ID, store.DownloadCompleted, store.DownloadFailed)
	if b.Status != store.DownloadCompleted {
		t.Fatalf("the one moved to the top failed: %s", b.Message)
	}
	a = waitFor(t, st, a.ID, store.DownloadCompleted, store.DownloadFailed)
	if a.Status != store.DownloadCompleted {
		t.Fatalf("the one that made way failed: %s", a.Message)
	}
	if !a.FinishedAt.After(b.FinishedAt) && !a.FinishedAt.Equal(b.FinishedAt) {
		t.Fatal("the one that made way finished first")
	}
	checkFiles(t, filepath.Join(e.Dir, a.Folder), want)
	checkFiles(t, filepath.Join(e.Dir, b.Folder), want)
}

// A download moved to the top doesn't stop another moved to the top: they
// go in turn.
func TestMoveToTopDoesntStopForced(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, _, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{SpeedLimit: 10})
	ctx := context.Background()
	var ds []store.Download
	for range 3 {
		d, err := e.Add(ctx, store.Download{}, data)
		if err != nil {
			t.Fatal(err)
		}
		ds = append(ds, d)
	}
	run(t, e)
	waitFor(t, st, ds[0].ID, store.DownloadDownloading)
	e.MoveToTop(ctx, ds[1].ID)
	waitFor(t, st, ds[1].ID, store.DownloadDownloading)
	e.MoveToTop(ctx, ds[2].ID)
	time.Sleep(500 * time.Millisecond)
	if d, _ := st.Download(ctx, ds[1].ID); d.Status != store.DownloadDownloading {
		t.Fatalf("the first moved to the top is %s: it made way for the second", d.Status)
	}
}
