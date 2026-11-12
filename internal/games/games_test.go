package games

import (
	"archive/zip"
	"bytes"
	"context"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// zipOf writes a zip whose files claim the given CRCs, as a set or a game
// in an archive: nothing reads past the archive's directory.
func zipOf(t *testing.T, p string, files map[string]uint32) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, crc := range files {
		w, err := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc, CompressedSize64: 4, UncompressedSize64: 4})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("data"))
	}
	z.Close()
	f.Close()
}

func touch(t *testing.T, p string, b []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func keys(f []found) []string {
	var out []string
	for _, x := range f {
		out = append(out, filepath.Base(x.path)+"="+x.sys.Key)
	}
	slices.Sort(out)
	return out
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	zipOf(t, filepath.Join(root, "roms", "mame", "pacman.zip"), map[string]uint32{"pacman.6e": 1})
	touch(t, filepath.Join(root, "roms", "Super Nintendo", "Game (USA).sfc"), []byte("x"))
	// Named for nothing, but full of Game Boy Advance games in zips.
	zipOf(t, filepath.Join(root, "roms", "handheld", "Game (USA).zip"), map[string]uint32{"Game (USA).gba": 1})
	// Arcade sets, by what's in them.
	zipOf(t, filepath.Join(root, "roms", "cabinets", "dkong.zip"), map[string]uint32{"c_5et_g.bin": 1})
	touch(t, filepath.Join(root, "roms", "notes", "readme.txt"), []byte("x"))
	touch(t, filepath.Join(root, "roms", "mystery", "game.bin"), []byte("x"))
	zipOf(t, filepath.Join(root, "roms", "mystery", "x.zip"), map[string]uint32{"a.bin": 1})
	touch(t, filepath.Join(root, "roms", "mystery", "y.iso"), []byte("x"))
	got, unsure, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Super Nintendo=snes", "cabinets=mame", "handheld=gba", "mame=mame", "mystery=mame"}
	if !slices.Equal(keys(got), want) {
		t.Errorf("found %v, want %v", keys(got), want)
	}
	if len(unsure) != 0 {
		t.Errorf("unsure %v", unsure)
	}

	// A root folder that's one system's folder.
	one := filepath.Join(t.TempDir(), "n64")
	touch(t, filepath.Join(one, "Game (USA).z64"), []byte("x"))
	if got, _, _ := Detect(one); !slices.Equal(keys(got), []string{"n64=n64"}) {
		t.Errorf("one system: %v", keys(got))
	}
	// Game files of no system Box Office can tell.
	odd := t.TempDir()
	touch(t, filepath.Join(odd, "games", "a.zip"), nil)
	if got, unsure, _ := Detect(odd); len(got) != 0 || !slices.Equal(unsure, []string{filepath.Join(odd, "games")}) {
		t.Errorf("odd: %v %v", keys(got), unsure)
	}
}

func TestReadCatver(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "catver.ini"), []byte(";; catver\n[Category]\npacman=Maze / Collect\nsf2=Fighter / Versus * Mature *\n\n[VerAdded]\npacman=.37b\n"))
	g := readCatver(dir)
	if g["pacman"] != "Maze" || g["sf2"] != "Fighter" || len(g) != 2 {
		t.Errorf("%v", g)
	}
}

const testList = `<?xml version="1.0"?>
<mame build="0.289 (mame0289)">
	<machine name="puckman"><description>Puck Man (Japan set 1)</description><year>1980</year><manufacturer>Namco</manufacturer>
		<rom name="pm1_prg1.6e" size="2048" crc="f36e88ab"/><input players="2"/></machine>
	<machine name="pacman" cloneof="puckman" romof="puckman"><description>Pac-Man (Midway)</description><year>1980</year><manufacturer>Namco (Midway license)</manufacturer>
		<rom name="pacman.6e" size="4096" crc="c1e6ab10"/><input players="2"/></machine>
</mame>`

