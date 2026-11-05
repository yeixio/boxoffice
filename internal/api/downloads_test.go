package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestBulkDownloads(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	var ids []int64
	for i := range 4 {
		d, err := s.Store.AddDownload(ctx, store.Download{Name: fmt.Sprintf("d%d", i), Status: store.DownloadQueued})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	order := func() string {
		q, _ := s.Store.DownloadQueue(ctx)
		var out []string
		for _, d := range q {
			out = append(out, d.Name+":"+d.Status)
		}
		return fmt.Sprint(out)
	}
	type answer struct {
		Done   int `json:"done"`
		Failed []struct {
			ID    int64  `json:"id"`
			Error string `json:"error"`
		} `json:"failed"`
	}
	bulk := func(body string) answer {
		t.Helper()
		var a answer
		if code := call(t, hs, "POST", "/api/v1/downloads/bulk", body, &a); code != 200 {
			t.Fatalf("%s: %d", body, code)
		}
		return a
	}
	// d2 and d3 go to the top, keeping their order.
	bulk(fmt.Sprintf(`{"action":"top","ids":[%d,%d]}`, ids[2], ids[3]))
	if got := order(); got != "[d2:queued d3:queued d0:queued d1:queued]" {
		t.Fatal(got)
	}
	bulk(fmt.Sprintf(`{"action":"bottom","ids":[%d]}`, ids[2]))
	if got := order(); got != "[d3:queued d0:queued d1:queued d2:queued]" {
		t.Fatal(got)
	}
	// One that can't be paused doesn't stop the rest.
	bulk(fmt.Sprintf(`{"action":"pause","ids":[%d]}`, ids[0]))
	a := bulk(fmt.Sprintf(`{"action":"pause","ids":[%d,%d,%d]}`, ids[0], ids[1], 9999))
	if a.Done != 1 || len(a.Failed) != 2 || a.Failed[1].Error != "it's gone" {
		t.Fatalf("%+v", a)
	}
	bulk(fmt.Sprintf(`{"action":"resume","ids":[%d,%d]}`, ids[0], ids[1]))
	bulk(fmt.Sprintf(`{"action":"remove","ids":[%d,%d]}`, ids[2], ids[3]))
	if got := order(); got != "[d0:queued d1:queued]" {
		t.Fatal(got)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/bulk", `{"action":"explode","ids":[1]}`, nil); code != 400 {
		t.Fatalf("unknown action: %d", code)
	}
}

func TestQuarantineBulk(t *testing.T) {
	s, hs, _ := monitorServer(t)
	dir := t.TempDir()
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, p := range []string{"_quarantine/bad one/x.exe", "_quarantine/worse/y.exe", "keep/z.mkv"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	var list struct {
		Items []downloader.QuarantineItem `json:"items"`
		Size  int64                       `json:"size"`
	}
	if code := call(t, hs, "GET", "/api/v1/downloads/quarantine", "", &list); code != 200 || len(list.Items) != 2 || list.Size != 2 {
		t.Fatalf("%d %+v", code, list)
	}
	var a struct {
		Done   int `json:"done"`
		Failed []struct {
			Path string `json:"path"`
		} `json:"failed"`
	}
	call(t, hs, "POST", "/api/v1/downloads/quarantine/delete", `{"paths":["_quarantine/bad one","keep","_quarantine/worse/y.exe"]}`, &a)
	if a.Done != 1 || len(a.Failed) != 2 {
		t.Fatalf("%+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep/z.mkv")); err != nil {
		t.Fatal("deleted something outside quarantine")
	}
	call(t, hs, "POST", "/api/v1/downloads/quarantine/release", `{"paths":["_quarantine/worse"]}`, &a)
	if _, err := os.Stat(filepath.Join(dir, "worse/y.exe")); a.Done != 1 || err != nil {
		t.Fatalf("release: %+v %v", a, err)
	}
}

// Removing a download can put its release on the blocklist, or not.
func TestRemoveDownloadBlock(t *testing.T) {
	s, hs, _ := monitorServer(t)
	s.Downloads = &downloader.Engine{Store: s.Store, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	add := func(name string) int64 {
		d, err := s.Store.AddDownload(ctx, store.Download{Name: name, Kind: "movie", TitleID: 7, Status: store.DownloadQueued})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	a, b, c := add("Keep.Looking"), add("Bad.Release"), add("Also.Bad")
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/downloads/%d", a), "", nil); code != 204 {
		t.Fatal(code)
	}
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/downloads/%d?block=true", b), "", nil); code != 204 {
		t.Fatal(code)
	}
	if code := call(t, hs, "POST", "/api/v1/downloads/bulk", fmt.Sprintf(`{"action":"remove","ids":[%d],"block":true}`, c), nil); code != 200 {
		t.Fatal(code)
	}
	list, err := s.Store.BlockedFor(ctx, "movie", 7)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range list {
		got = append(got, x.SourceTitle)
	}
	slices.Sort(got)
	if fmt.Sprint(got) != "[Also.Bad Bad.Release]" {
		t.Fatal(got)
	}
	// One being unpacked can't be removed.
	d := add("Busy")
	s.Store.SetDownloadStatus(ctx, d, store.DownloadUnpacking, "")
	if code := call(t, hs, "DELETE", fmt.Sprintf("/api/v1/downloads/%d", d), "", nil); code != 400 {
		t.Fatal(code)
	}
}
