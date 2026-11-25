package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func provider(id int, name string, priority int) map[string]any {
	return map[string]any{"provider_id": id, "provider_name": name, "logo_path": "/" + name + ".png", "display_priority": priority}
}

// Where to watch: a show's networks, and the services in the country
// asked for, kept so a second look doesn't ask TMDB again.
func TestWhereToWatch(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	fake.Shows[95396]["networks"] = []any{map[string]any{"id": 2552, "name": "Apple TV+", "logo_path": "/atv.png"}}
	fake.Shows[95396]["watch/providers"] = map[string]any{"results": map[string]any{
		"US": map[string]any{"link": "https://www.themoviedb.org/tv/95396/watch?locale=US",
			"flatrate": []any{provider(350, "Apple TV Plus", 5)},
			"buy":      []any{provider(2, "Apple TV", 3), provider(3, "Google Play", 1)}},
		"GB": map[string]any{"flatrate": []any{provider(350, "Apple TV Plus", 5)}},
		"FR": map[string]any{}, // nothing there
	}}
	fake.Raw["movie/603/watch/providers"] = map[string]any{"results": map[string]any{
		"DE": map[string]any{"link": "https://www.themoviedb.org/movie/603/watch?locale=DE",
			"free": []any{provider(9, "Pluto", 2)}, "ads": []any{provider(9, "Pluto", 2), provider(10, "Freevee", 1)},
			"rent": []any{provider(2, "Apple TV", 1)}},
	}}
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}

	w, err := cat.Watch(ctx, "series", 95396, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Networks) != 1 || w.Networks[0].Name != "Apple TV+" || w.Networks[0].Logo == "" {
		t.Fatalf("networks: %+v", w.Networks)
	}
	if w.Country != "US" || len(w.Stream) != 1 || w.Stream[0].Name != "Apple TV Plus" || w.Link == "" {
		t.Fatalf("US: %+v", w)
	}
	// JustWatch's order, not TMDB's.
	if len(w.Buy) != 2 || w.Buy[0].Name != "Google Play" {
		t.Fatalf("buy: %+v", w.Buy)
	}
	if !slices.Equal(w.Countries, []string{"GB", "US"}) {
		t.Fatalf("countries: %v", w.Countries)
	}
	// Another country is the same answer from TMDB, kept.
	gb, err := cat.Watch(ctx, "series", 95396, "gb")
	if err != nil || gb.Country != "GB" || len(gb.Stream) != 1 || len(gb.Buy) != 0 {
		t.Fatalf("GB: %+v %v", gb, err)
	}
	if n := fake.Hits("tv/95396"); n != 1 {
		t.Fatalf("asked TMDB %d times", n)
	}
	// Nowhere to watch it here, but the network is still said.
	fr, err := cat.Watch(ctx, "series", 95396, "FR")
	if err != nil || !fr.Empty() || len(fr.Networks) != 1 {
		t.Fatalf("FR: %+v %v", fr, err)
	}

	// Free and with ads are one list, each service once.
	m, err := cat.Watch(ctx, "movie", 603, "DE")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Networks) != 0 || len(m.Free) != 2 || m.Free[0].Name != "Freevee" || m.Free[1].Name != "Pluto" || len(m.Rent) != 1 {
		t.Fatalf("movie: %+v", m)
	}

	if _, err := cat.Watch(ctx, "album", 1, ""); !errors.Is(err, catalog.ErrNoKind) {
		t.Fatalf("album: %v", err)
	}
}
