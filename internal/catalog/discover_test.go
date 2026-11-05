package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func movieResult(id int, title string) map[string]any {
	return map[string]any{"id": id, "media_type": "movie", "title": title, "release_date": "2024-05-01", "poster_path": "/p.jpg", "popularity": 10.0}
}

func showResult(id int, name string) map[string]any {
	return map[string]any{"id": id, "media_type": "tv", "name": name, "first_air_date": "2023-01-01", "popularity": 5.0}
}

func TestDiscover(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	fake.Raw["trending/all/week"] = tmdbtest.List(5, movieResult(603, "The Matrix"), showResult(95396, "Severance"),
		map[string]any{"id": 1, "media_type": "person", "name": "Somebody"}, movieResult(2000, "New One"))
	fake.Raw["movie/popular"] = tmdbtest.List(3, movieResult(2000, "New One"), movieResult(603, "The Matrix"))
	fake.Raw["tv/popular"] = tmdbtest.List(1, showResult(95396, "Severance"), showResult(3000, "Other Show"))
	fake.Raw["discover/movie"] = tmdbtest.List(1, movieResult(2000, "New One"))
	fake.Raw["discover/tv"] = tmdbtest.List(1, showResult(3000, "Other Show"))
	// Coming Attraction (1001) is on the list, so For you leaves it out.
	fake.Raw["movie/603/recommendations"] = tmdbtest.List(1, movieResult(2001, "Like It"), movieResult(1001, "Coming Attraction"), movieResult(2000, "New One"))
	fake.Raw["genre/movie/list"] = map[string]any{"genres": []any{map[string]any{"id": 28, "name": "Action"}}}
	fake.Raw["genre/tv/list"] = map[string]any{"genres": []any{map[string]any{"id": 18, "name": "Drama"}}}
	fake.Raw["network/213"] = map[string]any{"id": 213, "name": "Netflix", "logo_path": "/netflix.png"}

	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	for _, kind := range []string{"movie", "series"} {
		root := filepath.Join(t.TempDir(), kind)
		os.MkdirAll(root, 0o755)
		if _, err := st.AddRootFolder(ctx, kind, root); err != nil {
			t.Fatal(err)
		}
	}
	matrix, err := cat.AddMovie(ctx, 603, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFile(ctx, store.File{Kind: "movie", OwnerID: matrix.ID, Path: "/m/matrix.mkv", Size: 1, Quality: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.AddMovie(ctx, 1001, catalog.AddOptions{Monitored: false}); err != nil {
		t.Fatal(err)
	}
	sev, err := cat.AddSeries(ctx, 95396, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	home, err := cat.Home(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	rows := map[string]catalog.DiscoverRow{}
	for _, r := range home.Rows {
		ids = append(ids, r.ID)
		rows[r.ID] = r
	}
	want := []string{"for-you", "trending", "popular-movies", "because-movie-603", "popular-series", "upcoming-movies", "upcoming-series"}
	if !slices.Equal(ids, want) {
		t.Fatalf("rows %v, want %v", ids, want)
	}
	status := func(r catalog.DiscoverRow) map[int64]string {
		m := map[int64]string{}
		for _, it := range r.Items {
			m[it.TMDBID] = it.Status
		}
		return m
	}
	tr := rows["trending"]
	if len(tr.Items) != 3 || !tr.More {
		t.Fatalf("trending: %+v", tr)
	}
	if s := status(tr); s[603] != catalog.StatusAvailable || s[95396] != catalog.StatusWanted || s[2000] != "" {
		t.Fatalf("trending status: %v", s)
	}
	if tr.Items[0].ID != matrix.ID || tr.Items[1].Kind != "series" {
		t.Fatalf("trending items: %+v", tr.Items)
	}
	var fy []int64
	for _, it := range rows["for-you"].Items {
		fy = append(fy, it.TMDBID)
	}
	if !slices.Equal(fy, []int64{2001, 2000}) {
		t.Fatalf("for you: %v", fy)
	}
	if b := rows["because-movie-603"]; b.Title != "Because you have The Matrix" || b.Because != "The Matrix" || len(b.Items) != 2 {
		t.Fatalf("because: %+v", b)
	}
	if len(home.Browse.Networks) != 1 || home.Browse.Networks[0].Name != "Netflix" || home.Browse.Networks[0].Logo == "" {
		t.Fatalf("networks: %+v", home.Browse.Networks)
	}
	if len(home.Browse.MovieGenres) != 1 || len(home.Browse.SeriesGenres) != 1 {
		t.Fatalf("genres: %+v", home.Browse)
	}

	// Lists are kept a while; where titles stand is not.
	eps, err := st.Episodes(ctx, sev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[1].ID, Path: "/tv/s1e1.mkv", Size: 1, Quality: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Home(ctx); err != nil {
		t.Fatal(err)
	}
	if n := fake.Hits("trending/all/week"); n != 1 {
		t.Fatalf("trending fetched %d times", n)
	}
	p, err := cat.Row(ctx, "trending", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s := status(p.DiscoverRow); s[95396] != catalog.StatusPartial {
		t.Fatalf("partly on disk: %v", s)
	}

	p, err = cat.Row(ctx, "popular-movies", 2)
	if err != nil || p.Page != 2 || p.Pages != 3 || !p.More {
		t.Fatalf("page 2: %+v, %v", p, err)
	}
	if p, err = cat.Row(ctx, "genre-movie-28", 1); err != nil || p.Title != "Action movies" {
		t.Fatalf("genre: %+v, %v", p, err)
	}
	if p, err = cat.Row(ctx, "network-213", 1); err != nil || p.Title != "Shows on Netflix" {
		t.Fatalf("network: %+v, %v", p, err)
	}
	for _, id := range []string{"genre-movie-99", "network-999", "because-movie-2000", "nope", "because-series-x"} {
		if _, err := cat.Row(ctx, id, 1); !errors.Is(err, catalog.ErrNoRow) {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

func TestDiscoverNeedsKey(t *testing.T) {
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log}
	if _, err := cat.Home(context.Background()); err == nil {
		t.Fatal("no key, no error")
	}
}
