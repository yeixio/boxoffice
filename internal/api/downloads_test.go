package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
