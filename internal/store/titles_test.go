package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// A show says how much it takes on disk and when it last and next airs,
// so its list can be sorted by them.
func TestSeriesSortFacts(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	profiles, err := st.Profiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatal(profiles, err)
	}
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1399, Title: "Game of Thrones", Year: 2011, Rating: 8.4, ProfileID: profiles[0].ID,
		RootFolder: "/tv", Path: "/tv/Game of Thrones", Monitored: true, Seasons: []store.Season{{Number: 0}, {Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int) *time.Time { at := time.Now().UTC().Truncate(time.Second).AddDate(0, 0, d); return &at }
	for _, e := range []store.Episode{
		{SeriesID: show.ID, Season: 0, Number: 1, AirDate: day(-1), Monitored: true},
		{SeriesID: show.ID, Season: 1, Number: 1, AirDate: day(-14), Monitored: true},
		{SeriesID: show.ID, Season: 1, Number: 2, AirDate: day(-7), Monitored: true},
		{SeriesID: show.ID, Season: 1, Number: 3, AirDate: day(7), Monitored: true},
		{SeriesID: show.ID, Season: 0, Number: 2, AirDate: day(3), Monitored: true},
	} {
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := st.Episodes(ctx, show.ID)
	for i, e := range eps[:3] {
		if _, err := st.AddFile(ctx, store.File{Kind: "episode", OwnerID: e.ID, Path: "/tv/Game of Thrones/" + string(rune('a'+i)), Size: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Series(ctx, show.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rating != 8.4 || got.SizeOnDisk != 3000 {
		t.Fatalf("rating %v, size %d", got.Rating, got.SizeOnDisk)
	}
	if got.LastAired == nil || !got.LastAired.Equal(*day(-7)) || got.NextAiring == nil || !got.NextAiring.Equal(*day(7)) {
		t.Fatalf("last aired %v, next airing %v", got.LastAired, got.NextAiring)
	}

	// A rating only fills in where there isn't one.
	if err := st.FillRating(ctx, "series", show.ID, 5); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Series(ctx, show.ID); got.Rating != 8.4 {
		t.Fatalf("rating overwritten: %v", got.Rating)
	}
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 603, Title: "The Matrix", ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/x"})
	if err != nil {
		t.Fatal(err)
	}
	st.FillRating(ctx, "movie", m.ID, 8.2)
	if got, _ := st.Movie(ctx, m.ID); got.Rating != 8.2 {
		t.Fatalf("movie rating: %v", got.Rating)
	}
}
