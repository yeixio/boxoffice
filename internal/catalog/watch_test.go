package catalog_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func day(days int) *time.Time {
	t := time.Now().AddDate(0, 0, days)
	return &t
}

func watched(m map[int]bool) string {
	s := ""
	for n := 0; n <= 20; n++ {
		if on, ok := m[n]; ok {
			if on {
				s += fmt.Sprint(n)
			} else {
				s += "-"
			}
		}
	}
	return s
}

func TestWatchedSeasons(t *testing.T) {
	now := time.Now()
	// Specials, eight aired seasons, one with no date yet, and one coming.
	long := []tmdb.SeasonInfo{{Number: 0, AirDate: day(-4000)}}
	for n := 1; n <= 8; n++ {
		long = append(long, tmdb.SeasonInfo{Number: n, AirDate: day(-400 * (9 - n))})
	}
	long = append(long, tmdb.SeasonInfo{Number: 9}, tmdb.SeasonInfo{Number: 10, AirDate: day(30)})
	short := []tmdb.SeasonInfo{{Number: 0}, {Number: 1, AirDate: day(-700)}, {Number: 2, AirDate: day(-300)}, {Number: 3, AirDate: day(-3)}}
	for _, c := range []struct {
		seasons []tmdb.SeasonInfo
		monitor string
		n       int
		want    string
	}{
		{long, catalog.MonitorRecent, 3, "------678910"},
		{long, catalog.MonitorRecent, 1, "--------8910"},
		{long, catalog.MonitorRecent, 20, "-12345678910"},
		{long, catalog.MonitorAll, 3, "-12345678910"},
		{long, catalog.MonitorFuture, 3, "-12345678910"},
		{long, catalog.MonitorLatest, 3, "----------10"},
		{long, catalog.MonitorNone, 3, "-----------"},
		{short, catalog.MonitorRecent, 3, "-123"},
		{short, catalog.MonitorRecent, 2, "--23"},
	} {
		if got := watched(catalog.WatchedSeasons(c.seasons, c.monitor, c.n, now)); got != c.want {
			t.Errorf("%s %d: got %s, want %s", c.monitor, c.n, got, c.want)
		}
	}
}

// A long show added without a choice watches the default: the last three
// seasons. Settings change the default; the request can still pick.
func TestAddLongShowWatchesRecentSeasons(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	seasons := []any{map[string]any{"season_number": 0, "episode_count": 1, "air_date": "2001-01-01"}}
	for n := 1; n <= 6; n++ {
		seasons = append(seasons, map[string]any{"season_number": n, "episode_count": 1, "air_date": fmt.Sprintf("%d-09-01", 2000+n)})
		fake.Episodes[fmt.Sprintf("777/%d", n)] = []map[string]any{{"season_number": n, "episode_number": 1, "name": "One", "air_date": fmt.Sprintf("%d-09-01", 2000+n)}}
	}
	fake.Shows[777] = map[string]any{"id": 777, "name": "Long Runner", "first_air_date": "2001-09-01", "seasons": seasons}
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	root := filepath.Join(t.TempDir(), "tv")
	os.MkdirAll(root, 0o755)
	if _, err := st.AddRootFolder(ctx, "series", root); err != nil {
		t.Fatal(err)
	}
	on := func(s store.Series) string {
		m := map[int]bool{}
		for _, se := range s.Seasons {
			m[se.Number] = se.Monitored
		}
		return watched(m)
	}
	add := func(o catalog.AddOptions) store.Series {
		t.Helper()
		o.Monitored = true
		s, err := cat.AddSeries(ctx, 777, o)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.DeleteSeries(ctx, s.ID); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := on(add(catalog.AddOptions{})); got != "----456" {
		t.Fatalf("default: %s", got)
	}
	if got := on(add(catalog.AddOptions{Monitor: "recent", Recent: 2})); got != "-----56" {
		t.Fatalf("recent 2: %s", got)
	}
	if got := on(add(catalog.AddOptions{Monitor: "all"})); got != "-123456" {
		t.Fatalf("all: %s", got)
	}
	if err := catalog.SaveShowDefaults(ctx, st, catalog.ShowDefaults{Monitor: "recent", Recent: 4}); err != nil {
		t.Fatal(err)
	}
	if got := on(add(catalog.AddOptions{})); got != "---3456" {
		t.Fatalf("default 4: %s", got)
	}
	if got := on(add(catalog.AddOptions{Monitor: "recent"})); got != "---3456" {
		t.Fatalf("recent, default count: %s", got)
	}
	if err := catalog.SaveShowDefaults(ctx, st, catalog.ShowDefaults{Monitor: "all", Recent: 4}); err != nil {
		t.Fatal(err)
	}
	if got := on(add(catalog.AddOptions{})); got != "-123456" {
		t.Fatalf("default all: %s", got)
	}
	if _, err := cat.AddSeries(ctx, 777, catalog.AddOptions{Monitor: "most"}); err == nil {
		t.Fatal("an unknown rule was taken")
	}
	if err := catalog.SaveShowDefaults(ctx, st, catalog.ShowDefaults{Monitor: "recent", Recent: 0}); err == nil {
		t.Fatal("zero seasons was taken")
	}
	if d, _ := catalog.LoadShowDefaults(ctx, st); d.Monitor != "all" || d.Recent != 4 {
		t.Fatalf("defaults: %+v", d)
	}
}
