package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func TestImportMovie(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Test: Movie", Year: 2024, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Test Movie (2024)"), Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "test.movie.2024.1080p.web-dl.mkv"), make([]byte, 5000), 0o644)
	os.WriteFile(filepath.Join(dl, "test.movie.2024.1080p.web-dl.en.srt"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dl, "sample.mkv"), make([]byte, 100), 0o644)
	li := &LibraryImporter{Store: st}
	d := store.Download{Kind: "movie", TitleID: m.ID, Name: "Test.Movie.2024.1080p.WEB-DL"}
	to, err := li.Import(ctx, d, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Path, "Test Movie (2024).mkv"); to != want {
		t.Fatalf("to %s", to)
	}
	if _, err := os.Stat(filepath.Join(m.Path, "Test Movie (2024).en.srt")); err != nil {
		t.Fatal("subtitle didn't come along")
	}
	m, _ = st.Movie(ctx, m.ID)
	if m.File == nil || m.File.Quality != 10 {
		t.Fatalf("file %+v", m.File)
	}
}

func TestImportReadOnlyLibrary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere that isn't a read-only mount")
	}
	lib := t.TempDir()
	os.Chmod(lib, 0o555)
	defer os.Chmod(lib, 0o755)
	if err := checkLibrary(lib, filepath.Join(lib, "X (2020)")); err == nil {
		t.Fatal("a read-only library passed")
	}
}

func TestEpisodeNumbers(t *testing.T) {
	for name, want := range map[string][2]int{"Show.S02E05.1080p.mkv": {2, 5}, "show.3x07.mkv": {3, 7}, "Show - S1E2.mkv": {1, 2}} {
		s, e := episodeNumbers(name)
		if s != want[0] || len(e) == 0 || e[0] != want[1] {
			t.Errorf("%s: %d %v", name, s, e)
		}
	}
	if _, e := episodeNumbers("Show.S01E01E02.mkv"); len(e) != 2 {
		t.Errorf("multi-episode: %v", e)
	}
	if err := checkLibrary("/lib", "/elsewhere/X"); err == nil {
		t.Fatal("a folder outside the library passed")
	}
}

// The original staying behind on the downloads share doesn't fail the
// import: the library copy is whole, and it's recorded.
func TestImportSourceStuck(t *testing.T) {
	crossDevice(t, 100)
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Cave", Year: 2016, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Cave (2016)"), Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "Cave.2016.1080p.WEB.mkv"), make([]byte, 5000), 0o644)
	li := &LibraryImporter{Store: st}
	to, err := li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Cave.2016.1080p.WEB"}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(to); err != nil || info.Size() != 5000 {
		t.Fatalf("library copy: %v", err)
	}
	if m, _ = st.Movie(ctx, m.ID); m.File == nil || m.File.Path != to {
		t.Fatalf("file %+v", m.File)
	}
}

// An upgrade whose file name is the same as the copy it replaces sets the
// old one aside in .recycle; it's never written over.
func TestImportUpgradeSameName(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Up", Year: 2020, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Up (2020)"), Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(m.Path, 0o755)
	oldPath := filepath.Join(m.Path, "Up (2020).mkv")
	os.WriteFile(oldPath, []byte("old copy"), 0o644)
	os.WriteFile(filepath.Join(m.Path, "Up (2020).srt"), []byte("old subs"), 0o644)
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: oldPath, Quality: 4})

	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "Up.2020.1080p.BluRay.mkv"), make([]byte, 5000), 0o644)
	os.WriteFile(filepath.Join(dl, "Up.2020.1080p.BluRay.srt"), []byte("new subs"), 0o644)
	li := &LibraryImporter{Store: st}
	to, err := li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Up.2020.1080p.BluRay"}, dl, listFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(to); to != oldPath || err != nil || info.Size() != 5000 {
		t.Fatalf("new copy at %s: %v", to, err)
	}
	day := filepath.Join(lib, ".recycle", time.Now().UTC().Format("2006-01-02"))
	if b, err := os.ReadFile(filepath.Join(day, "Up (2020).mkv")); err != nil || string(b) != "old copy" {
		t.Fatalf("old copy in .recycle: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(day, "Up (2020).srt")); err != nil || string(b) != "old subs" {
		t.Fatalf("old subtitles in .recycle: %q %v", b, err)
	}
	if m, _ = st.Movie(ctx, m.ID); m.File == nil || m.File.Quality != 11 {
		t.Fatalf("file %+v", m.File)
	}
}

// A move that fails puts back the copy it set aside.
func TestImportUpgradeFailsKeepsOld(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, _ := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Up", Year: 2020, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Up (2020)"), Monitored: true})
	os.MkdirAll(m.Path, 0o755)
	oldPath := filepath.Join(m.Path, "Up (2020).mkv")
	os.WriteFile(oldPath, []byte("old copy"), 0o644)
	st.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: oldPath, Quality: 4})
	orig := rename
	rename = func(src, dest string) error {
		if dest == oldPath && strings.Contains(src, "BluRay") {
			return errors.New("disk on fire")
		}
		return orig(src, dest)
	}
	t.Cleanup(func() { rename = orig })
	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "Up.2020.1080p.BluRay.mkv"), make([]byte, 5000), 0o644)
	li := &LibraryImporter{Store: st}
	if _, err := li.Import(ctx, store.Download{Kind: "movie", TitleID: m.ID, Name: "Up.2020.1080p.BluRay"}, dl, listFiles(dl)); err == nil {
		t.Fatal("no error")
	}
	if b, err := os.ReadFile(oldPath); err != nil || string(b) != "old copy" {
		t.Fatalf("old copy not put back: %q %v", b, err)
	}
}

