package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// The dashboard's counts: missing is only what's wanted and already out.
func TestLibraryStats(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	profiles, err := st.Profiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatal(profiles, err)
	}
	p := profiles[0].ID
	today := time.Now().UTC().Truncate(time.Second)
	day := func(d int) *time.Time { at := today.AddDate(0, 0, d); return &at }

	add := func(id int64, out *time.Time, monitored bool) store.Movie {
		m, err := st.AddMovie(ctx, store.Movie{TMDBID: id, Title: "M", ProfileID: p, RootFolder: "/movies", Path: "/movies/m" + string(rune('a'+id)), Monitored: monitored, DigitalRelease: out})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	have := add(1, day(-30), true)
	add(2, day(-30), true)  // missing
	add(3, day(30), true)   // not out yet
	add(4, day(-30), false) // not wanted
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: have.ID, Path: "/movies/a.mkv", Size: 1}); err != nil {
		t.Fatal(err)
	}

	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1, Title: "S", ProfileID: p, RootFolder: "/tv", Path: "/tv/S", Monitored: true,
		Seasons: []store.Season{{Number: 0}, {Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Episode{
		{SeriesID: show.ID, Season: 0, Number: 1, AirDate: day(-1), Monitored: true}, // season not wanted
		{SeriesID: show.ID, Season: 1, Number: 1, AirDate: day(-14), Monitored: true},
		{SeriesID: show.ID, Season: 1, Number: 2, AirDate: day(-7), Monitored: true}, // missing
		{SeriesID: show.ID, Season: 1, Number: 3, AirDate: day(7), Monitored: true},  // not aired
	} {
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := st.Episodes(ctx, show.ID)
	for _, e := range eps {
		if e.Season == 1 && e.Number == 1 {
			if _, err := st.AddFile(ctx, store.File{Kind: "episode", OwnerID: e.ID, Path: "/tv/S/1.mkv", Size: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}

	got, err := st.LibraryStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := store.LibraryStats{Movies: 4, MoviesOnDisk: 1, MoviesMissing: 1, Series: 1, Episodes: 3, EpisodesOnDisk: 1, EpisodesMissing: 1}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
