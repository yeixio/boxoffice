package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// fakeIndexer answers with what answer returns for each API call, and an
// NZB for each download.
func fakeIndexer(t *testing.T, answer func(r *http.Request) (int, string)) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var asked []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.RawQuery)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/getnzb/") {
			io.WriteString(w, nzbBody)
			return
		}
		code, body := answer(r)
		if code == 429 {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(code)
		io.WriteString(w, strings.ReplaceAll(body, "{{base}}", srv.URL))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func feedOf(items ...string) string { return fmt.Sprintf(feed, strings.Join(items, "\n")) }

func testIndexer(t *testing.T, st *store.Store) store.Indexer {
	ixs, _ := st.Indexers(context.Background())
	for _, ix := range ixs {
		if ix.Name == "Test" {
			return ix
		}
	}
	t.Fatal("no test indexer")
	return store.Indexer{}
}

func TestBackoff(t *testing.T) {
	limited := true
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		if limited {
			return 429, "slow down"
		}
		return 200, feedOf(item("The.Matrix.1999.1080p.WEB-DL-GRP", "a", 5e9))
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()

	if _, err := s.Movie(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	ix := testIndexer(t, st)
	status, _ := st.IndexerStatus(ctx, ix.ID)
	if !status.Resting(time.Now()) || status.RestingUntil.Sub(time.Now()) < 110*time.Second || status.Failures != 1 {
		t.Fatalf("after a 429: %+v", status)
	}
	// While it rests, it isn't asked, and searches say why.
	n := len(*asked)
	res, err := s.Movie(ctx, m.ID)
	if err != nil || len(*asked) != n || res.Searched != 0 || !strings.Contains(strings.Join(res.Errors, ";"), "Test is resting until") {
		t.Fatalf("while resting: %+v %v, asked %d", res, err, len(*asked)-n)
	}
	// Try again now clears it.
	limited = false
	if err := s.ResetIndexer(ctx, ix.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ = s.Movie(ctx, m.ID); res.Searched != 1 || len(res.Releases) != 1 {
		t.Fatalf("after reset: %+v", res)
	}
	states, _ := s.IndexerStates(ctx)
	if st := states[ix.ID]; st.Resting || st.Failures != 0 || st.Today.Queries != 2 {
		t.Fatalf("state %+v", st)
	}
}

func TestRestLadder(t *testing.T) {
	if restAfter(1) != 0 || restAfter(2) != time.Minute || restAfter(6) != time.Hour || restAfter(99) != 24*time.Hour {
		t.Fatal("ladder")
	}
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, `<error code="500" description="Request limit reached"/>`
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	s.Movie(ctx, m.ID)
	status, _ := st.IndexerStatus(ctx, testIndexer(t, st).ID)
	if d := time.Until(status.RestingUntil); d < 59*time.Minute || !strings.Contains(status.LastError, "daily search limit") {
		t.Fatalf("limit reached: rests %s, %+v", d, status)
	}
}

func TestLimitsReportedAndGrabLimit(t *testing.T) {
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) {
		body := feedOf(item("The.Matrix.1999.1080p.WEB-DL-GRP", "a", 5e9))
		return 200, strings.Replace(body, "<channel>", `<channel><newznab:apilimits apicurrent="40" apimax="5000" grabcurrent="599" grabmax="600"/>`, 1)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	res, err := s.Movie(ctx, m.ID)
	if err != nil || len(res.Releases) == 0 {
		t.Fatal(res, err)
	}
	ix := testIndexer(t, st)
	states, _ := s.IndexerStates(ctx)
	if got := states[ix.ID]; got.QueryLimit != 5000 || got.GrabLimit != 600 || got.Today.Queries != 40 || got.Today.Grabs != 599 || !got.LimitsReport {
		t.Fatalf("limits %+v", got)
	}
	// An automatic grab leaves the last 10% for grabs by hand.
	if _, err := s.grab(automatic(ctx), res.Releases[0]); err == nil || !strings.Contains(err.Error(), "kept for grabs by hand") {
		t.Fatalf("auto grab: %v", err)
	}
	// A grab by hand takes the last one; then there are none.
	if _, err := s.grab(ctx, res.Releases[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.grab(ctx, res.Releases[0]); err == nil || !strings.Contains(err.Error(), "used up") {
		t.Fatalf("past the limit: %v", err)
	}
	// Patrol room: half of what's left today.
	ix.DailyQueries = 100
	st.UpdateIndexer(ctx, ix)
	room, _, _ := s.autoSearchRoom(ctx, []store.Indexer{ix})
	if room != 30 { // (100 - 41) / 2, the search just made counted
		t.Fatalf("room %d", room)
	}
}

func showWithEpisodes(t *testing.T, st *store.Store, profile int, n int) (store.Series, []store.Episode) {
	ctx := context.Background()
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 1, TVDBID: 11, Title: "Show", ProfileID: profile, RootFolder: "/tv", Path: "/tv/Show",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	aired := time.Now().AddDate(0, 0, -7)
	for i := 1; i <= n; i++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: i, AirDate: &aired, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	return show, eps
}

func TestSeasonSearch(t *testing.T) {
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(
			item("Show.S01.1080p.WEB-DL-GRP", "pack", 20e9),
			item("Show.S01E02.720p.HDTV-GRP", "e2", 1e9),
		)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	show, eps := showWithEpisodes(t, st, m.ProfileID, 4)
	// Episode 4 is there already, at the cutoff.
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[3].ID, Path: "/tv/Show/Season 01/e4.mkv", Quality: 10})

	r, err := s.jobSeries(ctx, fmt.Sprintf(`{"id":%d}`, show.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Message, "3 missing episodes in 1 search") || !strings.Contains(r.Message, "grabbed 1") {
		t.Fatalf("job %+v", r)
	}
	var searches []string
	for _, q := range *asked {
		if strings.Contains(q, "t=tvsearch") {
			searches = append(searches, q)
		}
	}
	if len(searches) != 1 || !strings.Contains(searches[0], "season=1") || strings.Contains(searches[0], "ep=") {
		t.Fatalf("searches %v", searches)
	}
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 1 || q[0].Name != "Show.S01.1080p.WEB-DL-GRP" || len(q[0].EpisodeIDs) != 3 {
		t.Fatalf("queue %+v", q)
	}
	for _, id := range q[0].EpisodeIDs {
		if id == eps[3].ID {
			t.Fatal("the pack is down for the episode that's there already")
		}
	}
}

func TestPatrolOrderAndCap(t *testing.T) {
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) { return 200, feedOf() })
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	hd := m.ProfileID
	// The Matrix (from setup) is missing and long out: missing for longer.
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/x.mkv", Quality: 9}) // an upgrade, now
	recent := time.Now().AddDate(0, 0, -3)
	old := time.Now().AddDate(-2, 0, 0)
	newM, _ := st.AddMovie(ctx, store.Movie{TMDBID: 700, Title: "New", Year: 2026, ProfileID: hd, RootFolder: "/m", Path: "/m/New", Monitored: true, DigitalRelease: &recent})
	oldM, _ := st.AddMovie(ctx, store.Movie{TMDBID: 701, Title: "Old", Year: 2020, ProfileID: hd, RootFolder: "/m", Path: "/m/Old", Monitored: true, DigitalRelease: &old})
	list, _ := s.WantedList(ctx)
	if len(list) != 3 || list[0].ID != newM.ID || !list[0].New || list[1].ID != oldM.ID || list[2].ID != m.ID {
		t.Fatalf("order %+v", list)
	}

	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true})
	downloader.SaveCapSettings(ctx, st, downloader.CapSettings{Enabled: true, GB: 1, UpgradesAt: 70, MissingAt: 90, ResetDay: 1})
	st.AddDataUsage(ctx, 1, 750e6) // 75% of 1 GB: upgrades wait
	r, err := s.jobWanted(ctx, "{}")
	if err != nil || !strings.Contains(r.Message, "2 missing titles") || !strings.Contains(r.Message, "1 upgrade waiting") {
		t.Fatalf("at 75%%: %+v %v", r, err)
	}
	st.AddDataUsage(ctx, 1, 200e6) // 95%: everything waits
	if r, _ = s.jobWanted(ctx, "{}"); r.Status != "skipped" || !strings.Contains(r.Message, "automatic searches wait") {
		t.Fatalf("at 95%%: %+v", r)
	}
	if res := s.SyncRSS(ctx); !strings.Contains(res.Skipped, "automatic searches wait") {
		t.Fatalf("rss at 95%%: %+v", res)
	}
}

