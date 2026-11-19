package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// Rules for new titles over the API: saved and listed with the genres to
// pick from, asked about by the Add dialog, used for a title added with no
// folder or profile, and left out for one added with them.
func TestAddRulesAPI(t *testing.T) {
	db := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	animation := []any{map[string]any{"id": 16, "name": "Animation"}}
	fake.Movies[129] = map[string]any{"id": 129, "title": "Spirited Away", "release_date": "2001-07-20", "genres": animation, "original_language": "ja"}
	fake.Movies[4935] = map[string]any{"id": 4935, "title": "Howl's Moving Castle", "release_date": "2004-11-20", "genres": animation, "original_language": "ja"}
	fake.Raw["genre/movie/list"] = map[string]any{"genres": animation}
	fake.Raw["genre/tv/list"] = map[string]any{"genres": []any{}}
	cat := &catalog.Service{Store: db, Jobs: jobs.New(db, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	ctx := context.Background()
	dir := t.TempDir()
	all, anime := filepath.Join(dir, "all"), filepath.Join(dir, "anime")
	for _, p := range []string{all, anime} {
		if _, err := db.AddRootFolder(ctx, "movie", p); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Store: db, Catalog: cat, Log: log, Version: "test", EnvAPIKey: "key"}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)

	var view addRulesView
	if code := call(t, hs, "PUT", "/api/v1/addrules", `{"rules":[{"name":"Everything","kind":"movie","rootFolder":"`+anime+`"}]}`, nil); code != 400 {
		t.Fatalf("a rule for every title: %d", code)
	}
	body := fmt.Sprintf(`{"rules":[{"name":"Anime","kind":"movie","genres":[16],"language":"ja","rootFolder":%q}]}`, anime)
	if code := call(t, hs, "PUT", "/api/v1/addrules", body, &view); code != 200 || len(view.Rules) != 1 || view.Rules[0].ID != 1 {
		t.Fatalf("save: %d %+v", code, view)
	}
	view = addRulesView{}
	if code := call(t, hs, "GET", "/api/v1/addrules", "", &view); code != 200 || len(view.Rules) != 1 || len(view.MovieGenres) != 1 || view.MovieGenres[0].Name != "Animation" {
		t.Fatalf("list: %d %+v", code, view)
	}

	var picked struct{ Rule *catalog.Rule }
	if code := call(t, hs, "GET", "/api/v1/addrules/for?kind=movie&tmdbId=129", "", &picked); code != 200 || picked.Rule == nil || picked.Rule.Name != "Anime" {
		t.Fatalf("for Spirited Away: %d %+v", code, picked)
	}
	picked.Rule = nil
	if code := call(t, hs, "GET", "/api/v1/addrules/for?kind=movie&tmdbId=603", "", &picked); code != 200 || picked.Rule != nil {
		t.Fatalf("for The Matrix: %d %+v", code, picked)
	}
	if code := call(t, hs, "GET", "/api/v1/addrules/for?kind=book&tmdbId=1", "", nil); code != 400 {
		t.Fatalf("for a book: %d", code)
	}

	// No folder sent: the rule picks. A folder sent: a person picked.
	var m store.Movie
	if code := call(t, hs, "POST", "/api/v1/movies", `{"tmdbId":129,"monitored":true}`, &m); code != 201 || m.RootFolder != anime {
		t.Fatalf("add with no folder: %d %+v", code, m)
	}
	if code := call(t, hs, "POST", "/api/v1/movies", fmt.Sprintf(`{"tmdbId":4935,"monitored":true,"rootFolder":%q}`, all), &m); code != 201 || m.RootFolder != all {
		t.Fatalf("add with a folder: %d %+v", code, m)
	}
}
