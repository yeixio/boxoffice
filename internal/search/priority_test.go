package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

func TestFirstCopyReason(t *testing.T) {
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) { return 200, feedOf() })
	_, st, m := setup(t, srv.URL)
	ctx := context.Background()
	now := time.Now()
	recent := now.AddDate(0, 0, -3)
	newM, _ := st.AddMovie(ctx, store.Movie{TMDBID: 700, Title: "New", Year: 2026, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/New", Monitored: true, DigitalRelease: &recent})
	reason := func(d store.Download) string { return firstCopyReason(ctx, st, d, 30, now) }
	if r := reason(store.Download{Kind: "movie", TitleID: newM.ID}); r != store.ReasonNew {
		t.Errorf("new movie: %s", r)
	}
	if r := reason(store.Download{Kind: "movie", TitleID: m.ID}); r != store.ReasonBacklog {
		t.Errorf("old movie: %s", r)
	}
	if r := reason(store.Download{Kind: "movie", TitleID: newM.ID, Mode: ModeSecond}); r != store.ReasonSecond {
		t.Errorf("second copy: %s", r)
	}
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: newM.ID, Path: "/m/New/x.mkv", Quality: 9})
	if r := reason(store.Download{Kind: "movie", TitleID: newM.ID}); r != store.ReasonUpgrade {
		t.Errorf("movie with a copy: %s", r)
	}
	if r := reason(store.Download{Kind: store.Ebook, TitleID: 1}); r != store.ReasonBacklog {
		t.Errorf("book: %s", r)
	}
}
