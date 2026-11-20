package arrapi_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
)

// Matinee's report of what was watched is kept by title, and each report
// replaces the one before.
func TestWatchedReport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	st := e.cat.Store
	if f := features(t, e.url+"/radarr"); !slices.Contains(f, "watched") {
		t.Fatalf("features: %v", f)
	}
	mv, err := e.cat.AddMovie(ctx, 603, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	show, err := e.cat.AddSeries(ctx, 95396, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.LastWatchReport(ctx, "movie"); ok {
		t.Fatal("a report before any came in")
	}
	last := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	var out struct{ Kept int }
	body := map[string]any{"people": 3, "titles": []any{
		map[string]any{"tmdbId": 603, "lastPlayed": last, "viewers": 2, "finishedBy": 1, "finishedAt": last},
		map[string]any{"tmdbId": 999999, "lastPlayed": last, "viewers": 1}, // not on the list
		map[string]any{"tmdbId": 603, "viewers": 1},                        // named twice: the first counts
	}}
	if code := call(t, "PUT", e.url+"/radarr/api/v3/boxoffice/watched", body, &out); code != 200 || out.Kept != 1 {
		t.Fatalf("movies: %d %+v", code, out)
	}
	got, err := st.WatchedTitles(ctx, "movie")
	if err != nil || len(got) != 1 || got[mv.ID].Viewers != 2 || got[mv.ID].FinishedBy != 1 || got[mv.ID].LastPlayed == nil || !got[mv.ID].LastPlayed.Equal(last) {
		t.Fatalf("kept: %+v, %v", got, err)
	}
	rep, ok, _ := st.LastWatchReport(ctx, "movie")
	if !ok || rep.People != 3 || rep.Titles != 1 || time.Since(rep.At) > time.Minute {
		t.Fatalf("report: %+v %v", rep, ok)
	}
	// Shows go to /sonarr, by TVDB id, and don't touch the movies. A time
	// ahead of the clock counts as now.
	ahead := time.Now().Add(72 * time.Hour)
	body = map[string]any{"people": 3, "titles": []any{map[string]any{"tvdbId": 371980, "lastPlayed": ahead, "viewers": 1}}}
	if code := call(t, "PUT", e.url+"/sonarr/api/v3/boxoffice/watched", body, &out); code != 200 || out.Kept != 1 {
		t.Fatalf("shows: %d %+v", code, out)
	}
	shows, _ := st.WatchedTitles(ctx, "series")
	if w := shows[show.ID]; w.LastPlayed == nil || w.LastPlayed.After(time.Now().Add(time.Minute)) {
		t.Fatalf("show: %+v", shows)
	}
	if got, _ := st.WatchedTitles(ctx, "movie"); len(got) != 1 {
		t.Fatalf("the shows' report changed the movies: %+v", got)
	}
	// A show's report can say how far anyone has got, which space saver
	// goes by: with it on, only the next episodes past there are wanted.
	if n, _ := st.Series(ctx, show.ID); n.EpisodeCount < 3 {
		t.Fatalf("the show has %d episodes out; the test needs 3", n.EpisodeCount)
	}
	keep, _ := st.Series(ctx, show.ID)
	keep.KeepAhead = 1
	if err := st.UpdateSeries(ctx, keep); err != nil {
		t.Fatal(err)
	}
	if _, err := e.cat.HoldAhead(ctx, show.ID); err != nil {
		t.Fatal(err)
	}
	wanted := func() int {
		n, _ := st.Series(ctx, show.ID)
		return n.EpisodeCount
	}
	if wanted() != 1 {
		t.Fatalf("space saver on, nothing watched: %d wanted", wanted())
	}
	body = map[string]any{"people": 3, "titles": []any{map[string]any{"tvdbId": 371980, "viewers": 1, "furthestSeason": 1, "furthestEpisode": 1}}}
	if code := call(t, "PUT", e.url+"/sonarr/api/v3/boxoffice/watched", body, &out); code != 200 || out.Kept != 1 {
		t.Fatalf("shows, with how far: %d %+v", code, out)
	}
	if shows, _ = st.WatchedTitles(ctx, "series"); shows[show.ID].FurthestSeason != 1 || shows[show.ID].FurthestEpisode != 1 {
		t.Fatalf("how far: %+v", shows)
	}
	if wanted() != 2 {
		t.Fatalf("after episode 1 was watched: %d wanted", wanted())
	}
	// The next report has nothing for the movie: its plays are forgotten.
	if code := call(t, "PUT", e.url+"/radarr/api/v3/boxoffice/watched", map[string]any{"people": 3, "titles": []any{}}, &out); code != 200 || out.Kept != 0 {
		t.Fatalf("empty report: %d %+v", code, out)
	}
	if got, _ := st.WatchedTitles(ctx, "movie"); len(got) != 0 {
		t.Fatalf("forgotten: %+v", got)
	}
	// More finishers than viewers is a mistake, and changes nothing.
	body = map[string]any{"people": 3, "titles": []any{map[string]any{"tmdbId": 603, "viewers": 1, "finishedBy": 2}}}
	if code := call(t, "PUT", e.url+"/radarr/api/v3/boxoffice/watched", body, nil); code != 400 {
		t.Fatalf("bad report: %d", code)
	}
}

// What clean-up rules have listed as leaving soon is there for Matinee.
func TestLeavingFeed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	st := e.cat.Store
	if f := features(t, e.url+"/radarr"); !slices.Contains(f, "leaving") {
		t.Fatalf("features: %v", f)
	}
	mv, err := e.cat.AddMovie(ctx, 603, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := st.AddCleanupRule(ctx, store.CleanupRule{Kind: "movie", Watched: store.CleanupEveryone, Days: 30, GraceDays: 14, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	leaves := time.Now().Add(14 * 24 * time.Hour).UTC().Truncate(time.Second)
	if listed, err := st.ListCleanupItem(ctx, store.CleanupItem{RuleID: rule.ID, Kind: "movie", TitleID: mv.ID, Files: 1, Size: 10, LeavesAt: leaves}); err != nil || !listed {
		t.Fatalf("list: %v %v", listed, err)
	}
	var out struct {
		Titles []struct {
			TMDBID   int64
			Title    string
			LeavesAt time.Time
		}
	}
	if code := call(t, "GET", e.url+"/radarr/api/v3/boxoffice/leaving", nil, &out); code != 200 || len(out.Titles) != 1 || out.Titles[0].TMDBID != 603 || !out.Titles[0].LeavesAt.Equal(leaves) {
		t.Fatalf("movies: %d %+v", code, out)
	}
	out.Titles = nil
	if code := call(t, "GET", e.url+"/sonarr/api/v3/boxoffice/leaving", nil, &out); code != 200 || len(out.Titles) != 0 {
		t.Fatalf("shows: %d %+v", code, out)
	}
}
