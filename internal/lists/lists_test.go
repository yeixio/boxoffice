package lists_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/lists"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func TestPlace(t *testing.T) {
	for address, want := range map[string]string{
		"https://www.themoviedb.org/list/8136-best-of":          "tmdb",
		"themoviedb.org/list/8136":                              "tmdb",
		"https://trakt.tv/users/sam/watchlist":                  "trakt",
		"https://app.trakt.tv/users/sam/lists/heist-films/":     "trakt",
		"https://rss.plex.tv/0a1b2c3d-0000-1111-2222-333344445": "plex",
		"https://www.themoviedb.org/movie/603":                  "",
		"http://rss.plex.tv/abc":                                "",
		"https://rss.plex.tv.example.com/abc":                   "",
		"https://www.imdb.com/list/ls000000001/":                "",
		"https://app.plex.tv/desktop/#!/watchlist":              "",
		"not an address at all":                                 "",
		"":                                                      "",
	} {
		p, err := lists.Place(address)
		var le *lists.Error
		if want == "" && !errors.As(err, &le) {
			t.Errorf("%q: got %+v, %v; want a refusal that says why", address, p, err)
		}
		if p.Source != want {
			t.Errorf("%q: source %q, want %q", address, p.Source, want)
		}
	}
}

type world struct {
	ctx   context.Context
	st    *store.Store
	fake  *tmdbtest.Fake
	svc   *lists.Service
	trakt map[string]string // path → JSON
	plex  string
}

func setup(t *testing.T) *world {
	t.Helper()
	w := &world{ctx: context.Background(), st: storetest.Open(t), fake: tmdbtest.New(t), trakt: map[string]string{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := &catalog.Service{Store: w.st, Jobs: jobs.New(w.st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: w.fake.URL}
	for _, kind := range []string{"movie", "series"} {
		root := filepath.Join(t.TempDir(), kind)
		os.MkdirAll(root, 0o755)
		if _, err := w.st.AddRootFolder(w.ctx, kind, root); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed" {
			io.WriteString(rw, w.plex)
			return
		}
		if r.Header.Get("trakt-api-key") != "client" || r.Header.Get("trakt-api-version") != "2" {
			rw.WriteHeader(http.StatusForbidden)
			return
		}
		body, ok := w.trakt[r.URL.Path]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(rw, body)
	}))
	t.Cleanup(ts.Close)
	w.svc = &lists.Service{Store: w.st, Catalog: cat, Log: log, TraktBase: ts.URL, PlexFeed: func(string) string { return ts.URL + "/feed" }}
	return w
}

func (w *world) states(t *testing.T, id int64) string {
	t.Helper()
	items, err := w.st.ListItems(w.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Title+"="+it.State)
	}
	// Newest first, then by title: sort for a steady answer.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return strings.Join(out, " ")
}

func tmdbList(items ...map[string]any) map[string]any {
	return map[string]any{"name": "Best of", "items": items, "total_pages": 1}
}

var (
	matrix    = map[string]any{"id": 603, "media_type": "movie", "title": "The Matrix", "release_date": "1999-03-30"}
	coming    = map[string]any{"id": 1001, "media_type": "movie", "title": "Coming Attraction", "release_date": "2030-01-01"}
	severance = map[string]any{"id": 95396, "media_type": "tv", "name": "Severance", "first_air_date": "2022-02-18"}
)

