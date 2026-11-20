package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// An episode a show's space saver holds back isn't wanted: the patrol and
// RSS sync pass it over until it's let go.
func TestHeldEpisodesAreNotWanted(t *testing.T) {
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) { return 200, feedOf() })
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	if err := st.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 900, Title: "Show", ProfileID: m.ProfileID, RootFolder: "/tv", Path: "/tv/Show", Monitored: true,
		Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	out := time.Now().AddDate(0, -1, 0)
	for n := 1; n <= 3; n++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, AirDate: &out, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	if list, _ := s.WantedList(ctx); len(list) != 3 {
		t.Fatalf("wanted before: %+v", list)
	}
	for _, e := range eps[1:] {
		if err := st.SetEpisodeHeld(ctx, e.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.WantedList(ctx)
	if len(list) != 1 || list[0].ID != eps[0].ID {
		t.Fatalf("wanted with two held back: %+v", list)
	}
	if got, _ := st.Series(ctx, show.ID); got.EpisodeCount != 1 {
		t.Fatalf("the show counts %d wanted episodes", got.EpisodeCount)
	}
	// One that's held back but on disk already still counts as there.
	if _, err := st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[2].ID, Path: "/tv/Show/e3.mkv", Quality: 9}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Series(ctx, show.ID); got.EpisodeCount != 2 || got.FileCount != 1 {
		t.Fatalf("with a held episode on disk: %d of %d", got.FileCount, got.EpisodeCount)
	}
	// and is upgraded like any other.
	if list, _ = s.WantedList(ctx); len(list) != 2 || !list[1].Upgrade || list[1].ID != eps[2].ID {
		t.Fatalf("wanted with a held episode on disk: %+v", list)
	}
	st.SetEpisodeHeld(ctx, eps[1].ID, false)
	if list, _ = s.WantedList(ctx); len(list) != 3 {
		t.Fatalf("wanted with one let go: %+v", list)
	}
}
