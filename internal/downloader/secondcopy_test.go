package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// A movie's second copy goes in next to the first, named for its quality,
// or into a folder named like the movie's in another library folder. It
// never touches the first copy, and a better second copy sets only the
// old second copy aside.
func TestImportSecondCopy(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib, other := t.TempDir(), t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, _ := st.AddMovie(ctx, store.Movie{TMDBID: 78, Title: "Blade Runner", Year: 1982, ProfileID: profiles[2].ID, RootFolder: lib, Path: filepath.Join(lib, "Blade Runner (1982)"), Monitored: true})
	os.MkdirAll(m.Path, 0o755)
	first := filepath.Join(m.Path, "Blade Runner (1982).mkv")
	os.WriteFile(first, make([]byte, 9000), 0o644)
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: first, Quality: 16})
	m.SecondProfileID = profiles[1].ID
	st.UpdateMovie(ctx, m)
	li := &LibraryImporter{Store: st}
	bring := func(name string, size int) (string, error) {
		t.Helper()
		dl := t.TempDir()
		os.WriteFile(filepath.Join(dl, "video.mkv"), make([]byte, size), 0o644)
		os.WriteFile(filepath.Join(dl, "video.en.srt"), []byte("1"), 0o644)
		return li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: name, Mode: store.SecondKind}, dl, listFiles(dl))
	}

	to, err := bring("Blade.Runner.1982.720p.BluRay.x264-GRP", 3000)
	if err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(m.Path, "Blade Runner (1982) - Blu-ray 720p.mkv")
	if to != small {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(filepath.Join(m.Path, "Blade Runner (1982) - Blu-ray 720p.en.srt")); err != nil {
		t.Fatal("its subtitles didn't come along")
	}
	got, _ := st.Movie(ctx, m.ID)
	if got.File == nil || got.File.Path != first || got.Second == nil || got.Second.Path != small || got.Second.Kind != store.SecondKind {
		t.Fatalf("copies %+v %+v", got.File, got.Second)
	}
	// A better second copy: the old one is set aside, the first stays.
	to, err = bring("Blade.Runner.1982.1080p.BluRay.x264-GRP", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Path, "Blade Runner (1982) - Blu-ray 1080p.mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(small); err == nil {
		t.Fatal("the old second copy is still there")
	}
	if rec, _ := filepath.Glob(filepath.Join(lib, ".recycle", "*", "Blade Runner (1982) - Blu-ray 720p.mkv")); len(rec) != 1 {
		t.Fatalf("the old second copy wasn't set aside: %v", rec)
	}
	if fi, err := os.Stat(first); err != nil || fi.Size() != 9000 {
		t.Fatal("the first copy was touched")
	}
	got, _ = st.Movie(ctx, m.ID)
	if got.File == nil || got.File.Path != first || got.Second == nil || got.Second.Path != to {
		t.Fatalf("copies %+v %+v", got.File, got.Second)
	}

	// Kept in another library folder: a folder named like the movie's.
	st.DeleteFile(ctx, got.Second.ID)
	got.SecondRoot = other
	st.UpdateMovie(ctx, got)
	to, err = bring("Blade.Runner.1982.1080p.BluRay.x264-GRP", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(other, "Blade Runner (1982)", "Blade Runner (1982).mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	got, _ = st.Movie(ctx, m.ID)
	if got.File == nil || got.File.Path != first || got.Second == nil || got.Second.Path != to {
		t.Fatalf("copies %+v %+v", got.File, got.Second)
	}
	// The root folder is in use by it.
	if n, _ := st.RootFolderTitles(ctx, other); n != 1 {
		t.Fatalf("%d titles in the second copies' folder", n)
	}
}

// A show's second copies go in next to the first ones, named for their
// quality, or under a folder named like the show's in another library
// folder. The first copies are never touched, and a better second copy
// sets only the old second copy aside.
func TestImportSecondCopyEpisodes(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib, other := t.TempDir(), t.TempDir()
	profiles, _ := st.Profiles(ctx)
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 9, Title: "Show", ProfileID: profiles[2].ID, RootFolder: lib, Path: filepath.Join(lib, "Show"),
		Monitored: true, Seasons: []store.Season{{Number: 1, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	show.SecondProfileID = profiles[1].ID
	if err := st.UpdateSeries(ctx, show); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: i, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	first := filepath.Join(show.Path, "Season 01", "Show - S01E01.mkv")
	os.MkdirAll(filepath.Dir(first), 0o755)
	os.WriteFile(first, make([]byte, 9000), 0o644)
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: first, Quality: 15})
	li := &LibraryImporter{Store: st}
	bring := func(name string, size int, videos ...string) (string, error) {
		t.Helper()
		dl := t.TempDir()
		for _, v := range videos {
			os.WriteFile(filepath.Join(dl, v), make([]byte, size), 0o644)
		}
		return li.Import(ctx, store.Download{Kind: "series", TitleID: show.ID, Name: name, Mode: store.SecondKind}, dl, listFiles(dl))
	}
	copies := func() []store.Episode {
		t.Helper()
		eps, err := st.Episodes(ctx, show.ID)
		if err != nil {
			t.Fatal(err)
		}
		return eps
	}

	// A season pack: both episodes get a second copy, next to the first.
	if _, err := bring("Show.S01.720p.WEB-DL-GRP", 3000, "Show.S01E01.720p.WEB-DL.mkv", "Show.S01E02.720p.WEB-DL.mkv"); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(show.Path, "Season 01", "Show - S01E01 - WEB 720p.mkv")
	eps = copies()
	if eps[0].File == nil || eps[0].File.Path != first || eps[0].Second == nil || eps[0].Second.Path != small || eps[0].Second.Kind != store.SecondEpisodeKind {
		t.Fatalf("episode 1: %+v %+v", eps[0].File, eps[0].Second)
	}
	if eps[1].File != nil || eps[1].Second == nil || eps[1].Second.Path != filepath.Join(show.Path, "Season 01", "Show - S01E02 - WEB 720p.mkv") {
		t.Fatalf("episode 2: %+v %+v", eps[1].File, eps[1].Second)
	}
	// A better second copy of an episode: the old one is set aside.
	to, err := bring("Show.S01E01.1080p.WEB-DL-GRP", 5000, "Show.S01E01.1080p.WEB-DL.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(show.Path, "Season 01", "Show - S01E01 - WEB 1080p.mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(small); err == nil {
		t.Fatal("the old second copy is still there")
	}
	if rec, _ := filepath.Glob(filepath.Join(lib, ".recycle", "*", "Show - S01E01 - WEB 720p.mkv")); len(rec) != 1 {
		t.Fatalf("the old second copy wasn't set aside: %v", rec)
	}
	if fi, err := os.Stat(first); err != nil || fi.Size() != 9000 {
		t.Fatal("the first copy was touched")
	}
	if eps = copies(); eps[0].File == nil || eps[0].File.Path != first || eps[0].Second == nil || eps[0].Second.Path != to {
		t.Fatalf("episode 1: %+v %+v", eps[0].File, eps[0].Second)
	}

	// Kept in another library folder: a folder named like the show's.
	for _, e := range eps {
		st.DeleteFile(ctx, e.Second.ID)
	}
	show, _ = st.Series(ctx, show.ID)
	show.SecondRoot = other
	if err := st.UpdateSeries(ctx, show); err != nil {
		t.Fatal(err)
	}
	to, err = bring("Show.S01E01.1080p.WEB-DL-GRP", 5000, "Show.S01E01.1080p.WEB-DL.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(other, "Show", "Season 01", "Show - S01E01.mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if eps = copies(); eps[0].File == nil || eps[0].File.Path != first || eps[0].Second == nil || eps[0].Second.Path != to {
		t.Fatalf("episode 1: %+v %+v", eps[0].File, eps[0].Second)
	}
	if n, _ := st.RootFolderTitles(ctx, other); n != 1 {
		t.Fatalf("%d titles in the second copies' folder", n)
	}
}
