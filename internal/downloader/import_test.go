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

// A season pack fills in missing episodes and upgrades worse ones, but
// leaves an episode whose copy is as good alone.
func TestImportPackKeepsBetter(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	show, _ := st.AddSeries(ctx, store.Series{TMDBID: 1, Title: "Show", ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Show"), Monitored: true,
		Seasons: []store.Season{{Number: 1, Monitored: true}}})
	for n := 1; n <= 3; n++ {
		st.UpsertEpisode(ctx, store.Episode{SeriesID: show.ID, Season: 1, Number: n, Monitored: true})
	}
	eps, _ := st.Episodes(ctx, show.ID)
	os.MkdirAll(filepath.Join(show.Path, "Season 01"), 0o755)
	good := filepath.Join(show.Path, "Season 01", "good.mkv")
	worse := filepath.Join(show.Path, "Season 01", "worse.mkv")
	os.WriteFile(good, []byte("remux"), 0o644)
	os.WriteFile(worse, []byte("hdtv"), 0o644)
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[0].ID, Path: good, Quality: 12})
	st.AddFile(ctx, store.File{Kind: "episode", OwnerID: eps[1].ID, Path: worse, Quality: 4})

	dl := t.TempDir()
	for n := 1; n <= 3; n++ {
		os.WriteFile(filepath.Join(dl, fmt.Sprintf("Show.S01E%02d.1080p.BluRay.mkv", n)), make([]byte, 5000), 0o644)
	}
	li := &LibraryImporter{Store: st}
	d := store.Download{Kind: "series", TitleID: show.ID, Name: "Show.S01.1080p.BluRay", EpisodeIDs: []int64{eps[0].ID, eps[1].ID, eps[2].ID}}
	if _, err := li.Import(ctx, d, dl, listFiles(dl)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(good); string(b) != "remux" {
		t.Fatal("the better copy of episode 1 was replaced")
	}
	if _, err := os.Stat(worse); err == nil {
		t.Fatal("the worse copy of episode 2 is still in the library")
	}
	eps, _ = st.Episodes(ctx, show.ID)
	if eps[0].File.Quality != 12 || eps[1].File.Quality != 11 || eps[2].File == nil || eps[2].File.Quality != 11 {
		t.Fatalf("files %+v %+v %+v", eps[0].File, eps[1].File, eps[2].File)
	}
}
