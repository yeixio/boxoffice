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

// A daily show's releases are named by the day they aired: one is read
// as the episode that aired then, and the episode is asked for by its
// day as well as by season and episode.
func TestDailyShow(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n) }
	srv, asked := fakeIndexer(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Query().Get("ep"), "/") {
			return 200, feedOf(
				item("The.Nightly.2026.09.11.Some.Guest.1080p.WEB-DL-GRP", "day", 1e9),
				item("The.Nightly.2026.09.12.Other.Guest.1080p.WEB-DL-GRP", "next", 1e9),
				item("The.Nightly.2025.01.01.1080p.WEB-DL-GRP", "never", 1e9),
			)
		}
		return 200, feedOf(item("The.Nightly.S01E11.1080p.WEB-DL-GRP", "code", 1e9))
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 7, TVDBID: 77, Title: "The Nightly", ProfileID: m.ProfileID, RootFolder: "/tv", Path: "/tv/The Nightly",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 45; n++ {
		aired := day(n - 1)
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, AirDate: &aired, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	want := episodeOf(eps, 1, 11) // aired 2026-09-11

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
	if why["day"] != "" || why["code"] != "" {
		t.Fatalf("turned down: %v", why)
	}
	if !strings.Contains(why["next"], "it's episode 12") || !strings.Contains(why["never"], "doesn't say which episode") {
		t.Fatalf("reasons %v", why)
	}
	qs := *asked
	if len(qs) != 2 || !strings.Contains(qs[0], "ep=11") || !strings.Contains(qs[1], "season=2026") || !strings.Contains(qs[1], "ep=09%2F11") {
		t.Fatalf("asked %v", qs)
	}

	// A show of ordinary seasons isn't asked for by day.
	n := len(*asked)
	other, oeps := animeShow(t, st, m.ProfileID)
	_ = other
	if _, err := s.Episode(ctx, episodeOf(oeps, 1, 1).ID); err != nil {
		t.Fatal(err)
	}
	if got := (*asked)[n:]; len(got) != 1 {
		t.Fatalf("asked %v", got)
	}
}

// The RSS sync takes a daily show's release named by date for the wanted
// episode that aired that day.
func TestRSSDailyShow(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	name := func(d time.Time) string { return "The.Nightly." + d.Format("2006.01.02") + ".1080p.WEB-DL-GRP" }
	srv, _ := fakeIndexer(t, func(r *http.Request) (int, string) {
		return 200, feedOf(
			item(name(today.AddDate(0, 0, -2)), "wanted", 2e9),
			item(name(today.AddDate(0, 0, -400)), "never", 2e9),
			item("Another.Nightly."+today.AddDate(0, 0, -2).Format("2006.01.02")+".1080p.WEB-DL-GRP", "other", 2e9),
		)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 7, TVDBID: 77, Title: "The Nightly", ProfileID: m.ProfileID, RootFolder: "/tv", Path: "/tv/The Nightly",
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		aired := today.AddDate(0, 0, n-4)
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, AirDate: &aired, Monitored: true})
	}
	res := s.SyncRSS(ctx)
	if res.Skipped != "" || len(res.Grabbed) != 1 || res.Grabbed[0] != name(today.AddDate(0, 0, -2)) {
		t.Fatalf("sync %+v", res)
	}
	q, _ := st.DownloadQueue(ctx)
	eps, _ := st.Episodes(ctx, show.ID)
	if len(q) != 1 || len(q[0].EpisodeIDs) != 1 || q[0].EpisodeIDs[0] != episodeOf(eps, 1, 2).ID {
		t.Fatalf("queue %+v", q)
	}
}
