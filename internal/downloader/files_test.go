package downloader

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

func fileEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	st := storetest.Open(t)
	e := &Engine{Store: st, Dir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Importer: &LibraryImporter{Store: st}}
	return e, st
}

func write(t *testing.T, p string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCleanRel(t *testing.T) {
	for _, p := range []string{"..", "../etc", "a/../../b", "a/./b", ".nzb", ".nzb/1.nzb", "a//b"} {
		if _, err := cleanRel(p); err == nil {
			t.Errorf("%q passed", p)
		}
	}
	for p, want := range map[string]string{"": "", "/": "", "/Movie/": "Movie", `a\b`: "a/b", "_quarantine/x": "_quarantine/x"} {
		if got, err := cleanRel(p); err != nil || got != want {
			t.Errorf("%q: %q %v", p, got, err)
		}
	}
}

func TestBrowse(t *testing.T) {
	e, _ := fileEngine(t)
	ctx := context.Background()
	write(t, filepath.Join(e.Dir, "Movie.2024", "movie.mkv"), 1000)
	write(t, filepath.Join(e.Dir, "Movie.2024", "movie.nfo"), 10)
	write(t, filepath.Join(e.Dir, ".nzb", "1.nzb"), 50)
	write(t, filepath.Join(e.Dir, QuarantineDir, "Bad", "setup.exe"), 200)
	write(t, filepath.Join(e.Dir, "loose.mkv"), 5)
	l, err := e.Browse(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 3 || l.Entries[0].Name != QuarantineDir || l.Entries[1].Name != "Movie.2024" || l.Entries[2].Name != "loose.mkv" {
		t.Fatalf("entries %+v", l.Entries)
	}
	if l.Entries[1].Size != 1010 || l.Entries[1].Files != 2 || !l.Entries[2].Video {
		t.Fatalf("folder %+v", l.Entries[1])
	}
	if l.Size != 1215 || l.Quarantine != 200 {
		t.Fatalf("size %d quarantine %d", l.Size, l.Quarantine)
	}
	q, err := e.Browse(ctx, QuarantineDir+"/Bad")
	if err != nil || len(q.Entries) != 1 || !q.Entries[0].Quarantined || q.Entries[0].Video {
		t.Fatalf("quarantine %+v %v", q, err)
	}
	if _, err := e.Browse(ctx, "../"); err == nil {
		t.Fatal("browsed outside")
	}
}

func TestLinksStayInside(t *testing.T) {
	e, _ := fileEngine(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "keep.mkv"), 10)
	if err := os.Symlink(outside, filepath.Join(e.Dir, "lib")); err != nil {
		t.Skip(err)
	}
	if _, err := e.Browse(context.Background(), "lib"); err == nil {
		t.Fatal("browsed through a link out of the downloads folder")
	}
	if _, _, err := e.OpenFile(context.Background(), "lib/keep.mkv"); err == nil {
		t.Fatal("opened a file through a link")
	}
	if err := e.ImportPath(context.Background(), "lib/keep.mkv", "movie", 1); err == nil {
		t.Fatal("imported through a link")
	}
	// Deleting the link deletes the link, never what it points to.
	if err := e.DeletePath(context.Background(), "lib"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.mkv")); err != nil {
		t.Fatal("deleted through a link")
	}
}

func TestDeletePath(t *testing.T) {
	e, st := fileEngine(t)
	ctx := context.Background()
	write(t, filepath.Join(e.Dir, "Old", "a.mkv"), 10)
	write(t, filepath.Join(e.Dir, "Busy", "a.mkv"), 10)
	os.MkdirAll(filepath.Join(e.Dir, QuarantineDir), 0o750)
	for _, p := range []string{"", "/", QuarantineDir, "..", ".nzb"} {
		if err := e.DeletePath(ctx, p); err == nil {
			t.Errorf("deleted %q", p)
		}
	}
	d, err := st.AddDownload(ctx, store.Download{Name: "Busy", Status: store.DownloadQueued})
	if err != nil {
		t.Fatal(err)
	}
	d.Folder = "Busy"
	st.SaveDownloadProgress(ctx, d)
	if err := e.DeletePath(ctx, "Busy/a.mkv"); err == nil {
		t.Fatal("deleted a queued download's files")
	}
	if _, _, err := e.OpenFile(ctx, "Busy/a.mkv"); err == nil {
		t.Fatal("downloaded a queued download's files")
	}
	for _, p := range []string{"", "Busy"} {
		l, err := e.Browse(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range l.Entries {
			if busy := f.Path == "Busy" || f.Path == "Busy/a.mkv"; f.Busy != busy || busy && f.BusyWith != store.DownloadQueued {
				t.Errorf("%s busy %v %q", f.Path, f.Busy, f.BusyWith)
			}
		}
	}
	if err := e.DeletePath(ctx, "Old"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "Old")); !os.IsNotExist(err) {
		t.Fatal("still there")
	}
}

func TestReleaseAndOpen(t *testing.T) {
	e, st := fileEngine(t)
	ctx := context.Background()
	write(t, filepath.Join(e.Dir, QuarantineDir, "Rel", "movie.mkv"), 10)
	write(t, filepath.Join(e.Dir, "Rel", "other"), 1)
	d, _ := st.AddDownload(ctx, store.Download{Name: "Rel"})
	st.SetDownloadStatus(ctx, d.ID, store.DownloadFailed, "quarantined")
	st.SetDownloadQuarantined(ctx, d.ID, filepath.Join(e.Dir, QuarantineDir, "Rel"))
	if _, _, err := e.OpenFile(context.Background(), QuarantineDir+"/Rel/movie.mkv"); err == nil {
		t.Fatal("opened a quarantined file")
	}
	if err := e.ImportPath(ctx, QuarantineDir+"/Rel", "movie", 1); err == nil {
		t.Fatal("imported from quarantine")
	}
	if _, err := e.Release(ctx, QuarantineDir+"/Rel/movie.mkv"); err == nil {
		t.Fatal("released part of a folder")
	}
	to, err := e.Release(ctx, QuarantineDir+"/Rel")
	if err != nil || to != "Rel (2)" {
		t.Fatalf("released to %q: %v", to, err)
	}
	d, _ = st.Download(ctx, d.ID)
	if d.Quarantined != "" || d.Folder != "Rel (2)" {
		t.Fatalf("download %+v", d)
	}
	f, info, err := e.OpenFile(context.Background(), "Rel (2)/movie.mkv")
	if err != nil || info.Size() != 10 {
		t.Fatal(err)
	}
	f.Close()
}

func TestImportPath(t *testing.T) {
	e, st := fileEngine(t)
	ctx := context.Background()
	lib := t.TempDir()
	profiles, _ := st.Profiles(ctx)
	m, err := st.AddMovie(ctx, store.Movie{TMDBID: 1, Title: "Test Movie", Year: 2024, ProfileID: profiles[0].ID, RootFolder: lib, Path: filepath.Join(lib, "Test Movie (2024)"), Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.Dir, "Test.Movie.2024.1080p", "test.movie.mkv"), 5000)
	write(t, filepath.Join(e.Dir, "Test.Movie.2024.1080p", "test.movie.nfo"), 5)
	if err := e.ImportPath(ctx, "Test.Movie.2024.1080p", "movie", m.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, err := e.Browse(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		// Browse lists the folder before it reads how moves went, so a
		// move that finishes in between shows as done with the folder still
		// there; the next look has it gone.
		if len(l.Moves) == 1 && !l.Moves[0].Running && len(l.Entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never finished: %+v, left over %+v", l.Moves, l.Entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(m.Path, "Test Movie (2024).mkv")); err != nil {
		t.Fatal("not in the library")
	}
}
