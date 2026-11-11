package downloader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

func lockedRAR(t *testing.T) []byte {
	b, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", "locked.rar"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A release locked with a password, and none given, is discarded as soon
// as its first archive volume is here: it's stopped, its files deleted,
// it's blocked and another looked for, and nobody is told.
func TestLockedDiscardedEarly(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	events := make(chan string, 10)
	e.Notify = func(_ context.Context, d store.Download, event, why string) { events <- event }
	failed := make(chan string, 1)
	e.Failed = func(_ context.Context, d store.Download, why string) { failed <- why }
	enable(t, st, Settings{DeleteFailed: true})
	run(t, e)
	// The rest of the release: 100 articles that never need fetching.
	data := postFiles(srv, "Locked", [][2]any{{"a1b2c3", lockedRAR(t)}, {"d4e5f6", gen(7, 400000)}})
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !d.Missing || !strings.Contains(d.Message, "password") || !strings.Contains(d.Message, "files were deleted") {
		t.Fatalf("%s (missing %v): %s", d.Status, d.Missing, d.Message)
	}
	if n := srv.Fetches.Load(); n > 50 {
		t.Fatalf("fetched %d of 101 articles before stopping", n)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatalf("its folder is still there: %v", err)
	}
	if why := <-failed; !strings.Contains(why, "password") {
		t.Fatalf("failed with %q", why)
	}
	nextEvent(t, events, store.EventGrabbed)
	select {
	case ev := <-events:
		t.Fatalf("told of a discarded download: %s", ev)
	default:
	}
}

// The password a release gives (here in its name) opens it, and one that
// doesn't is discarded when it's unpacked.
func TestLockedWithPassword(t *testing.T) {
	smallMedia(t)
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	enable(t, st, Settings{DeleteFailed: true})
	run(t, e)
	ctx := context.Background()
	d, err := e.Add(ctx, store.Download{Source: "upload", Name: "Locked{{secret}}"}, postFiles(srv, "Locked", [][2]any{{"a1b2c3", lockedRAR(t)}}))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Locked" || d.Password != "secret" {
		t.Fatalf("name %q, password %q", d.Name, d.Password)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("failed: %s", d.Message)
	}
	if b, _ := os.ReadFile(filepath.Join(e.Dir, d.Folder, "readme.txt")); string(b) != "hello from box office\n" {
		t.Fatalf("readme %q", b)
	}

	d, err = e.Add(ctx, store.Download{Source: "upload", Name: "Wrong{{nope}}"}, postFiles(srv, "Wrong", [][2]any{{"a1b2c3", lockedRAR(t)}}))
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadFailed || !d.Missing || !strings.Contains(d.Message, "doesn't open it") {
		t.Fatalf("%s (missing %v): %s", d.Status, d.Missing, d.Message)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder)); !os.IsNotExist(err) {
		t.Fatalf("its folder is still there: %v", err)
	}
}
