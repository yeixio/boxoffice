package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// A show's other episode orders are listed from TMDB, and choosing one
// gives each episode its place in it; the seasons and numbers the show
// has stay as aired, and going back to the aired order clears the places.
func TestSeriesOrders(t *testing.T) {
	db := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	fake.Raw["tv/95396/episode_groups"] = map[string]any{"results": []any{
		map[string]any{"id": "abs1", "name": "Absolute", "type": 2, "group_count": 1, "episode_count": 4},
		map[string]any{"id": "dvd1", "name": "DVD Order", "type": 3, "group_count": 3, "episode_count": 5},
		map[string]any{"id": "dvd0", "name": "Region 2", "type": 3, "group_count": 1, "episode_count": 2},
	}}
	ep := func(season, number, order int) map[string]any {
		return map[string]any{"season_number": season, "episode_number": number, "order": order}
	}
	// The discs put the first two episodes the other way round, and the
	// second season's opener at the end of the first set.
	fake.Raw["tv/episode_group/dvd1"] = map[string]any{"groups": []any{
		map[string]any{"order": 2, "episodes": []any{ep(2, 2, 0)}},
		map[string]any{"order": 0, "episodes": []any{ep(0, 1, 0)}},
		map[string]any{"order": 1, "episodes": []any{ep(2, 1, 2), ep(1, 1, 1), ep(1, 2, 0)}},
	}}
	cat := &catalog.Service{Store: db, Jobs: jobs.New(db, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	s := &Server{Store: db, Catalog: cat, Log: log, Version: "test", EnvAPIKey: "key"}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	ctx := context.Background()
	root := t.TempDir()
	if _, err := db.AddRootFolder(ctx, "series", root); err != nil {
		t.Fatal(err)
	}
	profiles, _ := db.Profiles(ctx)
	show, err := cat.AddSeries(ctx, 95396, catalog.AddOptions{ProfileID: profiles[0].ID, RootFolder: root, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/series/" + strconv.FormatInt(show.ID, 10)

	var orders []tmdb.Order
	if code := call(t, hs, "GET", path+"/orders", "", &orders); code != 200 || len(orders) != 3 {
		t.Fatalf("orders: %d %+v", code, orders)
	}
	if orders[0].ID != "abs1" || orders[1].ID != "dvd1" || orders[1].Kind != tmdb.OrderDVD || orders[1].Episodes != 5 || orders[2].ID != "dvd0" {
		t.Fatalf("orders aren't by kind, biggest first: %+v", orders)
	}

	for _, bad := range []string{`"dvd"`, `"order:"`, `"order:../x"`} {
		if code := call(t, hs, "PATCH", path, `{"numbering":`+bad+`}`, nil); code != 400 {
			t.Fatalf("numbering %s: %d", bad, code)
		}
	}
	// One TMDB doesn't have isn't kept.
	if code := call(t, hs, "PATCH", path, `{"numbering":"order:nope"}`, nil); code != 400 {
		t.Fatalf("an order TMDB doesn't have: %d", code)
	}
	if got, _ := db.Series(ctx, show.ID); got.Numbering != "" {
		t.Fatalf("kept %q", got.Numbering)
	}

	if code := call(t, hs, "PATCH", path, `{"numbering":"order:dvd1"}`, nil); code != 200 {
		t.Fatalf("choosing the DVD order: %d", code)
	}
	places := func() map[store.Place]store.Place {
		eps, err := db.Episodes(ctx, show.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[store.Place]store.Place{}
		for _, e := range eps {
			if e.Place != nil {
				out[store.Place{Season: e.Season, Number: e.Number}] = *e.Place
			}
		}
		return out
	}
	want := map[store.Place]store.Place{{Season: 0, Number: 1}: {Season: 0, Number: 1}, {Season: 1, Number: 2}: {Season: 1, Number: 1},
		{Season: 1, Number: 1}: {Season: 1, Number: 2}, {Season: 2, Number: 1}: {Season: 1, Number: 3}, {Season: 2, Number: 2}: {Season: 2, Number: 1}}
	got := places()
	if len(got) != len(want) {
		t.Fatalf("places: %+v", got)
	}
	for aired, p := range want {
		if got[aired] != p {
			t.Fatalf("S%02dE%02d is placed %+v, want %+v", aired.Season, aired.Number, got[aired], p)
		}
	}
	// A refresh keeps them.
	if err := cat.RefreshSeries(ctx, show.ID); err != nil {
		t.Fatal(err)
	}
	if got := places(); len(got) != len(want) {
		t.Fatalf("places after a refresh: %+v", got)
	}

	if code := call(t, hs, "PATCH", path, `{"numbering":""}`, nil); code != 200 {
		t.Fatalf("back to the aired order: %d", code)
	}
	if got := places(); len(got) != 0 {
		t.Fatalf("places left behind: %+v", got)
	}
}
