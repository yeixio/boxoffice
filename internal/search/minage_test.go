package search

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// comeDue makes every held release due now, as if the wait were over.
func comeDue(t *testing.T, st *store.Store) []store.HeldRelease {
	t.Helper()
	ctx := context.Background()
	held, err := st.HeldReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range held {
		if err := st.HoldRelease(ctx, h.Key, h.Title, h.Data, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return held
}

func TestRSSHoldsNewReleases(t *testing.T) {
	ago := time.Now().Add(-10 * time.Minute)
	var mu sync.Mutex
	items := []string{posted("Show.S01E01.1080p.WEB-DL-NTb", "e1", 2e9, ago)}
	srv, _ := fakeIndexer(t, func(*http.Request) (int, string) {
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
	show, eps := showWithEpisodes(t, st, m.ProfileID, 1)

	// Posted 10 minutes ago, and Box Office waits 30 by default: held.
	res := s.SyncRSS(ctx)
	if len(res.Grabbed) != 0 {
		t.Fatalf("grabbed a new release at once %+v", res)
	}
	held, _ := st.HeldReleases(ctx)
	if len(held) != 1 || held[0].Title != "Show.S01E01.1080p.WEB-DL-NTb" || held[0].DueAt.Sub(ago.Truncate(time.Second)) != 30*time.Minute {
		t.Fatalf("held %+v", held)
	}
	// Not due yet: nothing happens, and it's kept.
	if got, _ := s.TakeHeld(ctx); len(got) != 0 {
		t.Fatalf("took one not due %v", got)
	}
	if held, _ = st.HeldReleases(ctx); len(held) != 1 {
		t.Fatalf("forgot one not due %+v", held)
	}

	// Once it's old enough, it's judged again and grabbed, with no
	// search. (The wait is shortened rather than waited out.)
	a, _ := LoadAutoSettings(ctx, st)
	a.MinAgeMinutes = 10
	SaveAutoSettings(ctx, st, a)
	comeDue(t, st)
	got, problems := s.TakeHeld(ctx)
	if !slices.Equal(got, []string{"Show.S01E01.1080p.WEB-DL-NTb"}) {
		t.Fatalf("took %v (%v)", got, problems)
	}
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 1 || q[0].TitleID != show.ID || !slices.Equal(q[0].EpisodeIDs, []int64{eps[0].ID}) {
		t.Fatalf("queue %+v", q)
	}
	if held, _ = st.HeldReleases(ctx); len(held) != 0 {
		t.Fatalf("still held %+v", held)
	}

	// With the wait at 0, a release is grabbed as soon as it's seen.
	st.DeleteDownload(ctx, q[0].ID)
	a.MinAgeMinutes = 0
	SaveAutoSettings(ctx, st, a)
	if a, _ = LoadAutoSettings(ctx, st); a.MinAgeMinutes != 0 {
		t.Fatalf("saved %d", a.MinAgeMinutes)
	}
	st.MarkRSS(ctx, ix.ID, "", time.Time{})
	mu.Lock()
	items = []string{posted("Show.S01E01.1080p.WEB-DL-OTHER", "e1b", 2e9, time.Now())}
	mu.Unlock()
	if res = s.SyncRSS(ctx); !slices.Equal(res.Grabbed, []string{"Show.S01E01.1080p.WEB-DL-OTHER"}) {
		t.Fatalf("with no wait %+v", res)
	}
}

func TestHeldReleaseNoLongerWanted(t *testing.T) {
	srv, _ := fakeIndexer(t, func(*http.Request) (int, string) {
		return 200, feedOf(posted("Show.S01E01.1080p.WEB-DL-NTb", "e1", 2e9, time.Now().Add(-5*time.Minute)))
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	_, eps := showWithEpisodes(t, st, m.ProfileID, 1)
	s.SyncRSS(ctx)
	// The episode came in another way meanwhile: nothing's grabbed, and
	// the held release is forgotten.
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: "/tv/Show/Season 01/e1.mkv", Quality: 10})
	st.SetSetting(ctx, SettingMinAge, "1")
	if held := comeDue(t, st); len(held) != 1 {
		t.Fatalf("held %+v", held)
	}
	if got, _ := s.TakeHeld(ctx); len(got) != 0 {
		t.Fatalf("took %v", got)
	}
	if held, _ := st.HeldReleases(ctx); len(held) != 0 {
		t.Fatalf("still held %+v", held)
	}
}

func TestSearchHoldsNewReleases(t *testing.T) {
	srv, _ := indexer(t,
		posted("The.Matrix.1999.1080p.BluRay.x264-NEW", "n", 9e9, time.Now().Add(-2*time.Minute)),
		item("The.Matrix.1999.720p.BluRay.x264-OLD", "o", 5e9), // the profile doesn't take it
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	// The movie isn't out by its dates (it has none), so only the search
	// when it was added would grab it: the held release still is.

	// Someone looking at the releases sees it as one to take, with when
	// Box Office would take it by itself; it isn't held.
	res, err := s.Movie(Browsing(ctx), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Releases[0]; !c.Accepted || c.NewUntil.IsZero() || c.held {
		t.Fatalf("browsing %+v", c)
	}
	if held, _ := st.HeldReleases(ctx); len(held) != 0 {
		t.Fatalf("held while browsing %+v", held)
	}
	// The search when a movie is added waits, and the search counts as
	// having found it, so the next one isn't put off.
	r, err := s.jobMovie(ctx, fmt.Sprintf(`{"id":%d}`, m.ID))
	if err != nil || r.Status != "done" || !strings.Contains(r.Message, "held back") {
		t.Fatalf("job %+v %v", r, err)
	}
	if q, _ := st.DownloadQueue(ctx); len(q) != 0 {
		t.Fatalf("grabbed %+v", q)
	}
	if done, _ := st.Searched(ctx, "movie"); done[m.ID].Misses != 0 {
		t.Fatalf("searched %+v", done[m.ID])
	}
	held, _ := st.HeldReleases(ctx)
	if len(held) != 1 || held[0].Title != "The.Matrix.1999.1080p.BluRay.x264-NEW" {
		t.Fatalf("held %+v", held)
	}

	// Its indexer gave no ids, and the feed matching finds the movie by
	// the one it was found for.
	st.SetSetting(ctx, SettingMinAge, "1")
	comeDue(t, st)
	if got, problems := s.TakeHeld(ctx); !slices.Equal(got, []string{"The.Matrix.1999.1080p.BluRay.x264-NEW"}) {
		t.Fatalf("took %v (%v)", got, problems)
	}

	// Grabbing a new release by hand doesn't wait.
	q, _ := st.DownloadQueue(ctx)
	st.DeleteDownload(ctx, q[0].ID)
	st.SetSetting(ctx, SettingMinAge, "")
	if res, err = s.Movie(Browsing(ctx), m.ID); err != nil || res.Releases[0].NewUntil.IsZero() {
		t.Fatalf("browsing again %+v %v", res, err)
	}
	if _, err := s.Grab(ctx, res.Releases[0].Key); err != nil {
		t.Fatalf("grab by hand: %v", err)
	}
	if q, _ = st.DownloadQueue(ctx); len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
}

func TestMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "less than a minute", time.Minute: "1 minute", 30 * time.Minute: "30 minutes",
		time.Hour: "1 hour", 2 * time.Hour: "2 hours", 90 * time.Minute: "90 minutes",
	} {
		if got := minutes(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
