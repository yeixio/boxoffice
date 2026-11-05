package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