// An episode whose video has a scrambled name goes in by the release's
// name, or else by the episode it was grabbed for.
func TestImportObfuscatedEpisode(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 2, Title: "The Lowdown", Year: 2025, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "The Lowdown (2025)"), Monitored: true, Seasons: []store.Season{{Number: 2, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for n := 4; n <= 6; n++ {
		if err := st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 2, Number: n, Title: fmt.Sprintf("Ep %d", n), Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := st.Episodes(ctx, show.ID)
	li := &LibraryImporter{Store: st}
	for _, c := range []struct {
		d    store.Download
		want string
	}{
		{store.Download{Name: "The.Lowdown.2025.S02E05.A.Dog.with.Two.Tails.1080p.AMZN.WEB-DL-RAWR"}, "S02E05 - Ep 5"},
		{store.Download{Name: "a3f9c0d1e2", EpisodeIDs: []int64{eps[2].ID}}, "S02E06 - Ep 6"},
	} {
		dl := t.TempDir()
		os.WriteFile(filepath.Join(dl, "8f2c1e9a7b.mkv"), make([]byte, 5000), 0o644)
		os.WriteFile(filepath.Join(dl, "d41d8cd98f.mkv"), make([]byte, 300), 0o644)
		c.d.Kind, c.d.TitleID = "series", show.ID
		to, err := li.Import(ctx, c.d, dl, listFiles(dl))
		if err != nil {
			t.Fatalf("%s: %v", c.d.Name, err)
		}
		if want := filepath.Join(show.Path, "Season 02", "The Lowdown (2025) - "+c.want+".mkv"); to != want {
			t.Fatalf("%s: to %s", c.d.Name, to)
		}
		if info, _ := os.Stat(to); info == nil || info.Size() != 5000 {
			t.Fatalf("%s: the wrong video went in", c.d.Name)
		}
	}
}

// A season pack whose videos are all named with hashes goes in by what
// the videos' tags say; one that can't be told apart stays in the
// downloads folder, and the import says so.
func TestImportSeasonPackByTags(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	show, err := st.AddSeries(ctx, store.Series{TMDBID: 7, Title: "Pantry Lab", Year: 2001, Runtime: 22, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Pantry Lab (2001)"), Monitored: true, Seasons: []store.Season{{Number: 3, Monitored: true}}})
	if err != nil {
		t.Fatal(err)
	}
	all, tags, _ := pantry()
	for _, e := range all {
		e.ID, e.SeriesID, e.Monitored = 0, show.ID, true
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	dl := t.TempDir()
	for v := range tags {
		os.WriteFile(filepath.Join(dl, v), make([]byte, 5000), 0o644)
	}
	li := &LibraryImporter{Store: st, Tags: func(_ context.Context, p string) (VideoTags, error) {
		return tags[filepath.Base(p)], nil
	}}
	d := store.Download{Kind: "series", TitleID: show.ID, Name: "Pantry.Lab.S03.576p.WEB-DL.DDP.2.0.H.264-GROUP"}
	to, err := li.Import(ctx, d, dl, listFiles(dl))
	var left *LeftOverError
	if !errors.As(err, &left) || len(left.Left) != 1 || left.Left[0] != "af03d2.mkv" {
		t.Fatalf("err %v", err)
	}
	if to != filepath.Join(show.Path, "Season 03") {
		t.Fatalf("to %s", to)
	}
	if !fileExists(filepath.Join(dl, "af03d2.mkv")) {
		t.Fatal("the unmatched video should stay where it is")
	}
	eps, _ := st.Episodes(ctx, show.ID)
	for _, e := range eps {
		if (e.File != nil) != (e.Season == 3) {
			t.Errorf("S%02dE%02d file %+v", e.Season, e.Number, e.File)
		}
	}
	if p := filepath.Join(to, "Pantry Lab (2001) - S03E04 - Shell Shock.mkv"); !fileExists(p) {
		t.Errorf("%s missing", p)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A video given an episode by hand goes in as that episode, whatever its
// name says.
func TestImportChosenEpisode(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	show, _ := st.AddSeries(ctx, store.Series{TMDBID: 9, Title: "Show", Year: 2020, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Show (2020)"), Monitored: true})
	st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 0, Number: 3, Title: "Turkey Special", Monitored: true})
	eps, _ := st.Episodes(ctx, show.ID)
	dl := t.TempDir()
	os.WriteFile(filepath.Join(dl, "Show.S01E02.mkv"), make([]byte, 5000), 0o644)
	li := &LibraryImporter{Store: st}
	to, err := li.Import(ctx, store.Download{Kind: "series", TitleID: show.ID, Name: "Show.S01E02", EpisodeIDs: []int64{eps[0].ID}, Mode: ModeEpisode}, dl, []string{"Show.S01E02.mkv"})
	if err != nil || to != filepath.Join(show.Path, "Season 00", "Show (2020) - S00E03 - Turkey Special.mkv") {
		t.Fatalf("%s %v", to, err)
	}
}
