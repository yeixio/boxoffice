package downloader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A read of the downloads folder that hangs, as on a share that dropped,
// gives up after diskWait, isn't started again while it's stuck, and
// makes other reads give up sooner until it ends.
func TestOnDiskGivesUpOnAHungShare(t *testing.T) {
	defer func(d, s time.Duration) { diskWait, stuckWait = d, s }(diskWait, stuckWait)
	diskWait, stuckWait = 100*time.Millisecond, 10*time.Millisecond
	e := &Engine{Dir: "/downloads"}
	ctx := context.Background()

	release := make(chan struct{})
	var started atomic.Int32
	hung := func(context.Context) (int, error) {
		started.Add(1)
		<-release
		return 7, nil
	}
	start := time.Now()
	if _, err := onDisk(e, ctx, "a", hung); err == nil || !strings.Contains(err.Error(), "isn't answering") {
		t.Fatalf("err = %v, want the folder isn't answering", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("waited %v on a hung share", took)
	}
	// Asking again waits on the same read rather than starting another,
	// and not the whole diskWait now that one is stuck.
	start = time.Now()
	if _, err := onDisk(e, ctx, "a", hung); err == nil {
		t.Fatal("a stuck read answered")
	}
	if took := time.Since(start); took >= diskWait {
		t.Fatalf("waited %v with a read already stuck", took)
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("started %d reads, want 1", n)
	}
	// Another read that's quick still answers.
	if v, err := onDisk(e, ctx, "b", func(context.Context) (string, error) { return "ok", nil }); err != nil || v != "ok" {
		t.Fatalf("quick read = %q, %v", v, err)
	}

	// Once the share answers, the stuck read ends and things are back to
	// normal.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.diskMu.Lock()
		stuck, left := e.diskStuck, len(e.disk)
		e.diskMu.Unlock()
		if stuck == 0 && left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %d stuck, %d under way", stuck, left)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if v, err := onDisk(e, ctx, "a", func(context.Context) (int, error) { return 8, nil }); err != nil || v != 8 {
		t.Fatalf("read after = %d, %v", v, err)
	}
}

func TestHasFiles(t *testing.T) {
	dir := t.TempDir()
	if hasFiles(dir) || hasFiles(filepath.Join(dir, "gone")) {
		t.Fatal("found files in an empty or missing folder")
	}
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", ".boxoffice-state"), []byte("x"), 0o644)
	if hasFiles(dir) {
		t.Fatal("counted Box Office's own file")
	}
	os.WriteFile(filepath.Join(dir, "a", "b", "movie.mkv"), []byte("x"), 0o644)
	if !hasFiles(dir) {
		t.Fatal("missed a file deep in the folder")
	}
}
