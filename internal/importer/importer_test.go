package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb/tmdbtest"
)

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := tmdbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := &catalog.Service{Store: db, Log: log, EnvTMDBKey: tmdbtest.Key, TMDBBase: fake.URL}
	dl := &downloader.Engine{Store: db, Dir: t.TempDir(), Log: log, Importer: &downloader.LibraryImporter{Store: db}}
	return &Service{Store: db, Downloads: dl, Catalog: cat, Scan: &libscan.Service{Store: db, Catalog: cat, Log: log},
		Games: &games.Service{Store: db, Dir: t.TempDir(), Log: log, MAMEReleases: "http://127.0.0.1:1", LibretroDB: "http://127.0.0.1:1"}, Log: log}
}

func write(t *testing.T, p string, n int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// settled waits for every item to be looked at, and for any copy or
// import to finish.
func settled(t *testing.T, s *Service) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.State(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		busy := st.Copying.Running || st.Running
		for _, it := range st.Items {
			busy = busy || it.Status == Guessing || it.Status == Importing
		}
		if !busy {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("still busy")
	return State{}
}

func item(t *testing.T, st State, name string) Item {
	t.Helper()
	for _, it := range st.Items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("%s isn't there: %+v", name, st.Items)
	return Item{}
}

func TestKindOf(t *testing.T) {
	for _, c := range []struct {
		name string
		w    what
		want string
	}{
		{"The.Matrix.1999.mkv", what{videos: 1}, Movie},
		{"Show S01", what{videos: 8, episodes: 8}, Series},
		{"Artist - Album", what{audio: 12, audioSize: 12 << 20}, Album},
		{"Author - Book", what{audio: 10, audioSize: 10 * 100 << 20}, Audiobook},
		{"Author - Book", what{audio: 1, m4b: 1, audioSize: 1 << 20}, Audiobook},
		{"Author - Book.epub", what{ebooks: 1}, Ebook},
		{"roms", what{games: 3, system: "snes"}, Game},
		{"notes.txt", what{files: 1}, ""},
	} {
		if got := kindOf(c.name, c.w); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCleanName(t *testing.T) {
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", ".hidden", "a/.b/c"} {
		if _, err := cleanName(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	if got, _ := cleanName(`Show\Season 1\e01.mkv`); got != "Show/Season 1/e01.mkv" {
		t.Errorf("got %q", got)
	}
}

// A movie uploaded is matched on TMDB, added to the list, and moved into
// its folder in the library.
func TestUploadAndImportMovie(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	lib := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, "movie", lib); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("m", 5000)
	if err := s.Upload(ctx, "The.Matrix.1999.1080p.mkv", int64(len(body)), strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upload(ctx, "The.Matrix.1999.1080p.mkv", 1, strings.NewReader("m")); err == nil {
		t.Fatal("uploaded over a file waiting")
	}
	if err := s.Upload(ctx, "short.mkv", 10, strings.NewReader("abc")); err == nil {
		t.Fatal("took a file cut short")
	}
	if err := s.Upload(ctx, "notes.txt", 2, strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	st := settled(t, s)
	if len(st.Items) != 2 {
		t.Fatalf("%+v", st.Items)
	}
	m := item(t, st, "The.Matrix.1999.1080p.mkv")
	if m.Kind != Movie || m.Match == nil || m.Match.TMDBID != 603 || !m.Sure || m.Status != Ready || m.Problem != "" {
		t.Fatalf("matrix %+v", m)
	}
	if n := item(t, st, "notes.txt"); n.Kind != "" || n.Problem == "" {
		t.Fatalf("notes %+v", n)
	}
	if _, err := s.Set(ctx, "imports/notes.txt", Pick{Kind: "song"}); err == nil {
		t.Fatal("took a kind Box Office doesn't import")
	}
	if err := s.Run(ctx, []string{"imports/notes.txt"}); err == nil {
		t.Fatal("imported something with no kind")
	}
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	m = item(t, st, "The.Matrix.1999.1080p.mkv")
	if m.Status != Done || !strings.HasPrefix(m.To, lib) {
		t.Fatalf("matrix %+v", m)
	}
	movie, err := s.Store.MovieByTMDB(ctx, 603)
	if err != nil || movie.File == nil || !strings.HasPrefix(movie.File.Path, lib) {
		t.Fatalf("%+v %v", movie, err)
	}
	if _, err := os.Stat(filepath.Join(s.Downloads.Dir, Folder, m.Name)); err == nil {
		t.Fatal("the upload is still waiting")
	}
	s.Clear()
	if st = settled(t, s); len(st.Items) != 1 {
		t.Fatalf("%+v", st.Items)
	}
	if err := s.Remove(ctx, "imports/notes.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "imports/../x"); err == nil {
		t.Fatal("removed something outside the imports folder")
	}
	if st = settled(t, s); len(st.Items) != 0 {
		t.Fatalf("%+v", st.Items)
	}
}

// Games copied from a folder on the server go into their systems'
// folders; the originals stay where they were.
func TestCopyInAndImportGames(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	roots := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, mediatypes.Game, roots); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	write(t, filepath.Join(src, "Carts", "Super Game (USA).sfc"), 512)
	write(t, filepath.Join(src, "Carts", "Pocket Game (World).gb"), 256)
	write(t, filepath.Join(src, "Lone (Europe).nes"), 64)

	st := settled(t, s)
	if n := len(st.Kinds); n != 2 {
		t.Fatalf("kinds with games off %v", st.Kinds)
	}
	if err := s.CopyIn(ctx, []string{filepath.Join(src, "Carts"), filepath.Join(src, "Lone (Europe).nes")}); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	if st.Copying.Error != "" || st.Copying.Done != st.Copying.Total || st.Copying.Total != 832 {
		t.Fatalf("copy %+v", st.Copying)
	}
	carts := item(t, st, "Carts")
	if carts.Kind != Game || carts.Problem == "" || carts.Files != 2 {
		t.Fatalf("carts with games off %+v", carts)
	}
	mediatypes.SetEnabled(ctx, s.Store, mediatypes.Game, true)
	if _, err := s.Set(ctx, "imports/Carts", Pick{Kind: Game}); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	if c := item(t, st, "Carts"); c.Problem != "" || c.System == "" {
		t.Fatalf("carts %+v", c)
	}
	if l := item(t, st, "Lone (Europe).nes"); l.Kind != Game || l.System != "nes" || l.Title != "Lone" {
		t.Fatalf("lone %+v", l)
	}
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	for _, it := range st.Items {
		if it.Status != Done {
			t.Fatalf("%+v", it)
		}
	}
	for _, p := range []string{"snes/Super Game (USA).sfc", "gb/Pocket Game (World).gb", "nes/Lone (Europe).nes"} {
		if _, err := os.Stat(filepath.Join(roots, p)); err != nil {
			t.Errorf("%s isn't in the games folder", p)
		}
	}
	if _, err := os.Stat(filepath.Join(src, "Carts", "Super Game (USA).sfc")); err != nil {
		t.Fatal("the original went")
	}
}

// A folder's games go in, and a file Box Office can't place stays in the
// imports folder with a problem saying so.
func TestImportGamesKeepsWhatItCantPlace(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	roots := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, mediatypes.Game, roots); err != nil {
		t.Fatal(err)
	}
	mediatypes.SetEnabled(ctx, s.Store, mediatypes.Game, true)
	src := t.TempDir()
	write(t, filepath.Join(src, "Mixed", "Lone (Europe).nes"), 64)
	write(t, filepath.Join(src, "Mixed", "notes", "mystery.bin"), 32)
	// A MAME set whose ROMs are named .bin.
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"gridlee.1", "gridlee.2", "gridlee.7"} {
		w, _ := zw.Create(n + ".bin")
		w.Write([]byte("rom"))
	}
	zw.Close()
	os.WriteFile(filepath.Join(src, "Mixed", "gridlee.zip"), zb.Bytes(), 0o644)

	if err := s.CopyIn(ctx, []string{filepath.Join(src, "Mixed")}); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if _, err := s.Set(ctx, "imports/Mixed", Pick{Kind: Game}); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st := settled(t, s)
	m := item(t, st, "Mixed")
	if m.Status != Done || !strings.Contains(m.Problem, "mystery.bin") {
		t.Fatalf("mixed %+v", m)
	}
	for _, p := range []string{"nes/Lone (Europe).nes", "mame/gridlee.zip"} {
		if _, err := os.Stat(filepath.Join(roots, p)); err != nil {
			t.Errorf("%s isn't in the games folder", p)
		}
	}
	dir, _ := s.dir()
	if _, err := os.Stat(filepath.Join(dir, "Mixed", "notes", "mystery.bin")); err != nil {
		t.Fatal("the file it couldn't place was deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "Mixed", "gridlee.zip")); err == nil {
		t.Fatal("a placed game was left in the imports folder")
	}
}

// A folder of disc images Box Office can't tell is still games; picking
// the system and typing the game's name files it whole under that name.
func TestImportGameByName(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	roots := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, mediatypes.Game, roots); err != nil {
		t.Fatal(err)
	}
	mediatypes.SetEnabled(ctx, s.Store, mediatypes.Game, true)
	src := t.TempDir()
	for i := 1; i <= 7; i++ {
		write(t, filepath.Join(src, "X-Files", fmt.Sprintf("XFILES%d.bin", i)), 4096)
	}
	if err := s.CopyIn(ctx, []string{filepath.Join(src, "X-Files")}); err != nil {
		t.Fatal(err)
	}
	st := settled(t, s)
	if x := item(t, st, "X-Files"); x.Kind != Game || x.System != "" || x.Problem == "" {
		t.Fatalf("discs %+v", x)
	}
	if _, err := s.Set(ctx, "imports/X-Files", Pick{Kind: Game, System: "pc", Game: "The X-Files Game"}); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	if x := item(t, st, "X-Files"); x.Problem != "" || x.System != "pc" || x.Game != "The X-Files Game" || x.Title != "The X-Files Game" {
		t.Fatalf("picked %+v", x)
	}
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st = settled(t, s)
	if x := item(t, st, "X-Files"); x.Status != Done {
		t.Fatalf("%+v", x)
	}
	for i := 1; i <= 7; i++ {
		if _, err := os.Stat(filepath.Join(roots, "pc", "The X-Files Game", fmt.Sprintf("XFILES%d.bin", i))); err != nil {
			t.Errorf("disc %d isn't in the game's folder", i)
		}
	}
}

func gridleeZip(t *testing.T, p string) {
	t.Helper()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"gridlee.1", "gridlee.2", "gridlee.7"} {
		w, _ := zw.Create(n + ".bin")
		w.Write([]byte("rom"))
	}
	zw.Close()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, zb.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gamesReady(t *testing.T, s *Service) string {
	t.Helper()
	ctx := context.Background()
	roots := t.TempDir()
	if _, err := s.Store.AddRootFolder(ctx, mediatypes.Game, roots); err != nil {
		t.Fatal(err)
	}
	mediatypes.SetEnabled(ctx, s.Store, mediatypes.Game, true)
	return roots
}

func importFolder(t *testing.T, s *Service, p string) Item {
	t.Helper()
	ctx := context.Background()
	if err := s.CopyIn(ctx, []string{p}); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if _, err := s.Set(ctx, "imports/"+filepath.Base(p), Pick{Kind: Game}); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	return item(t, settled(t, s), filepath.Base(p))
}

// An emulator's whole folder is imported: its ROMs go in, and its samples
// and artwork, which share the games' names, are left alone.
func TestImportEmulatorFolder(t *testing.T) {
	s := newService(t)
	roots := gamesReady(t, s)
	src := t.TempDir()
	gridleeZip(t, filepath.Join(src, "MAME", "roms", "gridlee.zip"))
	write(t, filepath.Join(src, "MAME", "samples", "gridlee.zip"), 40)
	write(t, filepath.Join(src, "MAME", "artwork", "gridlee.zip"), 50)
	write(t, filepath.Join(src, "MAME", "roms", "Lone (Europe).nes"), 64)
	// The emulator's own files aren't games, and aren't worth a mention.
	write(t, filepath.Join(src, "MAME", "mame0154b.exe"), 10)
	write(t, filepath.Join(src, "MAME", "mame.sym"), 10)
	write(t, filepath.Join(src, "MAME", "nl_examples", "7400_astable.c"), 10)
	write(t, filepath.Join(src, "MAME", "Microsoft.VC90.CRT", "msvcr90.dll"), 10)

	m := importFolder(t, s, filepath.Join(src, "MAME"))
	if m.Status != Done || m.Problem != "" {
		t.Fatalf("emulator folder %+v", m)
	}
	for _, p := range []string{"nes/Lone (Europe).nes", "mame/gridlee.zip"} {
		if _, err := os.Stat(filepath.Join(roots, p)); err != nil {
			t.Errorf("%s isn't in the games folder", p)
		}
	}
	if fi, _ := os.Stat(filepath.Join(roots, "mame", "gridlee.zip")); fi == nil || fi.Size() == 40 || fi.Size() == 50 {
		t.Fatal("a sample or artwork file went in as the game")
	}
}

// A game whose name is taken in the games folder by a different file is
// left in the imports folder, and the rest still go in.
func TestImportGamesSkipsAClash(t *testing.T) {
	s := newService(t)
	roots := gamesReady(t, s)
	write(t, filepath.Join(roots, "mame", "gridlee.zip"), 10)
	src := t.TempDir()
	gridleeZip(t, filepath.Join(src, "Old", "gridlee.zip"))
	write(t, filepath.Join(src, "Old", "Lone (Europe).nes"), 64)

	m := importFolder(t, s, filepath.Join(src, "Old"))
	if m.Status != Done || !strings.Contains(m.Problem, "gridlee.zip") || !strings.Contains(m.Problem, "same name") {
		t.Fatalf("old %+v", m)
	}
	if _, err := os.Stat(filepath.Join(roots, "nes", "Lone (Europe).nes")); err != nil {
		t.Fatal("the clash stopped the rest going in")
	}
	if fi, _ := os.Stat(filepath.Join(roots, "mame", "gridlee.zip")); fi == nil || fi.Size() != 10 {
		t.Fatal("the file already in the games folder was replaced")
	}
	dir, _ := s.dir()
	if _, err := os.Stat(filepath.Join(dir, "Old", "gridlee.zip")); err != nil {
		t.Fatal("the clashing game was deleted from the imports folder")
	}
}

// A copy that a restart cut off carries on from where it got to, and a
// half-made item no copy will finish is removed.
func TestResumeCopy(t *testing.T) {
	s := newService(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "Big", "a.nes"), 100)
	write(t, filepath.Join(src, "Big", "sub", "b.nes"), 200)
	dir, err := s.dir()
	if err != nil {
		t.Fatal(err)
	}
	// a.nes made it before the restart.
	write(t, filepath.Join(dir, ".Big.part", "a.nes"), 100)
	write(t, filepath.Join(dir, ".Stale.part", "x.nes"), 10)
	b, _ := json.Marshal([]copyJob{{From: filepath.Join(src, "Big"), Name: "Big"}})
	os.WriteFile(filepath.Join(dir, copyingFile), b, 0o644)

	s.Resume()
	st := settled(t, s)
	if st.Copying.Error != "" || st.Copying.Total != 300 || st.Copying.Done != 300 {
		t.Fatalf("copy %+v", st.Copying)
	}
	if _, err := os.Stat(filepath.Join(dir, "Big", "sub", "b.nes")); err != nil {
		t.Fatal("the copy didn't finish")
	}
	for _, n := range []string{".Stale.part", ".Big.part", copyingFile} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s is still there", n)
		}
	}
}

