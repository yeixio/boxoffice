package arrapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/matinee/internal/arr"
	"github.com/yeixio/matinee/internal/boxoffice/arrapi"
	"github.com/yeixio/matinee/internal/boxoffice/catalog"
	"github.com/yeixio/matinee/internal/boxoffice/jobs"
	"github.com/yeixio/matinee/internal/boxoffice/store"
	"github.com/yeixio/matinee/internal/boxoffice/store/storetest"
	"github.com/yeixio/matinee/internal/boxoffice/tmdb/tmdbtest"
)

const key = "box-office-key"

type env struct {
	cat    *catalog.Service
	url    string // Box Office's address
	movies string // its movie root folder
	tv     string
}

func setup(t *testing.T) env {
	t.Helper()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	cat.Register()
	dir := t.TempDir()
	e := env{cat: cat, movies: filepath.Join(dir, "movies"), tv: filepath.Join(dir, "tv")}
	ctx := context.Background()
	for kind, p := range map[string]string{"movie": e.movies, "series": e.tv} {
		os.MkdirAll(p, 0o755)
		if _, err := st.AddRootFolder(ctx, kind, p); err != nil {
			t.Fatal(err)
		}
	}
	keyFn := func(context.Context) string { return key }
	mux := http.NewServeMux()
	mux.Handle("/radarr/", http.StripPrefix("/radarr", arrapi.New(arrapi.Radarr, cat, keyFn, "test", log)))
	mux.Handle("/sonarr/", http.StripPrefix("/sonarr", arrapi.New(arrapi.Sonarr, cat, keyFn, "test", log)))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	e.url = ts.URL
	return e
}

