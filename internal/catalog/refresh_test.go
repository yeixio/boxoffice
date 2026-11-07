package catalog_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// A title TMDB doesn't know any more is named in the refresh's result, and
// put off for a week, not asked about (and warned of) every day.
func TestRefreshTitleGoneFromTMDB(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	runner := jobs.New(st, log)
	cat := &catalog.Service{Store: st, Jobs: runner, Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	cat.Register()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 424242, Title: "Wizard People, Dear Reader", Year: 2004, ProfileID: profiles[0].ID, RootFolder: "/m", Path: "/m/Wizard People, Dear Reader (2004)"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetRefreshed(ctx, "movie", m.ID, time.Now().Add(-48*time.Hour))
	refresh := func() store.Job {
		t.Helper()
		if _, err := runner.Queue(ctx, catalog.JobRefreshAll, "All titles", "{}"); err != nil {
			t.Fatal(err)
		}
		runner.RunAll(ctx)
		list, _ := st.Jobs(ctx, 1)
		return list[0]
	}
	j := refresh()
	if j.Status != "done" || !strings.Contains(j.Message, "Wizard People, Dear Reader") || strings.Contains(j.Message, "couldn't be refreshed") {
		t.Fatalf("%s: %s", j.Status, j.Message)
	}
	if j = refresh(); strings.Contains(j.Message, "Wizard People") {
		t.Fatalf("asked again the next day: %s", j.Message)
	}
}