// sources serves MAME's releases and libretro's database and thumbnails
// as GitHub would, and counts what's asked for.
func sources(t *testing.T, snesCRC uint32) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	var lx bytes.Buffer
	z := zip.NewWriter(&lx)
	w, _ := z.Create("mame0289.xml")
	w.Write([]byte(testList))
	z.Close()
	art := new(bytes.Buffer)
	img := image.NewRGBA(image.Rect(0, 0, 960, 1280))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	png.Encode(art, img)
	dat := `clrmamepro ( name "Nintendo - Super Nintendo Entertainment System" version "2026.10.07" )
game ( name "Super Metroid (Japan, USA) (En,Ja)" rom ( name "Super Metroid (Japan, USA) (En,Ja).sfc" size 4 crc ` + crcHex(snesCRC) + ` ) )
`
	genre := `clrmamepro ( name "x" )
game ( comment "Super Metroid" genre "Adventure" rom ( crc ` + crcHex(snesCRC) + ` ) )
`
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/mame/latest":
			w.Header().Set("Location", "https://github.com/mamedev/mame/releases/tag/mame0289")
			w.WriteHeader(http.StatusFound)
		case "/mame/download/mame0289/mame0289lx.zip":
			w.Write(lx.Bytes())
		case "/db/metadat/no-intro/Nintendo - Super Nintendo Entertainment System.dat":
			w.Write([]byte(dat))
		case "/db/metadat/genre/Nintendo - Super Nintendo Entertainment System.dat":
			w.Write([]byte(genre))
		case "/art/MAME/Named_Boxarts/Puck Man (Japan set 1).png",
			"/art/Nintendo_-_Super_Nintendo_Entertainment_System/Named_Boxarts/Super Metroid (World).png":
			w.Write(art.Bytes())
		case "/art/Nintendo_-_Super_Nintendo_Entertainment_System/Named_Boxarts/Super Metroid (Japan, USA) (En,Ja).png":
			// Another name for a picture is a link, as git keeps it.
			w.Write([]byte("Super Metroid (World).png"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	return hs, func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(asked)
	}
}

func crcHex(c uint32) string {
	const digits = "0123456789ABCDEF"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[c&0xf]
		c >>= 4
	}
	return string(b)
}

