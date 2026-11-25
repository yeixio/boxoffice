package search

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// patrolTwo sets up two missing movies and one upgrade (the Matrix), with
// room for two searches an hour.
func patrolTwo(t *testing.T, order string) (*Service, *store.Store, []int64, int64) {
	t.Helper()
	srv, _ := indexer(t, item("Nothing.Useful.1999.1080p.WEB-DL-GRP", "x", 5e9))
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/The Matrix (1999).mkv", Quality: 9})
	past := time.Now().AddDate(-1, 0, 0)
	var missing []int64
	for i, title := range []string{"First", "Second"} {
		mv, _ := st.AddMovie(ctx, store.Movie{TMDBID: int64(700 + i), Title: title, Year: 2025, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/" + title, Monitored: true, DigitalRelease: &past})
		missing = append(missing, mv.ID)
	}
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	a, _ := LoadAutoSettings(ctx, st)
	a.PerDay, a.Upgrades, a.SearchOrder = 48, true, order
	SaveAutoSettings(ctx, st, a)
	return s, st, missing, m.ID
}

func TestPatrolSearchesMissingFirst(t *testing.T) {
	ctx := context.Background()
	s, st, missing, matrix := patrolTwo(t, OrderPriority)
	if a, _ := LoadAutoSettings(ctx, st); a.SearchOrder != OrderPriority || a.Early != EarlyCutoff {
		t.Fatalf("defaults %+v", a)
	}
	r, err := s.jobWanted(ctx, "{}")
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := st.Searched(ctx, "movie")
	if ms[missing[0]].At.IsZero() || ms[missing[1]].At.IsZero() || !ms[matrix].At.IsZero() {
		t.Fatalf("searched %+v (%s)", ms, r.Message)
	}
	if !strings.Contains(r.Message, "1 upgrade wait for missing titles") {
		t.Fatalf("message %q", r.Message)
	}

	// Taking turns: one missing title, then the upgrade.
	s, st, missing, matrix = patrolTwo(t, OrderTurns)
	if a, _ := LoadAutoSettings(ctx, st); a.SearchOrder != OrderTurns {
		t.Fatalf("saved %+v", a)
	}
	s.jobWanted(ctx, "{}")
	ms, _ = st.Searched(ctx, "movie")
	if ms[missing[0]].At.IsZero() || !ms[missing[1]].At.IsZero() || ms[matrix].At.IsZero() {
		t.Fatalf("taking turns searched %+v", ms)
	}
}

func TestByPriorityFollowsDownloadOrder(t *testing.T) {
	now := time.Now()
	list := []Wanted{
		{Kind: "episode", ID: 1, out: now.AddDate(0, 0, -2)}, // new episode
		{Kind: "movie", ID: 2, out: now.AddDate(-1, 0, 0)},   // backlog movie
		{Kind: "movie", ID: 3, Upgrade: true},                // upgrade
		{Kind: "movie", ID: 4, Copy: ModeSecond},             // second copy
		{Kind: store.GameKind, ID: 5},                        // game
		{Kind: "episode", ID: 6, out: now.AddDate(0, -6, 0)}, // backlog episode
		{Kind: "movie", ID: 7, out: now.AddDate(0, 0, -1)},   // new movie
	}
	ids := func(ws []Wanted) []int64 {
		var out []int64
		for _, w := range ws {
			out = append(out, w.ID)
		}
		return out
	}
	got := byPriority(list, store.QueuePriority{NewDays: 30}, now)
	if want := []int64{1, 7, 6, 2, 5, 4, 3}; !slices.Equal(ids(got), want) {
		t.Fatalf("default order %v, want %v", ids(got), want)
	}
	// Backlog movies moved first, and upgrades ahead of games.
	got = byPriority(list, store.QueuePriority{NewDays: 30, Order: []string{store.GroupBacklogMovies, store.GroupUpgrades}}, now)
	if want := []int64{2, 3, 1, 7, 6, 5, 4}; !slices.Equal(ids(got), want) {
		t.Fatalf("custom order %v, want %v", ids(got), want)
	}
}

// earlyFeed sets up RSS sync on a feed, with nothing held back, a movie
// out to stream next week, and a show with episodes airing tomorrow and in
// 10 days.
func earlyFeed(t *testing.T, early string, items ...string) (*Service, *store.Store) {
	t.Helper()
	srv, _ := fakeIndexer(t, func(*http.Request) (int, string) { return 200, feedOf(items...) })
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	a, _ := LoadAutoSettings(ctx, st)
	a.MinAgeMinutes, a.Early = 0, early
	SaveAutoSettings(ctx, st, a)

	week := time.Now().AddDate(0, 0, 7)
	st.AddMovie(ctx, store.Movie{TMDBID: 800, Title: "Soon", Year: 2026, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/Soon", Monitored: true, DigitalRelease: &week})
	far := time.Now().AddDate(0, 6, 0)
	st.AddMovie(ctx, store.Movie{TMDBID: 801, Title: "Faraway", Year: 2027, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/Faraway", Monitored: true, DigitalRelease: &far})
	show, _ := showWithEpisodes(t, st, m.ProfileID, 1) // E01 aired last week
	tomorrow, later := time.Now().AddDate(0, 0, 1), time.Now().AddDate(0, 0, 10)
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: 2, AirDate: &tomorrow, Monitored: true})
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: 3, AirDate: &later, Monitored: true})
	return s, st
}

func TestRSSGrabsEarly(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	items := []string{
		posted("Soon.2026.HDTS.1080p.x264-CAM", "m1", 3e9, now),     // a cinema recording
		posted("Soon.2026.1080p.WEBRip.x264-GRP", "m2", 3e9, now),  // below the cutoff
		posted("Soon.2026.1080p.WEB-DL.DDP5.1-GRP", "m3", 6e9, now), // meets it
		posted("Faraway.2027.1080p.WEB-DL-GRP", "m4", 6e9, now),     // months off
		posted("Show.S01E02.1080p.WEB-DL-NTb", "e2", 2e9, now),      // airs tomorrow
		posted("Show.S01E03.1080p.WEB-DL-NTb", "e3", 2e9, now),      // in 10 days
	}
	s, st := earlyFeed(t, "", items...)
	res := s.SyncRSS(ctx)
	slices.Sort(res.Grabbed)
	if want := []string{"Show.S01E02.1080p.WEB-DL-NTb", "Soon.2026.1080p.WEB-DL.DDP5.1-GRP"}; !slices.Equal(res.Grabbed, want) {
		t.Fatalf("grabbed %v (%v), want %v", res.Grabbed, res.Problems, want)
	}
	// They download as new ones.
	q, _ := st.DownloadQueue(ctx)
	for _, d := range q {
		if d.Reason != store.ReasonNew {
			t.Fatalf("%s grabbed as %q", d.Name, d.Reason)
		}
	}
	// The patrol never searches for them.
	list, _ := s.WantedList(ctx)
	for _, w := range list {
		if w.Early || w.Subject == "Soon (2026)" {
			t.Fatalf("patrol would search %+v", w)
		}
	}

	// Any quality the profile takes: the WEBRip is fine, the recording isn't.
	s, _ = earlyFeed(t, EarlyAny, items[:2]...)
	if res = s.SyncRSS(ctx); !slices.Equal(res.Grabbed, []string{"Soon.2026.1080p.WEBRip.x264-GRP"}) {
		t.Fatalf("any quality grabbed %v (%v)", res.Grabbed, res.Problems)
	}

	// Off: it waits for its date.
	s, _ = earlyFeed(t, EarlyOff, items...)
	if res = s.SyncRSS(ctx); len(res.Grabbed) != 0 {
		t.Fatalf("grabbed with early grabs off %v", res.Grabbed)
	}
}

func TestEarlyMovie(t *testing.T) {
	now := time.Now()
	at := func(days int) *time.Time { d := now.AddDate(0, 0, days); return &d }
	for _, c := range []struct {
		m    store.Movie
		want bool
	}{
		{store.Movie{DigitalRelease: at(10)}, true},
		{store.Movie{PhysicalRelease: at(40)}, false},
		{store.Movie{InCinemas: at(-5)}, true},        // in cinemas now
		{store.Movie{InCinemas: at(5)}, false},        // not even in cinemas
		{store.Movie{DigitalRelease: at(-1)}, false},  // out already: wanted as usual
		{store.Movie{Status: "In Production"}, false}, // no dates
		{store.Movie{InCinemas: at(-5), DigitalRelease: at(60)}, true},
	} {
		if got := earlyMovie(c.m, now); got != c.want {
			t.Errorf("%+v: %v, want %v", c.m, got, c.want)
		}
	}
}
