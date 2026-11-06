package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// A database from before downloads.fetched was recorded gets it filled
// in: failures while still fetching aren't fetched, the rest are.
func TestBackfillFetched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ids := map[string]int64{}
	for name, how := range map[string][2]string{
		"done":       {store.DownloadCompleted, "Moved into the library"},
		"unpack":     {store.DownloadFailed, "Unpacking failed: CRC error"},
		"share":      {store.DownloadFailed, "Close /downloads/x/.boxoffice-1-a.part: host is down"},
		"servers":    {store.DownloadFailed, "The news servers stopped answering: timeout"},
		"incomplete": {store.DownloadFailed, "Too much of this release is gone from your news servers (25 MB missing) to repair"},
	} {
		d, err := st.AddDownload(ctx, store.Download{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		st.SetDownloadStatus(ctx, d.ID, how[0], how[1])
		ids[name] = d.ID
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"fetched", "missing"} {
		if _, err := db.Exec("ALTER TABLE downloads DROP COLUMN " + c); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for name, want := range map[string]bool{"done": true, "unpack": true, "share": false, "servers": false, "incomplete": false} {
		d, _ := st.Download(ctx, ids[name])
		if d.Fetched != want {
			t.Errorf("%s: fetched %v", name, d.Fetched)
		}
	}
	if d, _ := st.Download(ctx, ids["incomplete"]); !d.Missing {
		t.Error("incomplete isn't marked missing")
	}
}
