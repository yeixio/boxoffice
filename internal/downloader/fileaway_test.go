package downloader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp/nntptest"
)

// An NZB added by hand to be filed is handed over once it's unpacked,
// even with moving downloads in by itself off, and its history says
// where it went, or why it's waiting.
func TestFileAway(t *testing.T) {
	srv := nntptest.New()
	defer srv.Close()
	data, want, _ := release(t, srv, false)
	e, st := newEngine(t, srv)
	enable(t, st, Settings{})
	type call struct{ src, name, kind string }
	calls := make(chan call, 3)
	answer := func(c call) (string, string, error) {
		switch c.kind {
		case "movie":
			return "/media/movies/Test Movie (2024)", "", nil
		case "auto":
			return "", "Box Office isn't sure what it is", nil
		}
		return "", "", errors.New("the library is full")
	}
	e.FileAway = func(ctx context.Context, src, name, kind string) (string, string, error) {
		c := call{src, name, kind}
		// It's handed the whole, unpacked folder.
		for f, b := range want {
			if got, err := os.ReadFile(filepath.Join(src, f)); err != nil || len(got) != len(b) {
				c.kind = "missing " + f
			}
		}
		calls <- c
		return answer(c)
	}
	run(t, e)
	ctx := context.Background()
	for _, c := range []struct{ fileAs, msg, to string }{
		{"movie", "Filed in /media/movies/Test Movie (2024)", "/media/movies/Test Movie (2024)"},
		{"auto", "waiting on Import files: Box Office isn't sure what it is", ""},
		{"series", "couldn't file it: the library is full", ""},
	} {
		d, err := e.Add(ctx, store.Download{Source: "upload", FileAs: c.fileAs}, data)
		if err != nil {
			t.Fatal(err)
		}
		if d.FileAs != c.fileAs {
			t.Fatalf("file as %q", d.FileAs)
		}
		d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
		var got call
		select {
		case got = <-calls:
		default:
			t.Fatalf("%s: not handed over: %s %q", c.fileAs, d.Status, d.Message)
		}
		if d.Status != store.DownloadCompleted || !strings.Contains(d.Message, c.msg) || d.ImportedTo != c.to {
			t.Fatalf("%s: %s %q to %q", c.fileAs, d.Status, d.Message, d.ImportedTo)
		}
		if got.kind != c.fileAs || got.name != d.Name || got.src != filepath.Join(e.Dir, d.Folder) {
			t.Fatalf("%s: handed %+v", c.fileAs, got)
		}
	}
	// One said to be what it isn't is screened as that: an ebook with no
	// ebook in it is quarantined, and not handed over.
	d, err := e.Add(ctx, store.Download{Source: "upload", FileAs: "ebook"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed); d.Quarantined == "" {
		t.Fatalf("not quarantined: %s %q", d.Status, d.Message)
	}
	// One added with nothing said stays in the downloads folder, as before.
	d, err = e.Add(ctx, store.Download{Source: "upload"}, data)
	if err != nil {
		t.Fatal(err)
	}
	d = waitFor(t, st, d.ID, store.DownloadCompleted, store.DownloadFailed)
	select {
	case c := <-calls:
		t.Fatalf("filed %+v", c)
	default:
	}
	checkFiles(t, filepath.Join(e.Dir, d.Folder), want)
}
