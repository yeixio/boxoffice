package downloader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// waitingFor waits until the queue says why it's waiting, in words that
// hold want.
func waitingFor(t *testing.T, e *Engine, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if why := e.Waiting(); strings.Contains(why, want) {
			return why
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the queue says %q, want something with %q", e.Waiting(), want)
	return ""
}

// A download doesn't start when it would leave less free than Keep free
// says: the queue waits and says why, nothing fails, and it carries on by
// itself once there's room.
func TestWaitsForRoomToDownload(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	var free atomic.Int64
	free.Store(1_000_001_000) // a little over what's kept free
	e.Free = func(string) (int64, bool) { return free.Load(), true }
	enable(t, st, Settings{KeepFree: 1})
	run(t, e)
	d, err := e.Add(context.Background(), store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	why := waitingFor(t, e, "free")
	if !strings.Contains(why, d.Name) || !strings.Contains(why, "1.0 GB kept free") {
		t.Fatalf("why: %s", why)
	}
	if n := srv.Fetches.Load(); n != 0 {
		t.Fatalf("fetched %d articles with no room", n)
	}
	if e.RoomWait() != why {
		t.Fatalf("Health isn't told: %q", e.RoomWait())
	}
	if cur, _ := st.Download(context.Background(), d.ID); cur.Status != store.DownloadQueued {
		t.Fatalf("it's %s: %s", cur.Status, cur.Message)
	}
	free.Store(50 << 30)
	e.Wake()
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted || e.Waiting() != "" || e.RoomWait() != "" {
		t.Fatalf("%s: %s (queue: %s)", d.Status, d.Message, e.Waiting())
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}

// An archive isn't unpacked when what it holds wouldn't fit: the download
// goes back in the queue with what it fetched, the queue waits, and it's
// unpacked once there's room, without fetching it again.
func TestWaitsForRoomToUnpack(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	e, st := newEngine(t, srv)
	zip, err := os.ReadFile(filepath.Join("..", "usenet", "unpack", "testdata", "plain.zip"))
	if err != nil {
		t.Fatal(err)
	}
	// Room to download it, but not to unpack it, until the test says so.
	var room atomic.Bool
	ctx := context.Background()
	e.Free = func(string) (int64, bool) {
		if room.Load() {
			return 50 << 30, true
		}
		if q, _ := st.DownloadQueue(ctx); len(q) > 0 && q[0].Fetched {
			return 1_000_000_000, true
		}
		return 50 << 30, true
	}
	failed := make(chan string, 1)
	e.Failed = func(_ context.Context, d store.Download, why string) { failed <- why }
	enable(t, st, Settings{KeepFree: 1})
	run(t, e)
	d, err := e.Add(ctx, store.Download{Source: "upload"}, postFiles(srv, "Zipped", [][2]any{{"Zipped.zip", zip}}))
	if err != nil {
		t.Fatal(err)
	}
	waitingFor(t, e, "free")
	fetched := srv.Fetches.Load()
	cur, _ := st.Download(ctx, d.ID)
	if cur.Status != store.DownloadQueued || !cur.Fetched {
		t.Fatalf("it's %s (fetched %v): %s", cur.Status, cur.Fetched, cur.Message)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, cur.Folder, "readme.txt")); err == nil {
		t.Fatal("it was unpacked with no room")
	}
	room.Store(true)
	e.Wake()
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	if d.Status != store.DownloadCompleted {
		t.Fatalf("%s: %s", d.Status, d.Message)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, d.Folder, "readme.txt")); err != nil {
		t.Fatalf("not unpacked: %v (%v; %s)", err, listFiles(filepath.Join(e.Dir, d.Folder)), d.Message)
	}
	if n := srv.Fetches.Load(); n != fetched {
		t.Fatalf("fetched %d more articles to unpack it", n-fetched)
	}
	select {
	case why := <-failed:
		t.Fatalf("it was failed: %s", why)
	default:
	}
}

func TestDiskFullIsRoomTrouble(t *testing.T) {
	err := roomTrouble(&os.PathError{Op: "write", Path: "/downloads/x", Err: syscall.ENOSPC})
	if _, ok := err.(roomError); !ok {
		t.Fatalf("a full disk is %T", err)
	}
	if err := roomTrouble(os.ErrPermission); err != os.ErrPermission {
		t.Fatalf("no permission became %v", err)
	}
}
