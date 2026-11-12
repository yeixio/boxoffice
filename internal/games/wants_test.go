package games

import (
	"archive/zip"
	"context"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// realZip writes a zip of the files as given.
func realZip(t *testing.T, p string, files map[string][]byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, b := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	z.Close()
	f.Close()
}

func TestWantDownloadAndImportGames(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	snes := []byte("SNES")
	hs, _ := sources(t, crc32.ChecksumIEEE(snes))
	var searched []string
	s := &Service{Store: st, Dir: t.TempDir(), MAMEReleases: hs.URL + "/mame", LibretroDB: hs.URL + "/db",
		Art: []string{hs.URL + "/art/{repo}/{kind}/{name}.png"},
		Search: func(_ context.Context, w store.GameWant) error {
			searched = append(searched, w.Name)
			return nil
		}}
	if _, err := s.LookupGames(ctx, "metroid", nil); err != ErrOff {
		t.Fatalf("looked up with Games off: %v", err)
	}
	mediatypes.SetEnabled(ctx, st, mediatypes.Game, true)
	if _, err := s.Want(ctx, "snes", "Super Metroid (Japan, USA) (En,Ja)"); err == nil || !strings.Contains(err.Error(), "root folder") {
		t.Fatalf("wanted with no games root folder: %v", err)
	}
	root := t.TempDir()
	if _, err := st.AddRootFolder(ctx, mediatypes.Game, root); err != nil {
		t.Fatal(err)
	}

	// Looking up: a console game's versions, and a MAME clone with its
	// parent.
	found, err := s.LookupGames(ctx, "super met", []string{"snes"})
	if err != nil || len(found) != 1 || found[0].Best.Name != "Super Metroid (Japan, USA) (En,Ja)" || found[0].Title != "Super Metroid" || found[0].OnDisk {
		t.Fatalf("snes lookup %+v %v", found, err)
	}
	if found[0].Best.Year != "" || found[0].Best.Genre != "Adventure" || found[0].Best.Size != 4 {
		t.Errorf("facts %+v", found[0].Best)
	}
	found, err = s.LookupGames(ctx, "pac-man", []string{"mame"})
	if err != nil || len(found) != 1 || found[0].Best.Name != "puckman" || len(found[0].Versions) != 2 {
		t.Fatalf("mame lookup %+v %v", found, err)
	}
	if found, _ := s.LookupGames(ctx, "zelda", []string{"snes"}); len(found) != 0 {
		t.Errorf("zelda %+v", found)
	}

	// Wanting looks for it, once.
	w, err := s.Want(ctx, "snes", "Super Metroid (Japan, USA) (En,Ja)")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.Want(ctx, "snes", "Super Metroid (Japan, USA) (En,Ja)"); err != nil || again.ID != w.ID {
		t.Errorf("wanted twice: %+v %v", again, err)
	}
	if len(searched) != 1 || w.Group != "supermetroid" || w.Size != 4 {
		t.Errorf("searched %v, want %+v", searched, w)
	}
	if _, err := s.Want(ctx, "snes", "Super Mario World (USA)"); err == nil {
		t.Error("wanted a game not on the list")
	}
	if found, _ := s.LookupGames(ctx, "metroid", []string{"snes"}); len(found) != 1 || found[0].WantID != w.ID {
		t.Errorf("not marked wanted: %+v", found)
	}

	// A download that isn't it is caught; one with it in a pack of games
	// brings only it in, named as No-Intro names it.
	dl := t.TempDir()
	realZip(t, filepath.Join(dl, "wrong", "pack.zip"), map[string][]byte{"Some Game.sfc": []byte("nope")})
	if why, err := s.MatchDownload(ctx, w.ID, filepath.Join(dl, "wrong"), []string{"pack.zip"}); err != nil || !strings.Contains(why, "none of the 1 SNES games") {
		t.Errorf("wrong game: %q %v", why, err)
	}
	if why, err := s.MatchDownload(ctx, w.ID, filepath.Join(dl, "wrong"), []string{}); err != nil || !strings.Contains(why, "no Super Nintendo game") {
		t.Errorf("no game: %q %v", why, err)
	}
	realZip(t, filepath.Join(dl, "right", "pack.zip"), map[string][]byte{"metroid.sfc": snes, "Some Game.sfc": []byte("nope"), "readme.txt": []byte("hi")})
	if why, err := s.MatchDownload(ctx, w.ID, filepath.Join(dl, "right"), []string{"pack.zip"}); err != nil || why != "" {
		t.Fatalf("right game: %q %v", why, err)
	}
	to, err := s.ImportDownload(ctx, w.ID, filepath.Join(dl, "right"), []string{"pack.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "snes", "Super Metroid (Japan, USA) (En,Ja).sfc"); to != want {
		t.Errorf("went to %s, want %s", to, want)
	}
	if b, _ := os.ReadFile(to); string(b) != "SNES" {
		t.Errorf("copied %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dl, "right", ".boxoffice-*")); len(left) != 0 {
		t.Errorf("left %v", left)
	}
	// The check it starts finds it, and it's wanted no longer.
	wait(t, s)
	if _, err := st.GameWant(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("still wanted: %v", err)
	}
	if found, _ := s.LookupGames(ctx, "metroid", []string{"snes"}); len(found) != 1 || !found[0].OnDisk {
		t.Errorf("not on disk: %+v", found)
	}
	if _, err := s.Want(ctx, "snes", "Super Metroid (Japan, USA) (En,Ja)"); !errors.Is(err, ErrOnDisk) {
		t.Errorf("wanted what's on disk: %v", err)
	}

	// A MAME set comes in with its parent; one named for it with none of
	// its files isn't it.
	m, err := s.Want(ctx, "mame", "pacman")
	if err != nil {
		t.Fatal(err)
	}
	zipOf(t, filepath.Join(dl, "fake", "pacman.zip"), map[string]uint32{"x.6e": 7})
	if why, _ := s.MatchDownload(ctx, m.ID, filepath.Join(dl, "fake"), []string{"pacman.zip"}); !strings.Contains(why, "none of the files") {
		t.Errorf("fake set: %q", why)
	}
	if why, _ := s.MatchDownload(ctx, m.ID, filepath.Join(dl, "fake"), []string{}); !strings.Contains(why, "no pacman set") {
		t.Errorf("no set: %q", why)
	}
	zipOf(t, filepath.Join(dl, "pac", "Pac-Man", "pacman.zip"), map[string]uint32{"pacman.6e": 0xc1e6ab10})
	zipOf(t, filepath.Join(dl, "pac", "Pac-Man", "puckman.zip"), map[string]uint32{"pm1_prg1.6e": 0xf36e88ab})
	files := []string{"Pac-Man/pacman.zip", "Pac-Man/puckman.zip"}
	if to, err := s.ImportDownload(ctx, m.ID, filepath.Join(dl, "pac"), files); err != nil || to != filepath.Join(root, "mame", "pacman.zip") {
		t.Fatalf("mame import %s %v", to, err)
	}
	if _, err := os.Stat(filepath.Join(root, "mame", "puckman.zip")); err != nil {
		t.Errorf("parent: %v", err)
	}
	wait(t, s)
	if _, err := st.GameWant(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pacman still wanted: %v", err)
	}

	// A system on Do not touch takes no games.
	systems, _ := st.GameSystems(ctx)
	for _, sys := range systems {
		st.LockGameSystem(ctx, sys.ID, true)
	}
	if _, err := s.Want(ctx, "mame", "puckman"); err == nil || !strings.Contains(err.Error(), "Do not touch") {
		t.Error("wanted a game for a system on Do not touch")
	}
}