func wait(t *testing.T, s *Service) Progress {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if p := s.Progress(); !p.Running {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the check didn't finish")
	return Progress{}
}

func TestCheckFindsAndChecksGames(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	snes := []byte("SNES")
	hs, asked := sources(t, crc32.ChecksumIEEE(snes))
	s := &Service{Store: st, Dir: t.TempDir(), MAMEReleases: hs.URL + "/mame", LibretroDB: hs.URL + "/db",
		Art: []string{hs.URL + "/art/{repo}/{kind}/{name}.png"}}
	if _, err := s.Start(ctx, 0, ""); err != ErrOff {
		t.Fatalf("checked with Games off: %v", err)
	}
	mediatypes.SetEnabled(ctx, st, mediatypes.Game, true)
	root := t.TempDir()
	zipOf(t, filepath.Join(root, "mame", "puckman.zip"), map[string]uint32{"pm1_prg1.6e": 0xf36e88ab})
	zipOf(t, filepath.Join(root, "mame", "pacman.zip"), map[string]uint32{"pacman.6e": 0xc1e6ab10})
	zipOf(t, filepath.Join(root, "mame", "galaxian.zip"), map[string]uint32{"7f": 1})
	touch(t, filepath.Join(root, "snes", "metroid.sfc"), snes)
	touch(t, filepath.Join(root, "snes", "My Hack (World).sfc"), []byte("hack"))
	touch(t, filepath.Join(root, "snes", "super_metroid.sfc"), []byte("v2"))
	touch(t, filepath.Join(root, "unsure", "game.zip"), nil)
	if _, err := st.AddRootFolder(ctx, mediatypes.Game, root); err != nil {
		t.Fatal(err)
	}
	before := map[string]time.Time{}
	if _, err := s.Start(ctx, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, 0, ""); err != ErrBusy && s.Progress().Running {
		t.Errorf("started twice: %v", err)
	}
	p := wait(t, s)
	if p.Error != "" || p.Systems != 2 || p.Games != 6 {
		t.Fatalf("%+v", p)
	}
	systems, _ := st.GameSystems(ctx)
	if len(systems) != 2 {
		t.Fatalf("%+v", systems)
	}
	byName := map[string]store.Game{}
	for _, sys := range systems {
		games, _ := st.Games(ctx, sys.ID)
		for _, g := range games {
			byName[g.Name] = g
			before[g.Name] = g.AddedAt
		}
		switch sys.System {
		case "mame":
			if sys.List != "MAME 0.289" || sys.Format != gamedat.Split || sys.Games != 3 || sys.Groups != 2 || sys.Problems != 1 {
				t.Errorf("mame %+v", sys)
			}
		case "snes":
			if sys.List != "No-Intro 2026.10.07" || sys.Games != 3 || sys.Problems != 2 {
				t.Errorf("snes %+v", sys)
			}
		}
	}
	if g := byName["pacman"]; g.Title != "Pac-Man (Midway)" || g.Parent != "puckman" || g.ParentTitle != "Puck Man (Japan set 1)" || g.Status != gamedat.Complete || g.Players != 2 {
		t.Errorf("pacman %+v", g)
	}
	if g := byName["galaxian"]; g.Status != gamedat.Unknown {
		t.Errorf("galaxian %+v", g)
	}
	m := byName["Super Metroid (Japan, USA) (En,Ja)"]
	if m.Title != "Super Metroid" || m.Status != gamedat.Complete || m.Genre != "Adventure" || m.CRC != crcHex(crc32.ChecksumIEEE(snes)) {
		t.Errorf("metroid %+v", m)
	}
	if g := byName["My Hack (World)"]; g.Status != gamedat.Unknown || g.Title != "My Hack" {
		t.Errorf("hack %+v", g)
	}

	// Art is fetched once, made small, and kept; a game with none is
	// remembered as having none.
	puck := byName["puckman"]
	p1, err := s.ArtFile(ctx, puck, "mame")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p1)
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != artWidth || cfg.Height != 640 {
		t.Errorf("art %+v %v", cfg, err)
	}
	// The clone has no picture of its own, so it has its parent's.
	if _, err := s.ArtFile(ctx, byName["pacman"], "mame"); err != nil {
		t.Errorf("clone's art: %v", err)
	}
	// A version not on the list has the listed one's picture, found
	// through a link.
	if g := byName["super_metroid"]; g.Status != gamedat.Unknown || g.Art != "Super Metroid (Japan, USA) (En,Ja)" {
		t.Errorf("v2 %+v", g)
	} else if _, err := s.ArtFile(ctx, g, "snes"); err != nil {
		t.Errorf("v2's art: %v", err)
	}
	n := asked()
	if _, err := s.ArtFile(ctx, puck, "mame"); err != nil || asked() != n {
		t.Errorf("fetched again: %v", err)
	}
	if _, err := s.ArtFile(ctx, byName["galaxian"], "mame"); err != ErrNoArt {
		t.Errorf("galaxian: %v", err)
	}
	n = asked()
	s.ArtFile(ctx, byName["galaxian"], "mame")
	if asked() != n {
		t.Error("looked again for art that isn't there")
	}

	// Checking again keeps when each game was found, and a system set to
	// Do not touch is left as it was.
	for _, sys := range systems {
		if sys.System == "snes" {
			st.LockGameSystem(ctx, sys.ID, true)
		}
	}
	os.Remove(filepath.Join(root, "snes", "metroid.sfc"))
	s.Start(ctx, 0, "")
	wait(t, s)
	systems, _ = st.GameSystems(ctx)
	for _, sys := range systems {
		games, _ := st.Games(ctx, sys.ID)
		if sys.System == "snes" && len(games) != 3 {
			t.Errorf("a locked system changed: %d games", len(games))
		}
		for _, g := range games {
			if !g.AddedAt.Equal(before[g.Name]) {
				t.Errorf("%s found anew", g.Name)
			}
		}
	}

	// A newer MAME version is checked against its own list.
	if err := s.SetMAMEVersion(ctx, "0.100"); err == nil {
		t.Error("took a version too old to fetch")
	}
	if v, err := s.MAMEVersion(ctx); err != nil || v != "0.289" {
		t.Errorf("version %q %v", v, err)
	}
	set, err := s.Settings(ctx)
	if err != nil || set.Newest != "0.289" || set.Versions[0] != "0.289" || set.Versions[len(set.Versions)-1] != "0.162" {
		t.Errorf("settings %+v %v", set.Newest, err)
	}
}

func TestVersions(t *testing.T) {
	if tagVersion("https://github.com/mamedev/mame/releases/tag/mame0289") != "0.289" || tagVersion("v1") != "" {
		t.Error("tag")
	}
	if fileVersion("0.289") != "0289" || fileVersion("0.1000") != "1000" || fileVersion("x") != "" {
		t.Error("file version")
	}
	if !ValidVersion("0.162") || ValidVersion("0.161") || ValidVersion("1.0") || ValidVersion("0.x") {
		t.Error("valid")
	}
}
