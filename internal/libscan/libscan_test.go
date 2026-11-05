package libscan

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func touch(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot is every file and folder under root, to show a scan and an add
// leave the disk as it was.
func snapshot(t *testing.T, root string) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		info, _ := d.Info()
		out = append(out, path+" "+info.ModTime().String()+" "+info.Mode().String())
		return nil
	})
	return out
}

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := tmdbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Service{Store: db, Log: log, Catalog: &catalog.Service{Store: db, Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}}
}

func wait(t *testing.T, s *Service, root int, status string) Scan {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sc, ok := s.Get(root)
		if !ok {
			t.Fatal("scan gone")
		}
		if sc.Status == status || sc.Status == Failed {
			if sc.Status != status {
				t.Fatalf("scan %s: %s", sc.Status, sc.Error)
			}
			return sc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scan never got to %s", status)
	return Scan{}
}

func find(sc Scan, name string) Found {
	i := slices.IndexFunc(sc.Found, func(f Found) bool { return f.Name == name })
	if i < 0 {
		return Found{Name: "missing " + name}
	}
	return sc.Found[i]
}

// A movie root folder's scan finds the folders that aren't on the list,
// matches them, and adds the ones picked with their files, without
// touching anything on disk.
func TestScanMovies(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	dir := t.TempDir()
	root, err := s.Store.AddRootFolder(ctx, "movie", dir)
	if err != nil {
		t.Fatal(err)
	}
	profiles, _ := s.Store.Profiles(ctx)
	matrix := filepath.Join(dir, "The.Matrix.1999.1080p.BluRay.x264-GRP")
	touch(t, filepath.Join(matrix, "the.matrix.1999.1080p.bluray.x264-grp.mkv"), 1000)
	touch(t, filepath.Join(matrix, "sample", "sample.mkv"), 10)
	touch(t, filepath.Join(matrix, "the.matrix.1999.1080p.bluray.x264-grp.en.srt"), 5)
	os.MkdirAll(filepath.Join(dir, "Coming Attraction {tmdb-1001}"), 0o755)
	touch(t, filepath.Join(dir, "Nothing Like This (2001)", "x.mkv"), 10)
	touch(t, filepath.Join(dir, "Listed (2000)", "Listed (2000).mkv"), 10)
	touch(t, filepath.Join(dir, "Kept Elsewhere (2003)", "k.mkv"), 10)
	touch(t, filepath.Join(dir, "Moved Files", "m.mkv"), 10)
	touch(t, filepath.Join(dir, "stray.mkv"), 10)
	touch(t, filepath.Join(dir, ".recycle", "old.mkv"), 10)
	s.Store.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Listed", ProfileID: profiles[0].ID, RootFolder: dir, Path: filepath.Join(dir, "Listed (2000)")})
	s.Store.AddMovie(ctx, store.Movie{TMDBID: 2, Title: "Kept Elsewhere", ProfileID: profiles[0].ID, RootFolder: "/other", Path: "/other/Kept Elsewhere (2003)"})
	odd, _ := s.Store.AddMovie(ctx, store.Movie{TMDBID: 3, Title: "Odd", ProfileID: profiles[0].ID, RootFolder: dir, Path: filepath.Join(dir, "Odd (2004)")})
	s.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: odd.ID, Path: filepath.Join(dir, "Moved Files", "m.mkv")})
	before := snapshot(t, dir)

	if _, err := s.Start(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	sc := wait(t, s, root.ID, Ready)
	if sc.Folders != 6 || sc.Listed != 2 || sc.Elsewhere != 1 || sc.Loose != 1 || len(sc.Found) != 3 {
		t.Fatalf("scan %+v", sc)
	}
	m := find(sc, filepath.Base(matrix))
	if m.Match == nil || m.Match.TMDBID != 603 || !m.Sure || m.Files != 1 || m.Size != 1000 || m.Have != nil {
		t.Fatalf("matrix %+v", m)
	}
	c := find(sc, "Coming Attraction {tmdb-1001}")
	if c.Match == nil || c.Match.TMDBID != 1001 || !c.Sure || c.Note == "" {
		t.Fatalf("coming %+v", c)
	}
	if n := find(sc, "Nothing Like This (2001)"); n.Match != nil || n.Problem == "" {
		t.Fatalf("nothing %+v", n)
	}

	if _, err := s.Add(ctx, root.ID, AddRequest{Picks: []Pick{{Path: "/elsewhere", TMDBID: 603}}}); err == nil {
		t.Fatal("added a folder the scan didn't find")
	}
	if _, err := s.Add(ctx, root.ID, AddRequest{Picks: []Pick{{Path: matrix, TMDBID: 603}}, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	sc = wait(t, s, root.ID, Done)
	if sc.Added != 1 || sc.NotAdded != 0 || find(sc, filepath.Base(matrix)).Added == 0 {
		t.Fatalf("after add %+v", sc)
	}
	got, err := s.Store.MovieByTMDB(ctx, 603)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != matrix || got.File == nil || got.File.Path != filepath.Join(matrix, "the.matrix.1999.1080p.bluray.x264-grp.mkv") || got.File.Size != 1000 || got.File.Quality == 0 || !got.Monitored {
		t.Fatalf("movie %+v file %+v", got, got.File)
	}
	if after := snapshot(t, dir); !slices.Equal(before, after) {
		t.Fatalf("the disk changed:\n%v\n%v", before, after)
	}

	// Adding it again is refused; a fresh scan has it on the list.
	if _, err := s.Add(ctx, root.ID, AddRequest{Picks: []Pick{{Path: matrix, TMDBID: 603}}}); err == nil {
		t.Fatal("added twice")
	}
	s.Start(ctx, root.ID)
	sc = wait(t, s, root.ID, Ready)
	if sc.Listed != 3 || len(sc.Found) != 2 {
		t.Fatalf("rescan %+v", sc)
	}
	if err := s.Dismiss(root.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(root.ID); ok {
		t.Fatal("still there")
	}
}

// A show's episodes get the files named for them.
func TestScanShows(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	dir := t.TempDir()
	root, _ := s.Store.AddRootFolder(ctx, "series", dir)
	show := filepath.Join(dir, "Severance (2022)")
	touch(t, filepath.Join(show, "Season 01", "Severance - S01E01 - Good News About Hell.mkv"), 100)
	touch(t, filepath.Join(show, "Season 01", "Severance.S01E02.720p.WEB.mkv"), 50)
	touch(t, filepath.Join(show, "Season 09", "Severance - S09E01.mkv"), 50)
	before := snapshot(t, dir)

	s.Start(ctx, root.ID)
	sc := wait(t, s, root.ID, Ready)
	f := find(sc, "Severance (2022)")
	if f.Match == nil || f.Match.TMDBID != 95396 || !f.Sure || f.Files != 3 || f.Episodes != 3 {
		t.Fatalf("found %+v", f)
	}
	s.Add(ctx, root.ID, AddRequest{Picks: []Pick{{Path: show, TMDBID: 95396}}, Monitored: true, Monitor: "all"})
	sc = wait(t, s, root.ID, Done)
	if sc.Added != 1 {
		t.Fatalf("add %+v", sc)
	}
	got, _ := s.Store.SeriesByTMDB(ctx, 95396)
	eps, _ := s.Store.Episodes(ctx, got.ID)
	files := 0
	for _, e := range eps {
		if e.File != nil {
			files++
			if e.Season != 1 || filepath.Dir(e.File.Path) != filepath.Join(show, "Season 01") {
				t.Fatalf("episode %+v file %+v", e, e.File)
			}
		}
	}
	if got.Path != show || files != 2 {
		t.Fatalf("show %+v, %d files", got, files)
	}
	if after := snapshot(t, dir); !slices.Equal(before, after) {
		t.Fatal("the disk changed")
	}
}

// Root folders for what Box Office can't add yet can't be scanned.
func TestScanUnsupported(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	root, _ := s.Store.AddRootFolder(ctx, "ebook", t.TempDir())
	if _, err := s.Start(ctx, root.ID); err == nil {
		t.Fatal("scanned ebooks")
	}
}
