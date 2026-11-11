package downloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/boxoffice/internal/store"
)

// After an update, finished downloads that couldn't go into the library
// are tried once more, once per version; others are left be.
func TestRetryImports(t *testing.T) {
	e, st := fileEngine(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Test Movie", Year: 2024, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Test Movie (2024)"), Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	add := func(folder, msg string) store.Download {
		write(t, filepath.Join(e.Dir, folder, "test.movie.mkv"), 5000)
		d, err := st.AddDownload(ctx, store.Download{Name: folder, Kind: "movie", TitleID: m.ID, Folder: folder})
		if err != nil {
			t.Fatal(err)
		}
		d.Folder = folder
		st.SaveDownloadProgress(ctx, d)
		st.SetDownloadStatus(ctx, d.ID, store.DownloadCompleted, msg)
		return d
	}
	failed := add("Test.Movie.2024.1080p", "Downloaded, but it couldn't go into the library yet: no. It's in the downloads folder until you choose Move into library.")
	untried := add("Test.Movie.2024.720p", "Downloaded to Test.Movie.2024.720p. Moving downloads into the library is off, so it's waiting there.")

	// Off: nothing's tried.
	SaveSettings(ctx, st, Settings{Enabled: true})
	if n, err := e.RetryImports(ctx, "v1"); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	SaveSettings(ctx, st, Settings{Enabled: true, Import: true})
	if n, err := e.RetryImports(ctx, "v2"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	d, _ := st.Download(ctx, failed.ID)
	if d.ImportedTo == "" || d.Status != store.DownloadCompleted {
		t.Fatalf("%+v", d)
	}
	if _, err := os.Stat(filepath.Join(m.Path, "Test Movie (2024).mkv")); err != nil {
		t.Fatal("not in the library")
	}
	if d, _ := st.Download(ctx, untried.ID); d.ImportedTo != "" {
		t.Fatal("one never tried was moved in")
	}
	// Once per version.
	again := add("Test.Movie.2024.2160p", "Downloaded, but it couldn't go into the library yet: no.")
	if n, _ := e.RetryImports(ctx, "v2"); n != 0 {
		t.Fatal("retried twice for one version")
	}
	if d, _ := st.Download(ctx, again.ID); d.ImportedTo != "" {
		t.Fatal("moved in")
	}
}