func TestRSSSync(t *testing.T) {
	var mu sync.Mutex
	items := []string{
		item("Show.S01E01.1080p.WEB-DL-GRP", "e1", 2e9),
		item("Other.Show.S01E01.1080p.WEB-DL-GRP", "x", 2e9),
		item("Arrival.2016.1080p.WEB-DL-GRP", "m", 7e9),
		item("Arrival.1996.1080p.WEB-DL-GRP", "y", 7e9),
	}
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, feedOf(items...)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	if res := s.SyncRSS(ctx); !strings.Contains(res.Skipped, "downloading is off") {
		t.Fatalf("while off: %+v", res)
	}
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	showWithEpisodes(t, st, m.ProfileID, 2)
	past := time.Now().AddDate(-1, 0, 0)
	st.AddMovie(ctx, store.Movie{TMDBID: 329865, Title: "Arrival", Year: 2016, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/Arrival", Monitored: true, DigitalRelease: &past})

	res := s.SyncRSS(ctx)
	if res.Skipped != "" || res.Releases != 4 || len(res.Grabbed) != 2 {
		t.Fatalf("sync %+v", res)
	}
	q, _ := st.DownloadQueue(ctx)
	got := map[string]bool{}
	for _, d := range q {
		got[d.Name] = true
	}
	if !got["Show.S01E01.1080p.WEB-DL-GRP"] || !got["Arrival.2016.1080p.WEB-DL-GRP"] || len(got) != 2 {
		t.Fatalf("queue %v", got)
	}
	if !strings.Contains((*asked)[0], "t=search") || !strings.Contains((*asked)[0], "cat=2000%2C5000") {
		t.Fatalf("asked %v", *asked)
	}
	// Nothing new: nothing read.
	if res = s.SyncRSS(ctx); res.Releases != 0 || len(res.Grabbed) != 0 {
		t.Fatalf("again %+v", res)
	}
	// A new episode arrives on top.
	mu.Lock()
	items = append([]string{strings.Replace(item("Show.S01E02.1080p.WEB-DL-GRP", "e2", 2e9), "15:04:05", "16:04:05", 1)}, items...)
	mu.Unlock()
	if res = s.SyncRSS(ctx); res.Releases != 1 || len(res.Grabbed) != 1 || res.Grabbed[0] != "Show.S01E02.1080p.WEB-DL-GRP" {
		t.Fatalf("new episode %+v", res)
	}
}

func TestUpgradesOffByDefault(t *testing.T) {
	var mu sync.Mutex
	items := []string{item("The.Matrix.1999.1080p.WEB-DL-GRP", "m1", 7e9)}
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, feedOf(items...)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	// A WEBRip, below the profile's cutoff, and the profile takes upgrades.
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/x.mkv", Quality: 9})

	// It's still listed as an upgrade, for Settings and by-hand searches.
	if list, _ := s.WantedList(ctx); len(list) != 1 || !list[0].Upgrade {
		t.Fatalf("wanted %+v", list)
	}
	// Neither the patrol nor RSS sync go after it.
	if _, err := s.jobWanted(ctx, "{}"); err != nil {
		t.Fatal(err)
	}
	for _, q := range *asked {
		if strings.Contains(q, "t=movie") {
			t.Fatalf("the patrol searched for an upgrade: %v", *asked)
		}
	}
	if res := s.SyncRSS(ctx); res.Releases != 1 || len(res.Grabbed) != 0 {
		t.Fatalf("rss with upgrades off %+v", res)
	}

	// Turned on, RSS sync takes the better copy.
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true})
	mu.Lock()
	items = append([]string{strings.Replace(item("The.Matrix.1999.1080p.BluRay-GRP", "m2", 9e9), "15:04:05", "16:04:05", 1)}, items...)
	mu.Unlock()
	if res := s.SyncRSS(ctx); len(res.Grabbed) != 1 {
		t.Fatalf("rss with upgrades on %+v", res)
	}
}

