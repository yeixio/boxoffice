package titlelist_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/jobs"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
	"github.com/yeixio/boxoffice/internal/titlelist"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

type box struct {
	st     *store.Store
	cat    *catalog.Service
	runner *jobs.Runner
	titles *titlelist.Service
	movies string
	shows  string
}

func open(t *testing.T, fake *tmdbtest.Fake) box {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := jobs.New(st, log)
	cat := &catalog.Service{Store: st, Jobs: runner, Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	b := box{st: st, cat: cat, runner: runner, titles: &titlelist.Service{Store: st, Catalog: cat, Jobs: runner}}
	b.titles.Register()
	dir := t.TempDir()
	for kind, p := range map[string]*string{"movie": &b.movies, "series": &b.shows} {
		*p = filepath.Join(dir, kind)
		os.MkdirAll(*p, 0o755)
		if _, err := st.AddRootFolder(ctx, kind, *p); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// A list saved from one Box Office and loaded into another adds the same
// titles, watched the same way, and loading it again adds nothing.
func TestSaveAndLoad(t *testing.T) {
	ctx := context.Background()
	fake := tmdbtest.New(t)
	from := open(t, fake)
	ps, _ := from.st.Profiles(ctx)
	if _, err := from.cat.AddMovie(ctx, 603, catalog.AddOptions{ProfileID: ps[1].ID, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	show, err := from.cat.AddSeries(ctx, 95396, catalog.AddOptions{Monitored: true, Monitor: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if err := from.st.SetSeason(ctx, show.ID, store.Season{Number: 2}); err != nil {
		t.Fatal(err)
	}

	list, err := titlelist.Export(ctx, from.st)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Titles) != 2 || list.Titles[0].Profile != ps[1].Name || list.Titles[0].IMDBID != "tt0133093" ||
		!reflect.DeepEqual(list.Titles[1].Seasons, []int{1}) || list.Titles[1].TVDBID != 371980 {
		t.Fatalf("saved %+v", list.Titles)
	}
	// Both ways of saving it read back the same.
	var js, sheet bytes.Buffer
	if err := list.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	if err := list.WriteCSV(&sheet); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"json": js.Bytes(), "csv": sheet.Bytes()} {
		back, err := titlelist.Read(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(back.Titles, list.Titles) {
			t.Fatalf("%s read back as\n%+v\nnot\n%+v", name, back.Titles, list.Titles)
		}
	}

	to := open(t, fake)
	back, _ := titlelist.Read(sheet.Bytes())
	plan, err := titlelist.MakePlan(ctx, to.st, back)
	if err != nil {
		t.Fatal(err)
	}
	// The folders are another computer's, so this one's are used.
	if len(plan.New) != 2 || plan.Have != 0 || len(plan.Skipped) != 0 || plan.OtherFolder != 2 || plan.OtherProfile != 0 {
		t.Fatalf("plan %+v", plan)
	}
	if ms, _ := to.st.Movies(ctx); len(ms) != 0 {
		t.Fatal("making a plan added something")
	}
	if err := to.titles.Queue(ctx, plan, false); err != nil {
		t.Fatal(err)
	}
	to.runner.RunAll(ctx)
	m, err := to.st.MovieByTMDB(ctx, 603)
	if err != nil || !m.Monitored || m.RootFolder != to.movies {
		t.Fatalf("movie %+v, %v", m, err)
	}
	if tps, _ := to.st.Profiles(ctx); m.ProfileID != tps[1].ID {
		t.Fatalf("movie's profile %d, not the one of the same name", m.ProfileID)
	}
	s, err := to.st.SeriesByTMDB(ctx, 95396)
	if err != nil || !s.Monitored || s.RootFolder != to.shows {
		t.Fatalf("show %+v, %v", s, err)
	}
	watched := map[int]bool{}
	for _, se := range s.Seasons {
		watched[se.Number] = se.Monitored
	}
	if !watched[1] || watched[2] || watched[0] {
		t.Fatalf("seasons watched %v", watched)
	}
	eps, _ := to.st.Episodes(ctx, s.ID)
	for _, e := range eps {
		if e.Monitored != (e.Season == 1) {
			t.Fatalf("season %d episode %d monitored %v", e.Season, e.Number, e.Monitored)
		}
	}

	// Again: they're here, so nothing is added, and nothing is changed.
	m.Monitored = false
	to.st.UpdateMovie(ctx, m)
	plan, _ = titlelist.MakePlan(ctx, to.st, back)
	if len(plan.New) != 0 || plan.Have != 2 {
		t.Fatalf("second plan %+v", plan)
	}
	if m, _ := to.st.MovieByTMDB(ctx, 603); m.Monitored {
		t.Fatal("loading the list again changed a title that was here")
	}
}

// A spreadsheet someone made by hand: other column names, semicolons, a
// movie known only by its IMDb id and a show only by its TVDB id.
func TestLoadHandMadeSheet(t *testing.T) {
	ctx := context.Background()
	fake := tmdbtest.New(t)
	fake.Raw["find/tt0133093"] = map[string]any{"movie_results": []any{fake.Movies[603]}}
	fake.Raw["find/371980"] = map[string]any{"tv_results": []any{fake.Shows[95396]}}
	b := open(t, fake)
	sheet := "\xef\xbb\xbfType;Name;IMDb ID;TVDB ID;MBID\r\n" +
		"Film;The Matrix;tt0133093;;\r\n" +
		"TV;Severance;;371980;\r\n" +
		"TV;Severance again;;371980;\r\n" +
		";;;;\r\n" +
		"movie;No id;;;\r\n" +
		"game;Pong;;;\r\n" +
		"artist;Daft Punk;;;056e4f3e-d505-4dad-8ec1-d04f521cbb56\r\n"
	list, err := titlelist.Read([]byte(sheet))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := titlelist.MakePlan(ctx, b.st, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.New) != 2 || plan.Counts["movie"] != 1 || plan.Counts["show"] != 1 || len(plan.Skipped) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	why := ""
	for _, s := range plan.Skipped {
		why += s.Title + ": " + s.Reason + "\n"
	}
	for _, want := range []string{"No id: it has no TMDB or IMDb id", `Pong: its kind is "game"`, "Daft Punk: music is turned off here"} {
		if !strings.Contains(why, want) {
			t.Fatalf("skipped\n%swithout %q", why, want)
		}
	}
	if !plan.New[0].Monitored {
		t.Fatal("a sheet with no monitored column should watch its titles")
	}
	if err := b.titles.Queue(ctx, plan, false); err != nil {
		t.Fatal(err)
	}
	b.runner.RunAll(ctx)
	if _, err := b.st.MovieByTMDB(ctx, 603); err != nil {
		t.Fatalf("the movie by IMDb id: %v", err)
	}
	s, err := b.st.SeriesByTMDB(ctx, 95396)
	if err != nil {
		t.Fatalf("the show by TVDB id: %v", err)
	}
	// With no seasons named, the default for new shows picked them.
	if len(s.Seasons) == 0 {
		t.Fatalf("show %+v", s)
	}
}

func TestReadRefusals(t *testing.T) {
	for data, want := range map[string]string{
		"":                              "empty",
		"{nope":                         "JSON",
		`{"format": 99, "titles": []}`:  "newer Box Office",
		`{"titles": []}`:                "no titles",
		"title,year\nThe Matrix,1999\n": `"kind"`,
		"kind,title\n":                  "no titles",
	} {
		if _, err := titlelist.Read([]byte(data)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", data, err, want)
		}
	}
}

// A title that would run as a formula in a spreadsheet is written so it
// doesn't, and reads back as it was.
func TestSheetFormulas(t *testing.T) {
	list := titlelist.File{Titles: []titlelist.Entry{{Kind: "movie", Title: "=HYPERLINK(1)", TMDBID: 1, Monitored: true},
		{Kind: "movie", Title: "+1", TMDBID: 2}, {Kind: "movie", Title: "'71", Year: 2014, TMDBID: 3}}}
	var sheet bytes.Buffer
	if err := list.WriteCSV(&sheet); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sheet.String(), "\n=") || strings.Contains(sheet.String(), ",=") || !strings.Contains(sheet.String(), "'=HYPERLINK") {
		t.Fatalf("sheet\n%s", sheet.String())
	}
	back, err := titlelist.Read(sheet.Bytes())
	if err != nil || !reflect.DeepEqual(back.Titles, list.Titles) {
		t.Fatalf("read back %+v, %v", back.Titles, err)
	}
}
