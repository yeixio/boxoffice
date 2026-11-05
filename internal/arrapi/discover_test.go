package arrapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Api-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == 200 && out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

// Matinee fills its Discover tab from Box Office through the connection
// it has, in place of Overseerr.
func TestDiscoverForMatinee(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.cat.AddMovie(ctx, 603, catalog.AddOptions{Monitored: true}); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"id": 603, "media_type": "movie", "title": "The Matrix", "release_date": "1999-03-30"}
	other := map[string]any{"id": 2000, "media_type": "movie", "title": "Another", "release_date": "2020-01-01"}
	e.fake.Raw["trending/all/week"] = tmdbtest.List(2, m, other)
	e.fake.Raw["movie/550/recommendations"] = tmdbtest.List(1, m, other)

	var st map[string]any
	getJSON(t, e.url+"/radarr/api/v3/system/status", &st)
	if f, _ := st["boxOfficeFeatures"].([]any); len(f) < 2 || f[1] != "discover" {
		t.Fatalf("features: %v", st["boxOfficeFeatures"])
	}
	var home catalog.DiscoverHome
	if c := getJSON(t, e.url+"/sonarr/api/v3/boxoffice/discover", &home); c != 200 || len(home.Rows) != 1 || home.Rows[0].ID != "trending" {
		t.Fatalf("home %d: %+v", c, home)
	}
	if it := home.Rows[0].Items[0]; it.Status != catalog.StatusWanted || it.ID == 0 {
		t.Fatalf("matrix: %+v", it)
	}
	// Titles like one watched, whether or not it's here, without the ones
	// on the list.
	var p catalog.DiscoverPage
	if c := getJSON(t, e.url+"/radarr/api/v3/boxoffice/discover/rows/like-movie-550", &p); c != 200 || len(p.Items) != 1 || p.Items[0].TMDBID != 2000 {
		t.Fatalf("like %d: %+v", c, p)
	}
	if c := getJSON(t, e.url+"/radarr/api/v3/boxoffice/discover/rows/nope", nil); c != 404 {
		t.Fatalf("no such row: %d", c)
	}
	var found []catalog.DiscoverTitle
	if c := getJSON(t, e.url+"/radarr/api/v3/boxoffice/discover/search?q=sever", &found); c != 200 || len(found) != 1 || found[0].Kind != "series" || found[0].Status != "" {
		t.Fatalf("search %d: %+v", c, found)
	}
	if c := getJSON(t, e.url+"/radarr/api/v3/boxoffice/discover/search?q=matrix", &found); c != 200 || len(found) != 1 || found[0].Status != catalog.StatusWanted {
		t.Fatalf("search matrix %d: %+v", c, found)
	}
}
