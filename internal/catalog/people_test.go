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
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func castCredit(id int, kind, title, character string, votes int, genres ...int) map[string]any {
	c := map[string]any{"id": id, "media_type": kind, "character": character, "vote_count": votes, "genre_ids": genres, "popularity": 1.0}
	if kind == "movie" {
		c["title"], c["release_date"] = title, "2000-01-01"
	} else {
		c["name"], c["first_air_date"] = title, "2001-01-01"
	}
	return c
}

func TestSearchByCast(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	fake.Raw["search/person"] = map[string]any{"results": []any{
		map[string]any{"id": 6384, "name": "Keanu Reeves", "known_for_department": "Acting", "profile_path": "/k.jpg", "popularity": 50.0,
			"known_for": []any{movieResult(603, "The Matrix")}},
		map[string]any{"id": 7, "name": "Keanu Nobody", "known_for_department": "Acting", "known_for": []any{}},
	}}
	cast := []any{
		castCredit(9000, "movie", "Speed", "Jack Traven", 5000),
		castCredit(603, "movie", "The Matrix", "Neo", 25000),
		castCredit(603, "movie", "The Matrix", "Neo (voice)", 25000), // twice
		castCredit(8000, "tv", "Late Show", "Himself", 900, 10767),
		castCredit(8001, "movie", "Making Of", "Self", 10),
		castCredit(9001, "tv", "Swedish Dicks", "Tex", 50),
	}
	for i := range 25 {
		cast = append(cast, castCredit(10000+i, "movie", "Small Part", "Guy", 1))
	}
	fake.Raw["person/6384"] = map[string]any{"id": 6384, "name": "Keanu Reeves", "known_for_department": "Acting", "profile_path": "/k.jpg",
		"combined_credits": map[string]any{"cast": cast}}

	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	root := filepath.Join(t.TempDir(), "movie")
	os.MkdirAll(root, 0o755)
	if _, err := st.AddRootFolder(ctx, "movie", root); err != nil {
		t.Fatal(err)
	}
	matrix, err := cat.AddMovie(ctx, 603, catalog.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}

	people, err := cat.LookupPeople(ctx, "keanu")
	if err != nil || len(people) != 1 || people[0].Name != "Keanu Reeves" || people[0].Photo == "" || !slices.Equal(people[0].KnownFor, []string{"The Matrix"}) {
		t.Fatalf("people: %+v, %v", people, err)
	}

	// Their whole name: what they're in comes first, best known first,
	// without talk shows or parts as themselves.
	found, err := cat.Search(ctx, "Keanu Reeves")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, f := range found {
		ids = append(ids, f.TMDBID)
	}
	if len(found) != 20 || !slices.Equal(ids[:3], []int64{603, 9000, 9001}) || found[0].ID != matrix.ID || found[0].Status != catalog.StatusWanted ||
		found[2].Kind != "series" {
		t.Fatalf("whole name: %v %+v", ids, found[:3])
	}

	// Part of a name finds them after titles named like it.
	fake.Movies[4242] = map[string]any{"id": 4242, "title": "Reeves", "release_date": "2010-01-01"}
	found, err = cat.Search(ctx, "reeves")
	if err != nil || len(found) < 2 || found[0].TMDBID != 4242 || found[1].TMDBID != 603 {
		t.Fatalf("part of the name: %+v, %v", found, err)
	}

	// A title that isn't anyone's name finds no one's films.
	fake.Raw["search/person"] = map[string]any{"results": []any{map[string]any{"id": 6384, "name": "Keanu Reeves", "known_for_department": "Acting",
		"known_for": []any{movieResult(603, "The Matrix")}}}}
	found, err = cat.Search(ctx, "matrix")
	if err != nil || len(found) != 1 || found[0].TMDBID != 603 {
		t.Fatalf("not a name: %+v, %v", found, err)
	}

	// Their row is everything, a page at a time.
	p, err := cat.Row(ctx, "person-6384", 1)
	if err != nil || p.Title != "Keanu Reeves" || p.Person == nil || p.Person.Photo == "" || len(p.Items) != 20 || p.Pages != 2 || !p.More {
		t.Fatalf("row: %+v, %v", p, err)
	}
	if p, err = cat.Row(ctx, "person-6384", 2); err != nil || len(p.Items) != 8 || p.More {
		t.Fatalf("row page 2: %d items, more %v, %v", len(p.Items), p.More, err)
	}
	if _, err := cat.Row(ctx, "person-1", 1); !errors.Is(err, catalog.ErrNoRow) {
		t.Fatalf("no such person: %v", err)
	}
}
