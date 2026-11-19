package catalog_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

// Rules for new titles pick the folder and profile by genre and language
// for titles nobody chose them for, and leave alone ones a person did.
func TestAddRules(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := tmdbtest.New(t)
	genres := func(ids ...int) []any {
		out := []any{}
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "name": "g"})
		}
		return out
	}
	fake.Movies[603]["genres"], fake.Movies[603]["original_language"] = genres(28, 878), "en"
	fake.Movies[129] = map[string]any{"id": 129, "title": "Spirited Away", "release_date": "2001-07-20", "genres": genres(16, 10751), "original_language": "ja"}
	fake.Movies[862] = map[string]any{"id": 862, "title": "Toy Story", "release_date": "1995-11-22", "genres": genres(16, 10751), "original_language": "en"}
	fake.Shows[95396]["genres"], fake.Shows[95396]["original_language"] = genres(18), "en"
	cat := &catalog.Service{Store: st, Jobs: jobs.New(st, log), Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	dir := t.TempDir()
	root := func(kind, name string) string {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		if _, err := st.AddRootFolder(ctx, kind, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	movies, anime, kids, tv := root("movie", "all"), root("movie", "anime"), root("movie", "kids"), root("series", "tv")
	ps, _ := st.Profiles(ctx)
	any, hd, uhd := ps[0], ps[1], ps[2]

	// Rules that can't work are refused, with the rule's name.
	for why, r := range map[string]catalog.Rule{
		"give the rule a name":  {Kind: "movie", Genres: []int64{16}, RootFolder: anime},
		"movies or shows":       {Name: "A", Genres: []int64{16}, RootFolder: anime},
		"every title":           {Name: "A", Kind: "movie", RootFolder: anime},
		"pick a folder":         {Name: "A", Kind: "movie", Genres: []int64{16}},
		"isn't one of the root": {Name: "A", Kind: "series", Genres: []int64{16}, RootFolder: anime},
		"there's no quality":    {Name: "A", Kind: "movie", Genres: []int64{16}, ProfileID: 999},
		"Do not touch":          {Name: "A", Kind: "movie", Genres: []int64{16}, ProfileID: ps[len(ps)-1].ID},
		"isn't a language code": {Name: "A", Kind: "movie", Language: "japanese", RootFolder: anime},
	} {
		if !ps[len(ps)-1].Locked {
			t.Fatal("the last profile isn't Do not touch")
		}
		if _, err := cat.SaveAddRules(ctx, []catalog.Rule{r}); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%+v: %v, want %q", r, err, why)
		}
	}
	saved, err := cat.SaveAddRules(ctx, []catalog.Rule{
		{Name: "Anime", Kind: "movie", Genres: []int64{16}, Language: "JA", RootFolder: anime},
		{Name: "Kids", Kind: "movie", Genres: []int64{10751, 10762}, RootFolder: kids, ProfileID: hd.ID},
		{Name: "Drama in 4K", Kind: "series", Genres: []int64{18}, ProfileID: uhd.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].ID != 1 || saved[1].ID != 2 || saved[2].ID != 3 || saved[0].Language != "ja" {
		t.Fatalf("saved %+v", saved)
	}

	// Nobody chose: the first rule that fits picks, over the folder and
	// profile a list or the request site would have used.
	m, err := cat.AddMovie(ctx, 129, catalog.AddOptions{ProfileID: any.ID, RootFolder: movies})
	if err != nil || m.RootFolder != anime || m.ProfileID != any.ID || m.Path != filepath.Join(anime, "Spirited Away (2001)") {
		t.Fatalf("anime: %+v %v", m, err)
	}
	m, err = cat.AddMovie(ctx, 862, catalog.AddOptions{})
	if err != nil || m.RootFolder != kids || m.ProfileID != hd.ID {
		t.Fatalf("kids: %+v %v", m, err)
	}
	// No rule fits: as before.
	m, err = cat.AddMovie(ctx, 603, catalog.AddOptions{})
	if err != nil || m.RootFolder != movies || m.ProfileID != any.ID {
		t.Fatalf("no rule: %+v %v", m, err)
	}
	// A rule with only a profile keeps the folder.
	s, err := cat.AddSeries(ctx, 95396, catalog.AddOptions{})
	if err != nil || s.RootFolder != tv || s.ProfileID != uhd.ID {
		t.Fatalf("show: %+v %v", s, err)
	}

	// A person chose, or the files are somewhere already: left alone.
	st.DeleteMovie(ctx, m.ID)
	for _, id := range []int64{129, 862} {
		if have, err := st.MovieByTMDB(ctx, id); err == nil {
			st.DeleteMovie(ctx, have.ID)
		}
	}
	m, err = cat.AddMovie(ctx, 129, catalog.AddOptions{ProfileID: uhd.ID, RootFolder: movies, Chosen: true})
	if err != nil || m.RootFolder != movies || m.ProfileID != uhd.ID {
		t.Fatalf("chosen: %+v %v", m, err)
	}
	m, err = cat.AddMovie(ctx, 862, catalog.AddOptions{RootFolder: movies, Path: filepath.Join(movies, "Toy Story (1995)")})
	if err != nil || m.RootFolder != movies || m.ProfileID != any.ID {
		t.Fatalf("in place: %+v %v", m, err)
	}

	// The Add dialog asks which rule would pick.
	r, ok, err := cat.RuleForTitle(ctx, "movie", 129)
	if err != nil || !ok || r.Name != "Anime" {
		t.Fatalf("rule for: %+v %v %v", r, ok, err)
	}
	if _, ok, _ := cat.RuleForTitle(ctx, "movie", 603); ok {
		t.Fatal("a rule for The Matrix")
	}

	// A rule whose folder is no longer a root folder picks only what it
	// still can; one with nothing left is passed over.
	fs, _ := st.RootFolders(ctx, "movie")
	for _, f := range fs {
		if f.Path == kids || f.Path == anime {
			if err := st.DeleteRootFolder(ctx, f.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if r, ok, _ := cat.RuleForTitle(ctx, "movie", 862); !ok || r.Name != "Kids" || r.RootFolder != "" || r.ProfileID != hd.ID {
		t.Fatalf("kids without its folder: %+v %v", r, ok)
	}
	if r, ok, _ := cat.RuleForTitle(ctx, "movie", 129); !ok || r.Name != "Kids" {
		t.Fatalf("anime without its folder: %+v %v", r, ok)
	}
}
