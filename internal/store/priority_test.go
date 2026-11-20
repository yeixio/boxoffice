package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestCleanOrder(t *testing.T) {
	if got := store.CleanOrder(nil); !slices.Equal(got, store.DefaultPriority) {
		t.Fatalf("no order: %v", got)
	}
	// Unknown and repeated groups go; missing ones come back at the end.
	got := store.CleanOrder([]string{store.GroupUpgrades, "nope", store.GroupNewMovies, store.GroupUpgrades})
	if got[0] != store.GroupUpgrades || len(got) != len(store.DefaultPriority) {
		t.Fatalf("got %v", got)
	}
	if got[1] != store.GroupNewMovies || got[2] != store.GroupNewEpisodes {
		t.Fatalf("got %v", got)
	}
}

// The queue follows the order as it's read: saving another re-sorts it,
// and downloads moved by hand keep their place until put back.
func TestDownloadQueueOrder(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	add := func(name, kind, reason string) store.Download {
		d, err := st.AddDownload(ctx, store.Download{Name: name, Kind: kind, Reason: reason})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	up := add("upgrade", "movie", store.ReasonUpgrade)
	old := add("old episode", "series", store.ReasonBacklog)
	book := add("book", store.Ebook, "")
	ep := add("new episode", "series", store.ReasonNew)
	mv := add("new movie", "movie", store.ReasonNew)
	other := add("by hand", "", "")
	names := func() []string {
		q, err := st.DownloadQueue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range q {
			out = append(out, d.Name)
		}
		return out
	}
	want := func(w ...string) {
		t.Helper()
		if got := names(); !slices.Equal(got, w) {
			t.Fatalf("queue %v, want %v", got, w)
		}
	}
	want(ep.Name, mv.Name, old.Name, book.Name, up.Name, other.Name)

	if err := st.SaveQueuePriority(ctx, store.QueuePriority{Order: []string{store.GroupUpgrades, store.GroupBooks}, NewDays: 7}); err != nil {
		t.Fatal(err)
	}
	want(up.Name, book.Name, ep.Name, mv.Name, old.Name, other.Name)

	if err := st.MoveDownloadToTop(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveDownloadToBottom(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	want(other.Name, book.Name, ep.Name, mv.Name, old.Name, up.Name)
	if err := st.SaveQueuePriority(ctx, store.QueuePriority{NewDays: 7}); err != nil {
		t.Fatal(err)
	}
	want(other.Name, ep.Name, mv.Name, old.Name, book.Name, up.Name)

	if err := st.UnpinDownload(ctx, up.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.UnpinDownload(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	want(ep.Name, mv.Name, old.Name, book.Name, up.Name, other.Name)

	p, err := st.QueuePriority(ctx)
	if err != nil || p.NewDays != 7 || p.Preempt {
		t.Fatalf("saved %+v, %v", p, err)
	}
}

func TestIsNew(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		when time.Time
		want bool
	}{
		{now.AddDate(0, 0, -3), true},
		{now.AddDate(0, 0, 2), true},
		{now.AddDate(0, 0, -20), false},
		{time.Time{}, false},
	} {
		if got := store.IsNew(c.when, 14, now); got != c.want {
			t.Errorf("IsNew(%v) = %v", c.when, got)
		}
	}
}
