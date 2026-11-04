package migrate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFind(t *testing.T) {
	// Radarr behind a URL base: its API turns away a request without a key,
	// and anything outside the base is a 404.
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/radarr/api/v3/system/status" {
			if r.Header.Get("X-Api-Key") != "" {
				t.Error("Find sent an API key")
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer radarr.Close()
	nzbget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jsonrpc/version" {
			w.Header().Set("WWW-Authenticate", `Basic realm="NZBGet"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer nzbget.Close()
	// Something else that wants a sign-in isn't NZBGet.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="router"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer other.Close()

	m := &Migrator{Candidates: func(app string) []string {
		switch app {
		case "radarr":
			return []string{radarr.URL, radarr.URL + "/radarr"}
		case "nzbget":
			return []string{other.URL, nzbget.URL, nzbget.URL + "/nzbget"}
		}
		return []string{"http://127.0.0.1:1"}
	}}
	ctx := context.Background()
	if got := m.Find(ctx, "Radarr"); got != radarr.URL+"/radarr" {
		t.Errorf("Radarr: %q", got)
	}
	if got := m.Find(ctx, "nzbget"); got != nzbget.URL {
		t.Errorf("NZBGet: %q", got)
	}
	if got := m.Find(ctx, "sonarr"); got != "" {
		t.Errorf("Sonarr isn't running, but found %q", got)
	}
}

func TestCandidateURLs(t *testing.T) {
	got := candidateURLs("sonarr")
	for _, want := range []string{"http://sonarr:8989", "http://sonarr:8989/sonarr"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q not in %q", want, got)
		}
	}
	if candidateURLs("plex") != nil {
		t.Error("an app Box Office can't move in from has addresses")
	}
}

func TestFindPathMaps(t *testing.T) {
	dir := t.TempDir()
	mk := func(p string) { os.MkdirAll(filepath.Join(dir, p), 0o755) }
	// Box Office sees the movies at /media/movies, and a decoy folder holds
	// one of the three by chance.
	for _, m := range []string{"A (2001)", "B (2002)", "C (2003)"} {
		mk("media/movies/" + m)
	}
	mk("media/other/A (2001)")
	mk("media/tv")
	dirs := withChildren([]string{filepath.Join(dir, "media")})

	got := findPathMaps(
		[]string{"/data/media/movies", "/data/media/tv/", "/elsewhere"},
		map[string][]string{"/data/media/movies": {"/data/media/movies/A (2001)", "/data/media/movies/B (2002)", "/data/media/movies/C (2003)"}},
		dirs)
	want := []PathMap{
		{From: "/data/media/movies", To: filepath.Join(dir, "media/movies")},
		// No shows yet: matched by name.
		{From: "/data/media/tv", To: filepath.Join(dir, "media/tv")},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	// Seen at the same path already: no map.
	if got := findPathMaps([]string{filepath.Join(dir, "media/movies")}, nil, dirs); len(got) != 0 {
		t.Errorf("a visible root folder got a map: %+v", got)
	}
}

func TestPreviewFindsPathMaps(t *testing.T) {
	w := newWorld(t)
	movieDir := filepath.Join(w.media, "movies", "The Matrix (1999)")
	os.MkdirAll(movieDir, 0o755)
	os.WriteFile(filepath.Join(movieDir, "The Matrix.mkv"), []byte("x"), 0o644)
	w.m.MediaDirs = func() []string { return withChildren([]string{w.media}) }
	ts, _ := fakeRadarr(t, legacyZip(t, "radarr"))

	plan, err := w.m.Preview(context.Background(), Request{App: "radarr", URL: ts.URL, APIKey: legacyKey})
	if err != nil {
		t.Fatal(err)
	}
	want := []PathMap{{From: "/movies", To: filepath.Join(w.media, "movies")}}
	if !plan.PathMapFound || !slices.Equal(plan.PathMap, want) {
		t.Fatalf("path maps: found=%v %+v", plan.PathMapFound, plan.PathMap)
	}
	if plan.Files.Missing != 0 || plan.RootFolders[0].Path != want[0].To || !plan.RootFolders[0].Exists {
		t.Fatalf("the found map wasn't used: %+v %+v", plan.Files, plan.RootFolders)
	}

	// Maps typed in are used as they are.
	typed := []PathMap{{From: "/movies", To: "/somewhere"}}
	plan, err = w.m.Preview(context.Background(), Request{App: "radarr", URL: ts.URL, APIKey: legacyKey, PathMap: typed})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PathMapFound || !slices.Equal(plan.PathMap, typed) {
		t.Fatalf("typed maps: found=%v %+v", plan.PathMapFound, plan.PathMap)
	}
}

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/media/Plex\040Data`); got != "/media/Plex Data" {
		t.Errorf("%q", got)
	}
}