// A copy whose source has gone says so instead of carrying on.
func TestResumeCopySourceGone(t *testing.T) {
	s := newService(t)
	dir, _ := s.dir()
	write(t, filepath.Join(dir, ".Gone.part", "a.nes"), 10)
	b, _ := json.Marshal([]copyJob{{From: filepath.Join(t.TempDir(), "Gone"), Name: "Gone"}})
	os.WriteFile(filepath.Join(dir, copyingFile), b, 0o644)
	s.Resume()
	st := settled(t, s)
	if !strings.Contains(st.Copying.Error, "restarted") {
		t.Fatalf("copy %+v", st.Copying)
	}
	if _, err := os.Stat(filepath.Join(dir, ".Gone.part")); err == nil {
		t.Fatal("the half-made copy was kept")
	}
}

// A folder holding an AAE and a MAME install: each install's games go
// under its own emulator, so AAE's and MAME's sets of the same name don't
// clash, and the item says which emulator it is.
func TestImportEmulatorsApart(t *testing.T) {
	s := newService(t)
	roots := gamesReady(t, s)
	src := t.TempDir()
	old := filepath.Join(src, "OLD Games")
	gridleeZip(t, filepath.Join(old, "AAE", "roms", "gridlee.zip"))
	write(t, filepath.Join(old, "AAE", "aae092808", "aae.exe"), 10)
	write(t, filepath.Join(old, "AAE", "aae092808", "samples", "gridlee.zip"), 40)
	gridleeZip(t, filepath.Join(old, "MAME", "roms", "gridlee.zip"))
	write(t, filepath.Join(old, "MAME", "roms", "extra.zip"), 30) // not a real zip
	write(t, filepath.Join(old, "MAME", "mame.exe"), 10)
	os.WriteFile(filepath.Join(old, "MAME", "whatsnew.txt"), []byte("0.154\n-----\n"), 0o644)

	ctx := context.Background()
	if err := s.CopyIn(ctx, []string{filepath.Join(old, "MAME")}); err != nil {
		t.Fatal(err)
	}
	if m := item(t, settled(t, s), "MAME"); m.System != "mame" || m.Title != "MAME 0.154" {
		t.Fatalf("mame item %+v", m)
	}
	if err := s.CopyIn(ctx, []string{filepath.Join(old, "AAE")}); err != nil {
		t.Fatal(err)
	}
	if a := item(t, settled(t, s), "AAE"); a.System != "aae" || a.Title != "AAE" {
		t.Fatalf("aae item %+v", a)
	}
	if err := s.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st := settled(t, s)
	for _, n := range []string{"MAME", "AAE"} {
		if it := item(t, st, n); it.Status != Done || it.Problem != "" {
			t.Fatalf("%s %+v", n, it)
		}
	}
	for _, p := range []string{"mame/gridlee.zip", "mame/extra.zip", "aae/gridlee.zip"} {
		if _, err := os.Stat(filepath.Join(roots, p)); err != nil {
			t.Errorf("%s isn't in the games folder", p)
		}
	}
}
