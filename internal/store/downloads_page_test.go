package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestDownloadHistoryPage(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for i, name := range []string{"bravo", "Alpha", "charlie", "delta", "echo"} {
		d, err := st.AddDownload(ctx, store.Download{Name: name, Status: store.DownloadQueued, Size: int64(100 * (i + 1))})
		if err != nil {
			t.Fatal(err)
		}
		status := store.DownloadCompleted
		if name == "delta" {
			status = store.DownloadFailed
		}
		st.SetDownloadStatus(ctx, d.ID, status, "")
	}
	names := func(list []store.Download) string {
		var out []string
		for _, d := range list {
			out = append(out, d.Name)
		}
		return fmt.Sprint(out)
	}
	list, total, _, err := st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "name", Asc: true, Missing: false, Offset: 0, Limit: 2})
	if err != nil || total != 5 || names(list) != "[Alpha bravo]" {
		t.Fatalf("%s %d %v", names(list), total, err)
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "name", Asc: true, Missing: false, Offset: 4, Limit: 2})
	if names(list) != "[echo]" {
		t.Fatal(names(list))
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "size", Asc: false, Missing: false, Offset: 0, Limit: 2})
	if names(list) != "[echo delta]" {
		t.Fatal(names(list))
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "status", Asc: true, Missing: false, Offset: 0, Limit: 1})
	if names(list) != "[delta]" {
		t.Fatal(names(list))
	}
	// An unknown sort is newest first, never SQL.
	if _, _, _, err := st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "id; DROP TABLE downloads", Asc: true, Missing: false, Offset: 0, Limit: 2}); err != nil {
		t.Fatal(err)
	}
	// Failures for missing articles are hidden unless asked for.
	all, _ := st.DownloadHistory(ctx, 10)
	for _, d := range all {
		if d.Name == "delta" {
			st.SetDownloadMissing(ctx, d.ID)
		}
	}
	list, total, hidden, err := st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "name", Asc: true, Missing: false, Offset: 0, Limit: 10})
	if err != nil || total != 4 || hidden != 1 || names(list) != "[Alpha bravo charlie echo]" {
		t.Fatalf("%s %d %d %v", names(list), total, hidden, err)
	}
	list, total, hidden, _ = st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "name", Asc: true, Missing: true, Offset: 0, Limit: 10})
	if total != 5 || hidden != 0 || !list[3].Missing {
		t.Fatalf("%s %d %d", names(list), total, hidden)
	}
	// A search keeps those with every word, however the name spells
	// its spaces; % and _ are just characters.
	for _, name := range []string{"Bobs.Burgers.S01E08.DVDRip", "Bobs_Burgers_S02E01", "Other.Show.S01E08", "100%_Pure"} {
		d, _ := st.AddDownload(ctx, store.Download{Name: name})
		st.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, "")
	}
	for q, want := range map[string]string{
		"bobs burgers":   "[Bobs.Burgers.S01E08.DVDRip Bobs_Burgers_S02E01]",
		"Burgers s01e08": "[Bobs.Burgers.S01E08.DVDRip]",
		"s01e08":         "[Bobs.Burgers.S01E08.DVDRip Other.Show.S01E08]",
		"100%":           "[100%_Pure]",
		"0%":             "[100%_Pure]",
		"%":              "[100%_Pure]",
	} {
		list, _, _, err := st.DownloadHistoryPage(ctx, store.HistoryPage{Sort: "name", Asc: true, Query: q, Limit: 20})
		got := names(list)
		if err != nil || got != want {
			t.Errorf("%q: %s %v", q, got, err)
		}
	}
}
