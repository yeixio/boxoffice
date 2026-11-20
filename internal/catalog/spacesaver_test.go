package catalog_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// A show with space saver on fetches only the next few episodes past the
// latest one anyone has watched, and more as they watch. Seasons and
// episodes switched off aren't counted, the switches are left alone, and
// turning it off lets everything go.
func TestSpaceSaver(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	// Specials and two seasons of four, all out, then one episode to come.
	seasons := []any{map[string]any{"season_number": 0, "episode_count": 1, "air_date": "2001-01-01"}}
	fake.Episodes["778/0"] = []map[string]any{{"season_number": 0, "episode_number": 1, "name": "Extra", "air_date": "2001-01-01"}}
	for n := 1; n <= 2; n++ {
		seasons = append(seasons, map[string]any{"season_number": n, "episode_count": 4, "air_date": fmt.Sprintf("%d-09-01", 2000+n)})
		var eps []map[string]any
		for e := 1; e <= 4; e++ {
			eps = append(eps, map[string]any{"season_number": n, "episode_number": e, "name": fmt.Sprint(e), "air_date": fmt.Sprintf("%d-09-%02d", 2000+n, e)})
		}
		if n == 2 {
			eps = append(eps, map[string]any{"season_number": 2, "episode_number": 5, "name": "5", "air_date": "2999-01-01"})
		}
		fake.Episodes[fmt.Sprintf("778/%d", n)] = eps
	}
	fake.Shows[778] = map[string]any{"id": 778, "name": "Slow Burn", "first_air_date": "2001-09-01", "seasons": seasons}
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	root := filepath.Join(t.TempDir(), "tv")
	os.MkdirAll(root, 0o755)
	if _, err := st.AddRootFolder(ctx, "series", root); err != nil {
		t.Fatal(err)
	}
	// held draws the regular episodes in order: h for held back, . for not.
	held := func(id int64) string {
		t.Helper()
		eps, err := st.Episodes(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		s := ""
		for _, e := range eps {
			switch {
			case e.Season == 0 && e.Held:
				t.Fatal("a special is held back")
			case e.Season == 0:
			case e.Held:
				s += "h"
			default:
				s += "."
			}
			if e.Number == 4 && e.Season == 1 {
				s += " "
			}
		}
		return s
	}
	watch := func(id int64, season, episode int) {
		t.Helper()
		rows := []store.Watched{}
		if season > 0 {
			rows = append(rows, store.Watched{TitleID: id, Viewers: 1, FurthestSeason: season, FurthestEpisode: episode})
		}
		if err := st.ReplaceWatched(ctx, "series", 2, rows); err != nil {
			t.Fatal(err)
		}
		if err := cat.SpaceSaver(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := cat.AddSeries(ctx, 778, catalog.AddOptions{Monitored: true, Monitor: "all", KeepAhead: 101}); err == nil {
		t.Fatal("101 episodes ahead was taken")
	}
	s, err := cat.AddSeries(ctx, 778, catalog.AddOptions{Monitored: true, Monitor: "all", KeepAhead: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Nobody has started it: the first three.
	if got := held(s.ID); got != "...h hhhhh" {
		t.Fatalf("not started: %s", got)
	}
	// Only what isn't held back counts as wanted.
	if s, _ = st.Series(ctx, s.ID); s.KeepAhead != 3 || s.EpisodeCount != 3 {
		t.Fatalf("show: keep %d, wanted %d", s.KeepAhead, s.EpisodeCount)
	}
	// Someone starts episode 2: three past it, across the season's end.
	watch(s.ID, 1, 2)
	if got := held(s.ID); got != ".... .hhhh" {
		t.Fatalf("at S1E2: %s", got)
	}
	// The episodes let go are looked for.
	if js, _ := st.Jobs(ctx, 50); len(js) != 1 || js[0].Kind != catalog.JobSearchSeries {
		t.Fatalf("jobs after someone watched: %+v", js)
	}
	// Near the end, the episode to come is let go too, to be fetched when
	// it's out.
	watch(s.ID, 2, 3)
	if got := held(s.ID); got != ".... ....." {
		t.Fatalf("at S2E3: %s", got)
	}
	// Matinee forgetting the plays holds episodes back again; nothing on
	// disk is touched either way.
	watch(s.ID, 0, 0)
	if got := held(s.ID); got != "...h hhhhh" {
		t.Fatalf("plays forgotten: %s", got)
	}
	// A season switched off isn't counted, and its switch stays off.
	if err := st.SetSeason(ctx, s.ID, store.Season{Number: 1, Monitored: false}); err != nil {
		t.Fatal(err)
	}
	if n, err := cat.HoldAhead(ctx, s.ID); err != nil || n != 3 {
		t.Fatalf("season off: let go of %d, %v", n, err)
	}
	if got := held(s.ID); got != ".... ...hh" {
		t.Fatalf("season off: %s", got)
	}
	// A refresh keeps it so.
	if err := cat.RefreshSeries(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got := held(s.ID); got != ".... ...hh" {
		t.Fatalf("after a refresh: %s", got)
	}
	// Off: nothing is held back, and the switches are as they were.
	s, _ = st.Series(ctx, s.ID)
	s.KeepAhead = 0
	if err := st.UpdateSeries(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := cat.HoldAheadAndSearch(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got := held(s.ID); got != ".... ....." {
		t.Fatalf("off: %s", got)
	}
	eps, _ := st.Episodes(ctx, s.ID)
	for _, e := range eps {
		if !e.Monitored {
			t.Fatalf("S%dE%d was switched off", e.Season, e.Number)
		}
	}
}