// Matinee's own Radarr client, pointed at Box Office, does everything it
// does with Radarr.
func TestMatineeRadarrClient(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := arr.New(arr.Radarr, arr.Config{URL: e.url + "/radarr", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := c.Status(ctx); err != nil || s.AppName != "Radarr" {
		t.Fatalf("status: %+v, %v", s, err)
	}
	// The wrong slot is caught, as with the real apps.
	wrong, _ := arr.New(arr.Sonarr, arr.Config{URL: e.url + "/radarr", APIKey: key})
	if _, err := wrong.Status(ctx); err == nil {
		t.Fatal("a Sonarr client accepted the Radarr API")
	}
	bad, _ := arr.New(arr.Radarr, arr.Config{URL: e.url + "/radarr", APIKey: "nope"})
	if _, err := bad.Status(ctx); err == nil {
		t.Fatal("a wrong key was accepted")
	}
	profiles, err := c.QualityProfiles(ctx)
	if err != nil || len(profiles) != 3 {
		t.Fatalf("profiles: %+v, %v", profiles, err)
	}
	folders, err := c.RootFolders(ctx)
	if err != nil || len(folders) != 1 || folders[0].Path != e.movies {
		t.Fatalf("folders: %+v, %v", folders, err)
	}
	found, err := c.Search(ctx, "matrix")
	if err != nil || len(found) != 1 || found[0].TMDBID != 603 || found[0].ID != 0 || found[0].Poster == "" {
		t.Fatalf("search: %+v, %v", found, err)
	}
	added, err := c.Add(ctx, 603, arr.AddOptions{QualityProfileID: profiles[1].ID, RootFolder: folders[0].Path})
	if err != nil || added.ID == 0 || added.QualityProfileID != profiles[1].ID {
		t.Fatalf("add: %+v, %v", added, err)
	}
	// Adding again leaves it as it is.
	again, err := c.Add(ctx, 603, arr.AddOptions{QualityProfileID: profiles[0].ID, RootFolder: folders[0].Path})
	if err != nil || again.ID != added.ID || again.QualityProfileID != profiles[1].ID {
		t.Fatalf("add again: %+v, %v", again, err)
	}
	if got, err := c.Find(ctx, 603, "", 0); err != nil || got.ID != added.ID {
		t.Fatalf("find: %+v, %v", got, err)
	}
	if got, err := c.Find(ctx, 0, "The Matrix", 1999); err != nil || got.ID != added.ID {
		t.Fatalf("find by name: %+v, %v", got, err)
	}
	if err := c.SetQualityProfile(ctx, added.ID, profiles[2].ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Find(ctx, 603, "", 0); got.QualityProfileID != profiles[2].ID {
		t.Fatalf("profile not changed: %+v", got)
	}
	m, _ := e.cat.Store.MovieByTMDB(ctx, 603)
	if m.Path != filepath.Join(e.movies, "The Matrix (1999)") || m.InCinemas == nil || m.PhysicalRelease == nil {
		t.Fatalf("stored: %+v", m)
	}

	// Replace file: the bad copy moves aside, and a search is queued.
	os.MkdirAll(m.Path, 0o755)
	file := filepath.Join(m.Path, "The Matrix (1999).mkv")
	os.WriteFile(file, []byte("bad"), 0o644)
	if _, err := e.cat.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: file, Size: 3, Quality: 10}); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Find(ctx, 603, "", 0)
	if !got.HasFile || got.FileID == 0 {
		t.Fatalf("file not seen: %+v", got)
	}
	if err := c.ReplaceMovie(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the bad file is still in the library")
	}
	recycled, _ := filepath.Glob(filepath.Join(e.movies, ".recycle", "*", "The Matrix (1999).mkv"))
	if len(recycled) != 1 {
		t.Fatal("the bad file wasn't kept in the recycle folder")
	}
	if got, _ := c.Find(ctx, 603, "", 0); got.HasFile {
		t.Fatal("still has a file")
	}
	js, _ := e.cat.Store.Jobs(ctx, 10)
	if len(js) == 0 || js[0].Kind != catalog.JobSearchMovie {
		t.Fatalf("no search queued: %+v", js)
	}

	// Coming soon: the movie out in a week.
	if _, err := c.Add(ctx, 1001, arr.AddOptions{QualityProfileID: profiles[0].ID, RootFolder: folders[0].Path}); err != nil {
		t.Fatal(err)
	}
	up, err := c.Calendar(ctx, time.Now(), time.Now().AddDate(0, 2, 0))
	if err != nil || len(up) != 2 || up[0].Release != "cinemas" || up[1].Release != "digital" || up[0].Title != "Coming Attraction" {
		t.Fatalf("calendar: %+v, %v", up, err)
	}
}

func TestMatineeSonarrClient(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, _ := arr.New(arr.Sonarr, arr.Config{URL: e.url + "/sonarr", APIKey: key})
	if s, err := c.Status(ctx); err != nil || s.AppName != "Sonarr" {
		t.Fatalf("status: %+v, %v", s, err)
	}
	found, err := c.Search(ctx, "severance")
	if err != nil || len(found) != 1 || found[0].TVDBID != 371980 || found[0].TMDBID != 95396 {
		t.Fatalf("search: %+v, %v", found, err)
	}
	// Matinee adds shows by TVDB id.
	added, err := c.Add(ctx, 371980, arr.AddOptions{QualityProfileID: 1, RootFolder: e.tv})
	if err != nil || added.ID == 0 {
		t.Fatalf("add: %+v, %v", added, err)
	}
	t1, _ := e.cat.Store.Series(ctx, int64(added.ID))
	if len(t1.Seasons) != 3 || t1.Seasons[0].Monitored || !t1.Seasons[1].Monitored {
		t.Fatalf("seasons: %+v", t1.Seasons)
	}
	eps, _ := e.cat.Store.Episodes(ctx, t1.ID)
	if len(eps) != 5 {
		t.Fatalf("episodes: %d", len(eps))
	}
	up, err := c.Calendar(ctx, time.Now(), time.Now().AddDate(0, 0, 14))
	if err != nil || len(up) != 1 || up[0].ShowTitle != "Severance" || *up[0].Season != 2 || *up[0].Episode != 2 {
		t.Fatalf("calendar: %+v, %v", up, err)
	}

	var e11 store.Episode
	for _, ep := range eps {
		if ep.Season == 1 && ep.Number == 1 {
			e11 = ep
		}
	}
	os.MkdirAll(t1.Path, 0o755)
	file := filepath.Join(t1.Path, "Severance S01E01.mkv")
	os.WriteFile(file, []byte("bad"), 0o644)
	e.cat.Store.AddFile(ctx, store.File{Kind: "episode", OwnerID: e11.ID, Path: file})
	if err := c.ReplaceEpisode(ctx, added.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the bad episode is still in the library")
	}
	if err := c.SetQualityProfile(ctx, added.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Find(ctx, 0, "Severance", 2022); got.QualityProfileID != 2 {
		t.Fatalf("profile: %+v", got)
	}
}
