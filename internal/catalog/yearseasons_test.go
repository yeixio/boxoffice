package catalog_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// Horizon is numbered by year on TheTVDB (and so in Sonarr and the files),
// 1, 2, 3 on TMDB. A refresh keeps the year seasons, fills them from TMDB,
// adds the year's new episodes, and clears TMDB-numbered seasons an older
// refresh added next to them.
func TestRefreshYearNumberedShow(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	fake.Shows[1966] = map[string]any{"id": 1966, "name": "Horizon", "first_air_date": "1964-02-04", "status": "Returning Series",
		"seasons": []any{map[string]any{"season_number": 46, "name": "2009", "episode_count": 2},
			map[string]any{"season_number": 47, "name": "2009-2010", "episode_count": 2}},
		"external_ids": map[string]any{"tvdb_id": 74374}}
	fake.Episodes["1966/46"] = []map[string]any{
		{"season_number": 46, "episode_number": 1, "name": "The Secret You", "overview": "Who am I?", "air_date": "2009-01-26"},
		{"season_number": 46, "episode_number": 2, "name": "Do You See What I See?", "overview": "Colour.", "air_date": "2009-02-03"}}
	fake.Episodes["1966/47"] = []map[string]any{
		{"season_number": 47, "episode_number": 1, "name": "Is Everything We Know About the Universe Wrong?", "air_date": "2009-11-01"},
		{"season_number": 47, "episode_number": 2, "name": "To Infinity and Beyond", "air_date": "2010-02-10"}}
	cat := &catalog.Service{Store: st, Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}

	profiles, _ := st.Profiles(ctx)
	sr, err := st.AddSeries(ctx, store.Series{TMDBID: 1966, TVDBID: 74374, Title: "Horizon", Year: 1964, Monitored: true, ProfileID: profiles[0].ID,
		RootFolder: "/tv", Path: "/tv/Horizon", Seasons: []store.Season{{Number: 2009, Monitored: true}, {Number: 46, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) *time.Time { d, _ := time.Parse("2006-01-02", s); return &d }
	// From Sonarr: TheTVDB's titles, a day off for the first.
	for _, e := range []store.Episode{
		{Season: 2009, Number: 1, Title: "The Secret You", AirDate: day("2009-01-27")},
		{Season: 2009, Number: 2, Title: "Do you see what I see", AirDate: day("2009-02-03")},
		{Season: 2009, Number: 3, Title: "Is Everything We Know About the Universe Wrong?", AirDate: day("2009-11-01")},
		// From an older refresh, under TMDB's number.
		{Season: 46, Number: 1, Title: "The Secret You", AirDate: day("2009-01-26"), Monitored: true},
	} {
		e.SeriesID = sr.ID
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := st.Episodes(ctx, sr.ID)
	if _, err := st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[1].ID, Path: "/tv/Horizon/Season 2009/Horizon - S2009E01.mkv", Size: 1}); err != nil {
		t.Fatal(err)
	}

	if err := cat.RefreshSeries(ctx, sr.ID); err != nil {
		t.Fatal(err)
	}
	eps, _ = st.Episodes(ctx, sr.ID)
	var got []string
	for _, e := range eps {
		got = append(got, fmt.Sprintf("S%dE%d %s|%s|file=%v", e.Season, e.Number, e.Title, e.Overview, e.File != nil))
	}
	want := []string{
		"S2009E1 The Secret You|Who am I?|file=true",
		"S2009E2 Do You See What I See?|Colour.|file=false",
		"S2009E3 Is Everything We Know About the Universe Wrong?||file=false",
		"S2010E1 To Infinity and Beyond||file=false",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("episodes:\n%q\nwant\n%q", got, want)
	}
	after, _ := st.Series(ctx, sr.ID)
	var seasons []int
	for _, se := range after.Seasons {
		seasons = append(seasons, se.Number)
	}
	if fmt.Sprint(seasons) != "[2009 2010]" {
		t.Fatalf("seasons %v, want [2009 2010]", seasons)
	}
}
