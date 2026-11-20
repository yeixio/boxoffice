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
