package search

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/store"
)

// posted is item, posted at a time: fixes posted before the copy came in
// aren't taken.
func posted(title, guid string, size int64, at time.Time) string {
	return strings.Replace(item(title, guid, size), "Mon, 02 Jan 2026 15:04:05 +0000", at.Format(time.RFC1123Z), 1)
}

// fromRelease records that the copy at path was downloaded from release.
func fromRelease(t *testing.T, st *store.Store, kind string, titleID int64, eps []int64, release, path string) {
	t.Helper()
	ctx := context.Background()
	d, err := st.AddDownload(ctx, store.Download{Name: release, Kind: kind, TitleID: titleID, EpisodeIDs: eps, Source: "search"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, "")
	if err := st.SetDownloadImported(ctx, d.ID, path); err != nil {
		t.Fatal(err)
	}
}

func TestRSSTakesFixes(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	var mu sync.Mutex
	items := []string{
		// The movie's copy came from GRP's Blu-ray 1080p: another group's
		// PROPER of it is taken, a 2160p one isn't.
		posted("The.Matrix.1999.PROPER.1080p.BluRay.x264-OTHER", "m1", 9e9, soon),
		posted("The.Matrix.1999.PROPER.2160p.BluRay.x265-OTHER", "m2", 30e9, soon),
		// Episode 1 came from NTb: another group's REPACK isn't a fix of it,
		// and a 720p one isn't its quality.
		posted("Show.S01E01.REPACK.1080p.WEB-DL-GRP", "e1a", 2e9, soon),
		posted("Show.S01E01.REPACK.720p.WEB-DL-NTb", "e1b", 1e9, soon),
		// Episode 2's copy is the REPACK already.
		posted("Show.S01E02.REPACK.1080p.WEB-DL-NTb", "e2", 2e9, soon),
		// A whole season repacked isn't taken for one episode.
		posted("Show.S01.REPACK.1080p.WEB-DL-NTb", "s1", 9e9, soon),
	}
	srv, _ := fakeIndexer(t, func(*http.Request) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, feedOf(items...)
	})
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.SetSetting(ctx, SettingMinAge, "0") // they're posted just now
	ix := testIndexer(t, st)
	ix.EnableRSS = true
	st.UpdateIndexer(ctx, ix)
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})

	moviePath := m.Path + "/The Matrix (1999).mkv"
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: moviePath, Quality: 11})
	fromRelease(t, st, "movie", m.ID, nil, "The.Matrix.1999.1080p.BluRay.x264-GRP", moviePath)
	show, eps := showWithEpisodes(t, st, m.ProfileID, 2)
	for i, release := range []string{"Show.S01E01.1080p.WEB-DL-NTb", "Show.S01E02.REPACK.1080p.WEB-DL-NTb"} {
		p := show.Path + "/Season 01/Show - S01E0" + string(rune('1'+i)) + ".mkv"
		st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[i].ID, Path: p, Quality: 10})
		fromRelease(t, st, "series", show.ID, []int64{eps[i].ID}, release, p)
	}

	// Automatic upgrades are off, as they are by default: fixes are taken
	// anyway, and aren't counted as upgrades.
	res := s.SyncRSS(ctx)
	if res.Skipped != "" || !slices.Equal(res.Grabbed, []string{"The.Matrix.1999.PROPER.1080p.BluRay.x264-OTHER"}) {
		t.Fatalf("sync %+v", res)
	}
	q, _ := st.DownloadQueue(ctx)
	if len(q) != 1 || q[0].Kind != "movie" || q[0].TitleID != m.ID || q[0].Quality != 11 {
		t.Fatalf("queue %+v", q)
	}
	if n, _ := st.AutoUpgradesSince(ctx, monthStart(time.Now())); n != 0 {
		t.Fatalf("counted %d upgrades", n)
	}
	st.DeleteDownload(ctx, q[0].ID)

	// The same repack from the episode's own group is taken; with fixes
	// turned off, it wouldn't be.
	mu.Lock()
	items = []string{posted("Show.S01E01.REPACK.1080p.WEB-DL-NTb", "e1c", 2e9, soon.Add(time.Minute))}
	mu.Unlock()
	a, _ := LoadAutoSettings(ctx, st)
	a.Propers = false
	SaveAutoSettings(ctx, st, a)
	if res = s.SyncRSS(ctx); len(res.Grabbed) != 0 {
		t.Fatalf("with fixes off %+v", res)
	}
	a.Propers = true
	SaveAutoSettings(ctx, st, a)
	st.MarkRSS(ctx, ix.ID, "", time.Time{})
	mu.Lock()
	items = []string{posted("Show.S01E01.REPACK.1080p.WEB-DL-NTb", "e1d", 2e9, soon.Add(2*time.Minute))}
	mu.Unlock()
	if res = s.SyncRSS(ctx); !slices.Equal(res.Grabbed, []string{"Show.S01E01.REPACK.1080p.WEB-DL-NTb"}) {
		t.Fatalf("own group's repack %+v", res)
	}
	q, _ = st.DownloadQueue(ctx)
	if len(q) != 1 || !slices.Equal(q[0].EpisodeIDs, []int64{eps[0].ID}) {
		t.Fatalf("queue %+v", q)
	}
	st.DeleteDownload(ctx, q[0].ID)

	// A show whose automatic upgrades are turned off is left alone.
	show.AutoUpgrade = "off"
	if err := st.UpdateSeries(ctx, show); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	items = []string{posted("Show.S01E01.REAL.REPACK.1080p.WEB-DL-NTb", "e1e", 2e9, soon.Add(3*time.Minute))}
	mu.Unlock()
	if res = s.SyncRSS(ctx); len(res.Grabbed) != 0 {
		t.Fatalf("upgrades off for the show %+v", res)
	}
}

func TestSearchTakesFix(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	srv, _ := indexer(t,
		posted("The.Matrix.1999.1080p.BluRay.x264-GRP", "a", 9e9, soon),
		posted("The.Matrix.1999.REPACK.1080p.BluRay.x264-GRP", "b", 9e9, soon),
		posted("The.Matrix.1999.PROPER.1080p.WEB-DL-OTHER", "c", 7e9, soon),
		// Posted before the copy came in: it's that one, or was passed over.
		item("The.Matrix.1999.PROPER.1080p.BluRay.x264-OLD", "d", 9e9),
	)
	s, st, m := setup(t, srv.URL)
	ctx := context.Background()
	st.SetSetting(ctx, SettingMinAge, "0") // they're posted just now
	moviePath := m.Path + "/The Matrix (1999).mkv"
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: moviePath, Quality: 11})
	fromRelease(t, st, "movie", m.ID, nil, "The.Matrix.1999.1080p.BluRay.x264-GRP", moviePath)

	res, err := s.Movie(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var took []string
	for _, c := range res.Releases {
		if c.Accepted {
			took = append(took, c.GUID)
			if c.Fix != "REPACK" || !c.fix {
				t.Errorf("%s isn't marked a fix: %q", c.Title, c.Fix)
			}
		}
	}
	if !slices.Equal(took, []string{"b"}) {
		t.Fatalf("took %v: %+v", took, res.Releases)
	}

	// The grab is allowed with automatic upgrades off, while the data
	// cap hasn't paused them.
	downloader.SaveSettings(ctx, st, downloader.Settings{Enabled: true})
	if r, err := s.pick(automatic(ctx), res, "movie", m.ID, 0); err != nil || r.Status != "done" {
		t.Fatalf("pick %+v %v", r, err)
	}
}