func TestTMDBListAddsOnceAndNotAgainAfterRemoval(t *testing.T) {
	w := setup(t)
	w.fake.Raw["list/8136"] = tmdbList(matrix, severance)
	pre, err := w.svc.Look(w.ctx, "themoviedb.org/list/8136-best-of")
	if err != nil || pre.Source != "tmdb" || pre.Name != "Best of" || pre.Movies != 1 || pre.Shows != 1 {
		t.Fatalf("look: %+v, %v", pre, err)
	}
	l, err := w.svc.Follow(w.ctx, store.List{Name: "Best of", Address: pre.Address, Enabled: true, Movies: true, Shows: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Follow(w.ctx, store.List{Name: "Again", Address: pre.Address, Movies: true}); err == nil {
		t.Fatal("the same list was followed twice")
	}
	res, err := w.svc.Check(w.ctx, l.ID)
	if err != nil || res.Added != 2 {
		t.Fatalf("check: %+v, %v", res, err)
	}
	m, err := w.st.MovieByTMDB(w.ctx, 603)
	if err != nil || !m.Monitored {
		t.Fatalf("the movie wasn't added: %+v, %v", m, err)
	}
	if _, err := w.st.SeriesByTMDB(w.ctx, 95396); err != nil {
		t.Fatalf("the show wasn't added: %v", err)
	}
	// Removed here, it's noted and not added again, by this list or another.
	if err := w.st.DeleteMovie(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	w.fake.Raw["list/8136"] = tmdbList(matrix, severance, coming)
	if res, err := w.svc.Check(w.ctx, l.ID); err != nil || res.Added != 1 {
		t.Fatalf("second check: %+v, %v", res, err)
	}
	if got, want := w.states(t, l.ID), "Coming Attraction=added Severance=added The Matrix=removed"; got != want {
		t.Fatalf("states: %s, want %s", got, want)
	}
	if _, err := w.st.MovieByTMDB(w.ctx, 603); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the removed movie came back: %v", err)
	}
	w.fake.Raw["list/9"] = tmdbList(matrix)
	other, err := w.svc.Follow(w.ctx, store.List{Name: "Other", Address: "https://www.themoviedb.org/list/9", Enabled: true, Movies: true, Shows: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := w.svc.Check(w.ctx, other.ID); err != nil || res.Added != 0 {
		t.Fatalf("other list: %+v, %v", res, err)
	}
	// Asked for by hand, it's added after all.
	if it, err := w.svc.Decide(w.ctx, other.ID, "movie", 603, true); err != nil || it.State != store.ListAdded {
		t.Fatalf("decide: %+v, %v", it, err)
	}
	// A list that's gone says so, and keeps what it had.
	delete(w.fake.Raw, "list/8136")
	if _, err := w.svc.Check(w.ctx, l.ID); err == nil {
		t.Fatal("a missing list read fine")
	}
	if got, _ := w.st.List(w.ctx, l.ID); got.LastError == "" || got.CheckedAt == nil {
		t.Fatalf("the list doesn't say what's wrong: %+v", got)
	}
}

func TestAskFirstAndOnlyShows(t *testing.T) {
	w := setup(t)
	w.fake.Raw["list/8136"] = tmdbList(matrix, severance, coming)
	l, err := w.svc.Follow(w.ctx, store.List{Name: "Ask", Address: "themoviedb.org/list/8136", Enabled: true, Ask: true, Movies: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := w.svc.Check(w.ctx, l.ID); err != nil || res.Added != 0 || res.Waiting != 2 {
		t.Fatalf("check: %+v, %v", res, err)
	}
	if got, want := w.states(t, l.ID), "Coming Attraction=waiting The Matrix=waiting"; got != want {
		t.Fatalf("states: %s, want %s", got, want)
	}
	if it, err := w.svc.Decide(w.ctx, l.ID, "movie", 603, true); err != nil || it.State != store.ListAdded {
		t.Fatalf("yes: %+v, %v", it, err)
	}
	if it, err := w.svc.Decide(w.ctx, l.ID, "movie", 1001, false); err != nil || it.State != store.ListSkipped {
		t.Fatalf("no: %+v, %v", it, err)
	}
	if res, err := w.svc.Check(w.ctx, l.ID); err != nil || res.Added != 0 || res.Waiting != 0 {
		t.Fatalf("check again: %+v, %v", res, err)
	}
	if _, err := w.st.MovieByTMDB(w.ctx, 1001); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a movie said no to was added")
	}
}

func TestTraktAndPlex(t *testing.T) {
	w := setup(t)
	title := func(kind, name string, ids map[string]any) map[string]any {
		return map[string]any{"type": kind, kind: map[string]any{"title": name, "year": 2000, "ids": ids}}
	}
	movies, _ := json.Marshal([]any{title("movie", "The Matrix", map[string]any{"tmdb": 603, "imdb": "tt0133093"})})
	shows, _ := json.Marshal([]any{title("show", "Severance", map[string]any{"tvdb": 371980})})
	w.trakt["/users/sam/watchlist/movies"], w.trakt["/users/sam/watchlist/shows"] = string(movies), string(shows)
	w.fake.Raw["find/371980"] = map[string]any{"tv_results": []any{map[string]any{"id": 95396}}}
	w.fake.Raw["find/tt0133093"] = map[string]any{"movie_results": []any{map[string]any{"id": 603}}}

	// Without a client ID, Trakt says what's needed.
	if _, err := w.svc.Look(w.ctx, "https://trakt.tv/users/sam/watchlist"); !errors.Is(err, lists.ErrNoTraktKey) {
		t.Fatalf("no key: %v", err)
	}
	w.st.SetSetting(w.ctx, lists.SettingTraktClient, "client")
	pre, err := w.svc.Look(w.ctx, "https://trakt.tv/users/sam/watchlist")
	if err != nil || pre.Movies != 1 || pre.Shows != 1 {
		t.Fatalf("trakt look: %+v, %v", pre, err)
	}
	l, err := w.svc.Follow(w.ctx, store.List{Name: "Sam", Address: pre.Address, Enabled: true, Movies: true, Shows: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := w.svc.Check(w.ctx, l.ID); err != nil || res.Added != 2 {
		t.Fatalf("trakt check: %+v, %v", res, err)
	}
	if _, err := w.svc.Look(w.ctx, "https://trakt.tv/users/sam/lists/nope"); err == nil {
		t.Fatal("a list Trakt hasn't got read fine")
	}

	// Plex's feed names titles by IMDb's or TheTVDB's id. Both are here
	// already, so they're noted and nothing is added.
	w.plex = `<?xml version="1.0"?><rss><channel><title>Watchlist</title>
		<item><title>The Matrix (1999)</title><guid isPermaLink="false">imdb://tt0133093</guid><category>movie</category></item>
		<item><title>Severance (2022)</title><guid isPermaLink="false">tvdb://371980</guid><category>show</category></item>
		<item><title>Mystery</title><guid>plex://movie/abc</guid><category>movie</category></item>
	</channel></rss>`
	p, err := w.svc.Follow(w.ctx, store.List{Name: "Plex", Address: "https://rss.plex.tv/0a1b2c3d", Enabled: true, Movies: true, Shows: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := w.svc.Check(w.ctx, p.ID); err != nil || res.Added != 0 {
		t.Fatalf("plex check: %+v, %v", res, err)
	}
	if got, want := w.states(t, p.ID), "Severance=had The Matrix=had"; got != want {
		t.Fatalf("plex states: %s, want %s", got, want)
	}
}
