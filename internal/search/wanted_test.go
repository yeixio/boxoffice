package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

func TestAutoSettings(t *testing.T) {
	_, st, _ := setup(t, "http://nowhere.invalid")
	ctx := context.Background()
	a, err := LoadAutoSettings(ctx, st)
	// Missing titles are searched for by default; upgrades aren't.
	if err != nil || !a.Enabled || !a.RSS || a.Upgrades || a.PerDay != DefaultPerDay {
		t.Fatalf("defaults %+v %v", a, err)
	}
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: false, PerDay: 1e6, Upgrades: true})
	if a, _ = LoadAutoSettings(ctx, st); a.Enabled || !a.Upgrades || a.PerDay != MaxPerDay {
		t.Fatalf("saved %+v", a)
	}
}

func TestWantedMovieUpgrade(t *testing.T) {
	srv, asked := indexer(t,
		item("The.Matrix.1999.720p.BluRay.x264-GRP", "a", 5e9),
		item("The.Matrix.1999.1080p.WEB-DL.DDP5.1-GRP", "c", 7e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	hd := m.ProfileID // HD 1080p: good enough at WEB 1080p, upgrades on
	// A WEBRip is below the cutoff: it's due an upgrade.
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/The Matrix (1999).mkv", Quality: 9})
	// At the cutoff already: left alone.
	done, _ := st.AddMovie(ctx, store.Movie{TMDBID: 604, Title: "Done", Year: 2001, ProfileID: hd, RootFolder: "/m", Path: "/m/Done", Monitored: true})
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: done.ID, Path: "/m/Done/Done.mkv", Quality: 10})
	// Missing, but not out yet.
	soon := time.Now().AddDate(0, 1, 0)
	st.AddMovie(ctx, store.Movie{TMDBID: 605, Title: "Soon", Year: 2027, ProfileID: hd, RootFolder: "/m", Path: "/m/Soon", Monitored: true, InCinemas: &soon})
	// Missing and out on digital.
	past := time.Now().AddDate(0, -1, 0)
	out, _ := st.AddMovie(ctx, store.Movie{TMDBID: 606, Title: "Out", Year: 2025, ProfileID: hd, RootFolder: "/m", Path: "/m/Out", Monitored: true, DigitalRelease: &past})
	// Unmonitored: left alone.
	st.AddMovie(ctx, store.Movie{TMDBID: 607, Title: "Unwatched", Year: 2025, ProfileID: hd, RootFolder: "/m", Path: "/m/Unwatched", DigitalRelease: &past})

	list, err := s.WantedList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != out.ID || list[0].Upgrade || list[1].ID != m.ID || !list[1].Upgrade || list[1].Have != 9 {
		t.Fatalf("wanted %+v", list)
	}

	// Downloading is off: the automatic search doesn't ask the indexers.
	r, err := s.jobWanted(ctx, "{}")
	if err != nil || r.Status != "skipped" || len(*asked) != 0 {
		t.Fatalf("while off: %+v %v, asked %v", r, err, *asked)
	}

	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true})
	r, err = s.jobWanted(ctx, "{}")
	if err != nil || r.Status != "done" {
		t.Fatalf("job %+v %v", r, err)
	}
	if !strings.Contains(r.Message, "1 missing title and 1 to upgrade") || !strings.Contains(r.Message, "The Matrix (1999)") {
		t.Fatalf("message %q", r.Message)
	}
	q, _ := st.DownloadQueue(ctx)
	var matrix *store.Download
	for i := range q {
		if q[i].TitleID == m.ID {
			matrix = &q[i]
		}
	}
	if matrix == nil || matrix.Name != "The.Matrix.1999.1080p.WEB-DL.DDP5.1-GRP" {
		t.Fatalf("queue %+v", q)
	}

	// The movie that isn't out found nothing to take; the Matrix did.
	ms, _ := st.Searched(ctx, "movie")
	if ms[out.ID].Misses != 1 || ms[m.ID].Misses != 0 || ms[m.ID].At.IsZero() {
		t.Fatalf("searched %+v", ms)
	}
	st.MarkSearched(ctx, "movie", out.ID, false)
	if ms, _ = st.Searched(ctx, "movie"); ms[out.ID].Misses != 2 {
		t.Fatalf("misses %+v", ms[out.ID])
	}
	st.MarkSearched(ctx, "movie", out.ID, true)
	if ms, _ = st.Searched(ctx, "movie"); ms[out.ID].Misses != 0 {
		t.Fatalf("misses after a find %+v", ms[out.ID])
	}

	// Searched just now, and downloading: the next hour leaves them be.
	n := len(*asked)
	r, _ = s.jobWanted(ctx, "{}")
	if len(*asked) != n || !strings.Contains(r.Message, "searched for recently") {
		t.Fatalf("again: %+v, asked %d more", r, len(*asked)-n)
	}

	// Upgrades off in the profile: nothing's wanted for the Matrix.
	ps, _ := st.Profiles(ctx)
	for _, p := range ps {
		if p.ID == hd {
			p.UpgradeOK = false
			st.UpdateProfile(ctx, p)
		}
	}
	list, _ = s.WantedList(ctx)
	for _, w := range list {
		if w.ID == m.ID && w.Kind == "movie" {
			t.Fatalf("still wanted with upgrades off: %+v", w)
		}
	}
}

