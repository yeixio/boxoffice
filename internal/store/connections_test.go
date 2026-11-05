package store_test

import (
	"context"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestMergeDuplicateDownloadClients(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for _, c := range []store.DownloadClient{
		{Name: "down", Kind: "nzbget", Protocol: "usenet", Host: "nzbget", Port: 6789, Category: "movies", Enabled: true, Source: "radarr", MissingSecret: true},
		{Name: "down", Kind: "nzbget", Protocol: "usenet", Host: "nzbget", Port: 6789, Category: "tv", Password: "pw", Enabled: true, Source: "sonarr"},
		{Name: "Deluge", Kind: "deluge", Protocol: "torrent", Host: "deluge", Port: 8112, Category: "radarr", Enabled: true},
	} {
		if err := st.UpsertDownloadClient(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MergeDuplicateDownloadClients(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := st.DownloadClients(ctx)
	if len(list) != 2 {
		t.Fatalf("%d clients: %+v", len(list), list)
	}
	for _, c := range list {
		if c.Kind == "nzbget" && (c.Category != "movies, tv" || c.Password != "pw" || c.MissingSecret) {
			t.Fatalf("merged %+v", c)
		}
	}
}

func TestDownloads(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	a, _ := st.AddDownload(ctx, store.Download{Name: "a", EpisodeIDs: []int64{3, 4}})
	b, _ := st.AddDownload(ctx, store.Download{Name: "b"})
	st.MoveDownloadToTop(ctx, b.ID)
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 2 || q[0].ID != b.ID || len(q[1].EpisodeIDs) != 2 {
		t.Fatalf("queue %+v", q)
	}
	st.SetDownloadStatus(ctx, a.ID, store.DownloadFailed, "gone")
	h, _ := st.DownloadHistory(ctx, 10)
	if len(h) != 1 || h[0].Message != "gone" || h[0].FinishedAt.IsZero() {
		t.Fatalf("history %+v", h)
	}
	st.ResetDownload(ctx, a.ID)
	if d, _ := st.Download(ctx, a.ID); d.Status != store.DownloadQueued || !d.FinishedAt.IsZero() {
		t.Fatalf("reset %+v", d)
	}
}
