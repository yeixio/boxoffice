package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
)

// A show's space saver is set on the show: episodes past the next few are
// held back, the show says how far Matinee has it watched, and switching
// an episode off moves the count along.
func TestEditSpaceSaver(t *testing.T) {
	s, hs, _ := monitorServer(t)
	ctx := context.Background()
	s.Catalog = &catalog.Service{Store: s.Store, Jobs: jobs.New(s.Store, s.Log), Log: s.Log}
	root := t.TempDir()
	ps, _ := s.Store.Profiles(ctx)
	m, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Keep", Year: 2000, ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Keep (2000)"), Monitored: true})
	sh, _ := s.Store.AddSeries(ctx, store.Series{TMDBID: 2, Title: "Show", ProfileID: ps[0].ID, RootFolder: root, Path: filepath.Join(root, "Show"), Monitored: true,
		Seasons: []store.Season{{Number: 1, Monitored: true}}})
	aired := time.Now().Add(-48 * time.Hour)
	for n := 1; n <= 4; n++ {
		if err := s.Store.UpsertEpisode(ctx, store.Episode{SeriesID: sh.ID, Season: 1, Number: n, AirDate: &aired, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	type detail struct {
		Series   store.Series
		Episodes []store.Episode
		Watched  *struct {
			Reported        bool
			Season, Episode int
		}
	}
	held := func(d detail) string {
		out := ""
		for _, e := range d.Episodes {
			if e.Held {
				out += "h"
			} else {
				out += "."
			}
		}
		return out
	}
	path := fmt.Sprintf("/api/v1/series/%d", sh.ID)
	var d detail
	if code := call(t, hs, "GET", path, "", &d); code != 200 || d.Watched != nil || held(d) != "...." {
		t.Fatalf("before: %d %+v", code, d)
	}
	for _, bad := range []string{`{"keepAhead":-1}`, `{"keepAhead":101}`} {
		if code := call(t, hs, "PATCH", path, bad, nil); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/movies/%d", m.ID), `{"keepAhead":3}`, nil); code != 400 {
		t.Fatalf("a movie took space saver: %d", code)
	}
	d = detail{}
	if code := call(t, hs, "PATCH", path, `{"keepAhead":2}`, &d); code != 200 || d.Series.KeepAhead != 2 || held(d) != "..hh" {
		t.Fatalf("on: %d %+v", code, d)
	}
	// Matinee hasn't said anything yet, and the page can say so.
	if d.Watched == nil || d.Watched.Reported || d.Series.EpisodeCount != 2 {
		t.Fatalf("on, no report: %+v", d)
	}
	if err := s.Store.ReplaceWatched(ctx, "series", 1, []store.Watched{{TitleID: sh.ID, Viewers: 1, FurthestSeason: 1, FurthestEpisode: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Catalog.SpaceSaver(ctx); err != nil {
		t.Fatal(err)
	}
	d = detail{}
	if code := call(t, hs, "GET", path, "", &d); code != 200 || !d.Watched.Reported || d.Watched.Season != 1 || d.Watched.Episode != 1 || held(d) != "...h" {
		t.Fatalf("after a report: %d %+v", code, d)
	}
	// Episode 2 switched off isn't counted: the fourth takes its place.
	var e store.Episode
	if code := call(t, hs, "PATCH", fmt.Sprintf("/api/v1/episodes/%d", d.Episodes[1].ID), `{"monitored":false}`, &e); code != 200 || e.Monitored || e.Held {
		t.Fatalf("episode off: %d %+v", code, e)
	}
	d = detail{}
	if call(t, hs, "GET", path, "", &d); held(d) != "...." {
		t.Fatalf("episode off: %s", held(d))
	}
	// Changing nothing else leaves it on; 0 turns it off.
	d = detail{}
	if code := call(t, hs, "PATCH", path, `{"monitored":true}`, &d); code != 200 || d.Series.KeepAhead != 2 {
		t.Fatalf("another edit: %d %+v", code, d.Series)
	}
	call(t, hs, "PATCH", fmt.Sprintf("/api/v1/episodes/%d", d.Episodes[1].ID), `{"monitored":true}`, nil)
	d = detail{}
	if call(t, hs, "GET", path, "", &d); held(d) != "...h" {
		t.Fatalf("episode back on: %s", held(d))
	}
	d = detail{}
	if code := call(t, hs, "PATCH", path, `{"keepAhead":0}`, &d); code != 200 || d.Series.KeepAhead != 0 || held(d) != "...." || d.Watched != nil {
		t.Fatalf("off: %d %+v", code, d)
	}
}