func TestSeriesSearchUpgrades(t *testing.T) {
	srv, _ := indexer(t,
		item("Show.S01E01.1080p.WEB-DL-GRP", "e1", 2e9),
		item("Show.S01E02.1080p.WEB-DL-GRP", "e2", 2e9),
		item("Show.S01E03.1080p.WEB-DL-GRP", "e3", 2e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1, TVDBID: 11, Title: "Show", ProfileID: m.ProfileID, RootFolder: "/tv", Path: "/tv/Show",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	aired := time.Now().AddDate(0, 0, -7)
	for n := 1; n <= 3; n++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, AirDate: &aired, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: "/tv/Show/Season 01/e1.mkv", Quality: 4})  // HDTV 720p: upgrade
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[1].ID, Path: "/tv/Show/Season 01/e2.mkv", Quality: 11}) // above the cutoff
	// Episode 3 is missing.

	r, err := s.jobSeries(ctx, fmt.Sprintf(`{"id":%d}`, show.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Message, "1 missing episode and 1 to upgrade") || !strings.Contains(r.Message, "grabbed 2") {
		t.Fatalf("job %+v", r)
	}
	q, _ := st.DownloadQueue(ctx)
	got := map[string]bool{}
	for _, d := range q {
		got[d.Name] = true
	}
	if !got["Show.S01E01.1080p.WEB-DL-GRP"] || !got["Show.S01E03.1080p.WEB-DL-GRP"] || got["Show.S01E02.1080p.WEB-DL-GRP"] {
		t.Fatalf("queue %v", got)
	}
	// Both are downloading: searching again grabs nothing more.
	r, _ = s.jobSeries(ctx, fmt.Sprintf(`{"id":%d}`, show.ID))
	if !strings.Contains(r.Message, "grabbed 0") || !strings.Contains(r.Message, "2 already downloading") {
		t.Fatalf("again %+v", r)
	}
}

func TestAgainAfter(t *testing.T) {
	day := 24 * time.Hour
	for misses, want := range map[int]time.Duration{0: day, 1: day, 2: 3 * day, 3: 7 * day, 4: 14 * day, 5: 30 * day, 50: 30 * day} {
		if got := againAfter(misses); got != want {
			t.Errorf("after %d misses: %s, want %s", misses, got, want)
		}
	}
	now := time.Now()
	if (Wanted{Searched: now.Add(-2 * day), Misses: 2}).due(now) || !(Wanted{Searched: now.Add(-4 * day), Misses: 2}).due(now) || !(Wanted{}).due(now) {
		t.Fatal("due judged wrong")
	}
}

// Missing titles don't starve upgrades: the automatic search takes them in
// turn.
func TestWantedTakesTurns(t *testing.T) {
	if got := interleave([]int{1, 3, 5, 7}, []int{2, 4}); fmt.Sprint(got) != "[1 2 3 4 5 7]" {
		t.Fatal(got)
	}
}

// Choosing a release for a season by hand: packs are marked, and judged
// against every aired episode.
func TestSeasonReleases(t *testing.T) {
	srv, _ := indexer(t, item("Show.S01.1080p.BluRay.x264-GRP", "pack", 20e9))
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	show, _ := st.AddSeries(ctx, store.Series{TMDBID: 1, TVDBID: 11, Title: "Show", ProfileID: m.ProfileID, RootFolder: "/tv", Path: "/tv/Show",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}, {Number: 2, Monitored: true}}})
	aired, soon := time.Now().AddDate(-1, 0, 0), time.Now().AddDate(0, 1, 0)
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: 1, AirDate: &aired, Monitored: true})
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 2, Number: 1, AirDate: &soon, Monitored: true})
	res, err := s.SeasonReleases(ctx, show.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Releases[0]; !c.Pack || !c.Accepted {
		t.Fatalf("pack %+v", c)
	}
	if _, err := s.SeasonReleases(ctx, show.ID, 2); !errors.Is(err, ErrNothingAired) {
		t.Fatalf("season 2: %v", err)
	}
}

// A movie on Do not touch is never wanted, searched for or grabbed.
func TestDoNotTouchNotSearched(t *testing.T) {
	srv, asked := indexer(t, item("The.Matrix.1999.1080p.BluRay.x264-GRP", "b", 9e9))
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	ps, _ := st.Profiles(ctx)
	for _, p := range ps {
		if p.Locked {
			m.ProfileID = p.ID
		}
	}
	st.UpdateMovie(ctx, m)
	if list, _ := s.WantedList(ctx); len(list) != 0 {
		t.Fatalf("wanted %+v", list)
	}
	if _, err := s.Movie(ctx, m.ID); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("search: %v", err)
	}
	if r, _ := s.jobMovie(ctx, fmt.Sprintf(`{"id":%d}`, m.ID)); r.Status != "skipped" {
		t.Fatalf("job %+v", r)
	}
	if len(*asked) != 0 {
		t.Fatalf("asked the indexer %v", *asked)
	}
	if _, err := s.grab(ctx, Candidate{Release: Release{Protocol: "usenet"}, kind: "movie", titleID: m.ID}); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("grab: %v", err)
	}
}