// A title's own choice wins over Settings, and the month's limit stops
// automatic upgrades once it's reached.
func TestUpgradesPerTitleAndMonthlyLimit(t *testing.T) {
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(item("The.Matrix.1999.1080p.WEB-DL-GRP", "m1", 7e9))
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	// withFile adds a movie with a WEBRip, below the cutoff.
	withFile := func(m store.Movie, pref string) store.Movie {
		if m.ID == 0 {
			m, _ = st.AddMovie(ctx, m)
		}
		st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/x.mkv", Quality: 9})
		m, _ = st.Movie(ctx, m.ID)
		m.AutoUpgrade = pref
		st.UpdateMovie(ctx, m)
		return m
	}
	movieSearches := func() int {
		n := 0
		for _, q := range *asked {
			if strings.Contains(q, "t=movie") {
				n++
			}
		}
		return n
	}

	// Off in Settings, on for the Matrix: the patrol goes after it, and
	// the grab counts toward the month.
	m = withFile(m, "on")
	if r, err := s.jobWanted(ctx, "{}"); err != nil || movieSearches() != 1 || !strings.Contains(r.Message, "1 title to upgrade") {
		t.Fatalf("on for the title: %+v %v, asked %v", r, err, *asked)
	}
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
	st.DeleteDownload(ctx, q[0].ID)
	if n, _ := st.AutoUpgradesSince(ctx, monthStart(time.Now())); n != 1 {
		t.Fatalf("counted %d upgrades", n)
	}

	// On in Settings, off for another movie: it's left alone.
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true})
	withFile(store.Movie{TMDBID: 800, Title: "Kept", Year: 2001, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/Kept", Monitored: true}, "off")
	s.jobWanted(ctx, "{}")
	if movieSearches() != 1 {
		t.Fatalf("off for the title, searched anyway: %v", *asked)
	}
	// And a grab Box Office makes by itself is refused; one by hand isn't.
	m.AutoUpgrade = "off"
	st.UpdateMovie(ctx, m)
	res, err := s.Movie(ctx, m.ID)
	if err != nil || len(res.Releases) == 0 {
		t.Fatal(res, err)
	}
	if _, err := s.grab(automatic(ctx), res.Releases[0]); !errors.Is(err, ErrUpgradeOff) {
		t.Fatalf("automatic grab with the title off: %v", err)
	}
	// Not marked either way (Matinee's search commands): the rules hold.
	if _, err := s.grab(ctx, res.Releases[0]); !errors.Is(err, ErrUpgradeOff) {
		t.Fatalf("unmarked grab with the title off: %v", err)
	}
	if _, err := s.grab(byHand(ctx), res.Releases[0]); err != nil {
		t.Fatalf("by hand: %v", err)
	}
	if n, _ := st.AutoUpgradesSince(ctx, monthStart(time.Now())); n != 1 {
		t.Fatalf("a grab by hand counted: %d", n)
	}
	q, _ = st.DownloadQueue(ctx)
	for _, d := range q {
		st.DeleteDownload(ctx, d.ID)
	}

	// Following Settings, 1 a month and 1 used: nothing more this month.
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true, UpgradesPerMonth: 1})
	if a, _ := LoadAutoSettings(ctx, st); a.UpgradesPerMonth != 1 {
		t.Fatalf("saved %+v", a)
	}
	m.AutoUpgrade = ""
	st.UpdateMovie(ctx, m)
	withFile(store.Movie{TMDBID: 801, Title: "Third", Year: 2002, ProfileID: m.ProfileID, RootFolder: "/m", Path: "/m/Third", Monitored: true}, "")
	before := movieSearches()
	s.jobWanted(ctx, "{}")
	if movieSearches() != before {
		t.Fatalf("searched past the month's limit: %v", *asked)
	}
	if _, err := s.grab(automatic(ctx), res.Releases[0]); err == nil || !strings.Contains(err.Error(), "used up") {
		t.Fatalf("grab past the limit: %v", err)
	}
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	if r := s.SyncRSS(ctx); len(r.Grabbed) != 0 || len(r.Problems) != 0 {
		t.Fatalf("rss past the limit %+v", r)
	}
	if left, used, _ := s.UpgradesLeft(ctx, AutoSettings{UpgradesPerMonth: 3}); left != 2 || used != 1 {
		t.Fatalf("left %d used %d", left, used)
	}
}

