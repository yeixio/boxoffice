package search

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// animeShow is a show of two seasons of three episodes, so its fifth
// episode straight through is S02E02.
func animeShow(t *testing.T, st *store.Store, profile int) (store.Series, []store.Episode) {
	ctx := context.Background()
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 9, TVDBID: 99, Title: "Frieren", ProfileID: profile, RootFolder: "/tv", Path: "/tv/Frieren",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}, {Number: 2, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	aired := time.Now().AddDate(0, 0, -7)
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 0, Number: 1, AirDate: &aired})
	for season := 1; season <= 2; season++ {
		for n := 1; n <= 3; n++ {
			st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: season, Number: n, AirDate: &aired, Monitored: true})
		}
	}
	eps, _ := st.Episodes(ctx, show.ID)
	return show, eps
}

func episodeOf(eps []store.Episode, season, number int) store.Episode {
	for _, e := range eps {
		if e.Season == season && e.Number == number {
			return e
		}
	}
	return store.Episode{}
}

// A show whose releases are numbered straight through: "Show - 05" is
// S02E02, asked for by name and number as well as by season and episode.
func TestAbsoluteNumbering(t *testing.T) {
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("t") == "tvsearch" {
			// Found by the show's id, under its name in Japanese.
			return 200, feedOf(item("[Group] Sousou no Frieren - 05 [1080p]", "jp", 1e9))
		}
		return 200, feedOf(
			item("[SubsPlease] Frieren - 05 (1080p) [ABCD1234]", "five", 1e9),
			item("[SubsPlease] Frieren - 04 (1080p) [ABCD1234]", "four", 1e9),
			item("[SubsPlease] Frieren - 99 (1080p) [ABCD1234]", "none", 1e9),
			item("[SubsPlease] Frieren Extra - 05 (1080p)", "other", 1e9),
		)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	show, eps := animeShow(t, st, m.ProfileID)
	want := episodeOf(eps, 2, 2)

	// Numbered by season and episode, none of them can be read.
	res, err := s.Episode(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Releases {
		if c.Accepted {
			t.Fatalf("taken without absolute numbering: %+v", c)
		}
	}
	n := len(*asked)

	show.Numbering = store.NumberingAbsolute
	if err := st.UpdateSeries(ctx, show); err != nil {
		t.Fatal(err)
	}
	if res, err = s.Episode(ctx, want.ID); err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, c := range res.Releases {
		why[c.GUID] = strings.Join(c.Rejections, "; ")
		if c.Accepted && (len(c.episodes) != 1 || c.episodes[0] != want.ID) {
			t.Fatalf("%s taken for %v", c.Title, c.episodes)
		}
	}
	if why["five"] != "" || why["jp"] != "" {
		t.Fatalf("the fifth episode was turned down: %v", why)
	}
	if !strings.Contains(why["four"], "it's episode 1") || !strings.Contains(why["none"], "doesn't say which episode") || !strings.Contains(why["other"], "doesn't") {
		t.Fatalf("reasons %v", why)
	}
	// One search by season and episode, one by name and number.
	qs := (*asked)[n:]
	if len(qs) != 2 || !strings.Contains(qs[0], "t=tvsearch") || !strings.Contains(qs[1], "t=search") || !strings.Contains(qs[1], "q=Frieren+05") {
		t.Fatalf("asked %v", qs)
	}
}

// A show wanted dubbed takes releases that say they are, or that carry
// both; one wanted with its original sound turns down a dub alone.
func TestDubbedOrSubtitled(t *testing.T) {
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(
			item("Frieren.S02E02.1080p.WEB-DL-GRP", "plain", 1e9),
			item("Frieren.S02E02.DUBBED.1080p.WEB-DL-GRP", "dub", 1e9),
			item("Frieren.S02E02.DUAL.1080p.WEB-DL-GRP", "dual", 1e9),
		)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	show, eps := animeShow(t, st, m.ProfileID)
	want := episodeOf(eps, 2, 2)
	for audio, taken := range map[string]string{"": "dual dub plain", store.AudioDubbed: "dual dub", store.AudioOriginal: "dual plain"} {
		show.Audio = audio
		if err := st.UpdateSeries(ctx, show); err != nil {
			t.Fatal(err)
		}
		res, err := s.Episode(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, id := range []string{"dual", "dub", "plain"} {
			for _, c := range res.Releases {
				if c.GUID == id && c.Accepted {
					got = append(got, id)
				}
			}
		}
		if strings.Join(got, " ") != taken {
			t.Errorf("audio %q takes %v, want %s", audio, got, taken)
		}
	}
}
