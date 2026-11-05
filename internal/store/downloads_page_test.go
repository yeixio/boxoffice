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
	list, total, _, err := st.DownloadHistoryPage(ctx, "name", true, false, 0, 2)
	if err != nil || total != 5 || names(list) != "[Alpha bravo]" {
		t.Fatalf("%s %d %v", names(list), total, err)
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, "name", true, false, 4, 2)
	if names(list) != "[echo]" {
		t.Fatal(names(list))
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, "size", false, false, 0, 2)
	if names(list) != "[echo delta]" {
		t.Fatal(names(list))
	}
	list, _, _, _ = st.DownloadHistoryPage(ctx, "status", true, false, 0, 1)
	if names(list) != "[delta]" {
		t.Fatal(names(list))
	}
	// An unknown sort is newest first, never SQL.
	if _, _, _, err := st.DownloadHistoryPage(ctx, "id; DROP TABLE downloads", true, false, 0, 2); err != nil {
		t.Fatal(err)
	}
	// Failures for missing articles are hidden unless asked for.
	all, _ := st.DownloadHistory(ctx, 10)
	for _, d := range all {
		if d.Name == "delta" {
			st.SetDownloadMissing(ctx, d.ID)
		}
	}
	list, total, hidden, err := st.DownloadHistoryPage(ctx, "name", true, false, 0, 10)
	if err != nil || total != 4 || hidden != 1 || names(list) != "[Alpha bravo charlie echo]" {
		t.Fatalf("%s %d %d %v", names(list), total, hidden, err)
	}
	list, total, hidden, _ = st.DownloadHistoryPage(ctx, "name", true, true, 0, 10)
	if total != 5 || hidden != 0 || !list[3].Missing {
		t.Fatalf("%s %d %d", names(list), total, hidden)
	}
}