// Searches Box Office or Matinee start for a movie that has a copy (a
// title just added, Matinee's search command) follow the automatic
// upgrade rules; one a person starts in Box Office doesn't.
func TestMovieSearchJobFollowsUpgradeRules(t *testing.T) {
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(item("The.Matrix.1999.1080p.WEB-DL-GRP", "m1", 7e9))
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: m.Path + "/x.mkv", Quality: 9})
	job := fmt.Sprintf(`{"id":%d}`, m.ID)

	r, err := s.jobMovie(ctx, job)
	if err != nil || r.Status != "skipped" || !strings.Contains(r.Message, "automatic upgrades are off") || len(*asked) != 0 {
		t.Fatalf("upgrades off: %+v %v, asked %v", r, err, *asked)
	}
	SaveAutoSettings(ctx, st, AutoSettings{Enabled: true, PerDay: DefaultPerDay, RSS: true, RSSMinutes: DefaultRSSMinutes, Upgrades: true, UpgradesPerMonth: 1})
	st.CountAutoUpgrade(ctx)
	if r, _ = s.jobMovie(ctx, job); r.Status != "skipped" || !strings.Contains(r.Message, "1 automatic upgrade is used up") || len(*asked) != 0 {
		t.Fatalf("limit used: %+v, asked %v", r, *asked)
	}
	// By hand: searched and grabbed, and not counted.
	if r, err = s.jobMovie(ctx, fmt.Sprintf(`{"id":%d,"byHand":true}`, m.ID)); err != nil || !strings.Contains(r.Message, "grabbed") {
		t.Fatalf("by hand: %+v %v", r, err)
	}
	if q, _ := st.DownloadQueue(ctx); len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
	if n, _ := st.AutoUpgradesSince(ctx, monthStart(time.Now())); n != 1 {
		t.Fatalf("counted %d", n)
	}
}
