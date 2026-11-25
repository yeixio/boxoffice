package search

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// A show whose releases follow another episode order (the DVD sets',
// say) is asked for, and its releases are read, by each episode's place
// in that order: S02E02 as aired is S01E05 on the discs here.
func TestEpisodeOrder(t *testing.T) {
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(
			item("Frieren.S01E05.1080p.WEB-DL-GRP", "disc", 1e9),
			item("Frieren.S02E02.1080p.WEB-DL-GRP", "aired", 1e9),
			item("Frieren.S01.1080p.WEB-DL-GRP", "pack", 6e9),
		)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	show, eps := animeShow(t, st, m.ProfileID)
	want := episodeOf(eps, 2, 2)

	// The discs hold all six episodes as one season; the special isn't
	// on them.
	places := map[int64]store.Place{}
	for _, e := range eps {
		if e.Season > 0 {
			places[e.ID] = store.Place{Season: 1, Number: (e.Season-1)*3 + e.Number}
		}
	}
	show.Numbering = store.OrderNumbering("dvd1")
	if err := st.UpdateSeries(ctx, show); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEpisodePlaces(ctx, show.ID, places); err != nil {
		t.Fatal(err)
	}

	eps, _ = st.Episodes(ctx, show.ID)

	res, err := s.Episode(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, c := range res.Releases {
		why[c.GUID] = strings.Join(c.Rejections, "; ")
		if c.Accepted && (len(c.episodes) != 1 || c.episodes[0] != want.ID) {
			t.Fatalf("%s taken for %v", c.Title, c.episodes)
		}
	}
	if why["disc"] != "" || !strings.Contains(why["aired"], "it's season 2") {
		t.Fatalf("reasons %v", why)
	}
	if q := (*asked)[len(*asked)-1]; !strings.Contains(q, "season=1") || !strings.Contains(q, "ep=5") {
		t.Fatalf("asked %s", q)
	}

	// The second season as aired is asked for as the discs' first, and a
	// pack of that brings its three episodes.
	n := len(*asked)
	var second []store.Episode
	for _, e := range eps {
		if e.Season == 2 {
			second = append(second, e)
		}
	}
	res, err = s.Season(ctx, show, 2, second)
	if err != nil {
		t.Fatal(err)
	}
	if qs := (*asked)[n:]; len(qs) != 1 || !strings.Contains(qs[0], "season=1") {
		t.Fatalf("asked %v", qs)
	}
	for _, c := range res.Releases {
		switch c.GUID {
		case "pack":
			if !c.Accepted || len(c.episodes) != 3 {
				t.Fatalf("the pack: %v %v", c.Rejections, c.episodes)
			}
		case "disc":
			if !c.Accepted || len(c.episodes) != 1 || c.episodes[0] != want.ID {
				t.Fatalf("the single episode: %v %v", c.Rejections, c.episodes)
			}
		case "aired":
			if c.Accepted {
				t.Fatal("a release numbered as aired was taken")
			}
		}
	}

	// An episode the order doesn't list keeps its own number while no
	// other episode has been given it.
	special := episodeOf(eps, 0, 1)
	if in := s.inOrder(ctx, show, []store.Episode{special}); in[0].Season != 0 || in[0].Number != 1 {
		t.Fatalf("the special is read as %d/%d", in[0].Season, in[0].Number)
	}
}
