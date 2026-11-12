package games

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/store/storetest"
)

// psxImage is a PlayStation disc image as raw 2352-byte sectors, with a
// SYSTEM.CNF naming its serial.
func psxImage(serial string) []byte {
	cnf := []byte("BOOT = cdrom:\\" + serial + ";1\r\n")
	data := make([]byte, 20*2048)
	pvd := data[16*2048:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	copy(pvd[8:], "PLAYSTATION")
	record := func(lba, size int, dir bool, name string) []byte {
		r := make([]byte, 33+len(name))
		if len(r)%2 == 1 {
			r = append(r, 0)
		}
		r[0] = byte(len(r))
		binary.LittleEndian.PutUint32(r[2:], uint32(lba))
		binary.LittleEndian.PutUint32(r[10:], uint32(size))
		if dir {
			r[25] = 2
		}
		r[32] = byte(len(name))
		copy(r[33:], name)
		return r
	}
	copy(pvd[156:], record(18, 2048, true, "\x00"))
	copy(data[18*2048:], record(19, len(cnf), false, "SYSTEM.CNF;1"))
	copy(data[19*2048:], cnf)
	out := make([]byte, 0, 20*2352)
	for i := range 20 {
		s := make([]byte, 2352)
		copy(s, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
		s[15] = 2
		copy(s[24:], data[i*2048:(i+1)*2048])
		out = append(out, s...)
	}
	return out
}

// sfo is a PARAM.SFO with one text value per key.
func sfo(vals ...string) []byte {
	var kt, dt, idx []byte
	for i := 0; i+1 < len(vals); i += 2 {
		e := make([]byte, 16)
		binary.LittleEndian.PutUint16(e, uint16(len(kt)))
		binary.LittleEndian.PutUint16(e[2:], 0x0204)
		v := append([]byte(vals[i+1]), 0)
		binary.LittleEndian.PutUint32(e[4:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[12:], uint32(len(dt)))
		kt = append(kt, vals[i]+"\x00"...)
		dt = append(dt, v...)
		idx = append(idx, e...)
	}
	h := make([]byte, 20)
	copy(h, "\x00PSF")
	binary.LittleEndian.PutUint32(h[8:], uint32(20+len(idx)))
	binary.LittleEndian.PutUint32(h[12:], uint32(20+len(idx)+len(kt)))
	binary.LittleEndian.PutUint32(h[16:], uint32(len(vals)/2))
	return slices.Concat(h, idx, kt, dt)
}

func cue(bins ...string) []byte {
	var b strings.Builder
	for i, n := range bins {
		b.WriteString("FILE \"" + n + "\" BINARY\n")
		kind := "MODE2/2352"
		if i > 0 {
			kind = "AUDIO"
		}
		b.WriteString("  TRACK 0" + string(rune('1'+i)) + " " + kind + "\n    INDEX 01 00:00:00\n")
	}
	return []byte(b.String())
}

// discSources serves Redump's PlayStation and PlayStation 3 lists and
// titledb's list of the Switch's games.
func discSources(t *testing.T) *httptest.Server {
	t.Helper()
	psx := `clrmamepro ( name "Sony - PlayStation" version "2026.10.07" )
game ( name "Final Test (USA)" serial "SLUS-00067" rom ( name "Final Test (USA) (Track 1).bin" size 47040 crc 11111111 ) )
game ( name "Final Test (Europe)" serial "SLES-00067" rom ( name "Final Test (Europe) (Track 1).bin" size 47040 crc 11111112 ) )
game ( name "Another Game (Europe)" serial "SLES-00100" rom ( name "Another Game (Europe).bin" size 47040 crc 11111114 ) )
game ( name "Named Right (USA)" serial "SLUS-00999" rom ( name "Named Right (USA).bin" size 47040 crc 11111113 ) )
`
	ps3 := `clrmamepro ( name "Sony - PlayStation 3" version "2026.10.01" )
game ( name "Resistance - Fall of Man (USA)" serial "BLUS-30001" rom ( name "Resistance - Fall of Man (USA).iso" size 10 crc 21111111 ) )
`
	year := `clrmamepro ( name "x" )
game ( comment "Final Test (USA)" releaseyear "1997" rom ( crc 11111111 ) )
`
	titledb := `{"1": {"id": "01002B30028F6000", "name": "Celeste™", "publisher": "Matt Makes Games", "releaseDate": 20180125, "numberOfPlayers": 1,
	"category": ["Platformer"], "iconUrl": "https://img-eshop.cdn.nintendo.net/i/celeste.jpg", "size": 1000000},
	"2": {"id": "01002B30028F6800", "name": "Celeste Update"}}`
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/db/metadat/redump/Sony - PlayStation.dat":
			w.Write([]byte(psx))
		case "/db/metadat/redump/Sony - PlayStation 3.dat":
			w.Write([]byte(ps3))
		case "/db/metadat/releaseyear/Sony - PlayStation.dat":
			w.Write([]byte(year))
		case "/titledb/US.en.json":
			w.Write([]byte(titledb))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	return hs
}

func TestCheckDiscsAndDownloads(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	hs := discSources(t)
	s := &Service{Store: st, Dir: t.TempDir(), LibretroDB: hs.URL + "/db", TitleDB: hs.URL + "/titledb", Art: []string{hs.URL + "/art/{repo}/{kind}/{name}.png"}}
	mediatypes.SetEnabled(ctx, st, mediatypes.Game, true)
	root := t.TempDir()
	// A cue sheet and its tracks, known by the serial on the disc.
	touch(t, filepath.Join(root, "psx", "ft", "Track 1.bin"), psxImage("SLUS_000.67"))
	touch(t, filepath.Join(root, "psx", "ft", "Track 2.bin"), make([]byte, 2352))
	touch(t, filepath.Join(root, "psx", "ft", "My Copy.cue"), cue("Track 1.bin", "Track 2.bin"))
	// A CHD can't be read, but is named as Redump names it.
	touch(t, filepath.Join(root, "psx", "Named Right (USA).chd"), []byte("MComprHD"))
	touch(t, filepath.Join(root, "psx", "Mystery Disc.chd"), []byte("MComprHD"))
	// Switch downloads, by the title ID in their names.
	touch(t, filepath.Join(root, "switch", "Celeste [01002B30028F6000][v0].nsp"), []byte("PFS0\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	touch(t, filepath.Join(root, "switch", "Celeste [01002B30028F6800][v65536].nsp"), []byte("PFS0\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	// PlayStation 3 games dumped whole, in a folder named for nothing.
	touch(t, filepath.Join(root, "PS3 Games", "BLUS30001", "PS3_GAME", "PARAM.SFO"), sfo("TITLE_ID", "BLUS30001", "TITLE", "Resistance"))
	touch(t, filepath.Join(root, "PS3 Games", "BLUS30001", "PS3_GAME", "USRDIR", "EBOOT.BIN"), make([]byte, 100))
	// PC games are folders.
	touch(t, filepath.Join(root, "pc", "Portal 2", "portal2.exe"), make([]byte, 50))
	touch(t, filepath.Join(root, "pc", "Doom.Eternal-CODEX", "setup.exe"), make([]byte, 50))
	if _, err := st.AddRootFolder(ctx, mediatypes.Game, root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, 0, ""); err != nil {
		t.Fatal(err)
	}
	if p := wait(t, s); p.Error != "" || p.Systems != 4 {
		t.Fatalf("%+v", p)
	}
	systems, _ := st.GameSystems(ctx)
	got := map[string]store.Game{}
	for _, sys := range systems {
		games, _ := st.Games(ctx, sys.ID)
		for _, g := range games {
			got[sys.System+"/"+g.Name] = g
		}
		switch sys.System {
		case "psx":
			if sys.List != "Redump 2026.10.07" || sys.Games != 3 || sys.Problems != 1 {
				t.Errorf("psx %+v", sys)
			}
		case "switch":
			if sys.List != "Nintendo eShop" || sys.Games != 2 || sys.Groups != 1 || sys.Problems != 0 {
				t.Errorf("switch %+v", sys)
			}
		case "ps3":
			if filepath.Base(sys.Path) != "PS3 Games" {
				t.Errorf("ps3 %+v", sys)
			}
		}
	}
	names := make([]string, 0, len(got))
	for k := range got {
		names = append(names, k)
	}
	slices.Sort(names)
	want := []string{"pc/Doom.Eternal-CODEX", "pc/Portal 2", "ps3/Resistance - Fall of Man (USA)", "psx/Final Test (USA)", "psx/Mystery Disc", "psx/Named Right (USA)",
		"switch/Celeste", "switch/Celeste (Update)"}
	if !slices.Equal(names, want) {
		t.Fatalf("games %v, want %v", names, want)
	}
	if g := got["psx/Final Test (USA)"]; g.Status != gamedat.Identified || g.Serial != "SLUS-00067" || g.Year != "1997" || !strings.HasSuffix(g.Path, "My Copy.cue") || g.Size != 20*2352+2352+int64(len(cue("Track 1.bin", "Track 2.bin"))) {
		t.Errorf("cue %+v", g)
	}
	if g := got["psx/Mystery Disc"]; g.Status != gamedat.Unknown {
		t.Errorf("mystery %+v", g)
	}
	if g := got["switch/Celeste"]; g.Status != gamedat.Identified || g.Title != "Celeste" || g.Maker != "Matt Makes Games" || g.Year != "2018" || g.Art != "https://img-eshop.cdn.nintendo.net/i/celeste.jpg" {
		t.Errorf("celeste %+v", g)
	}
	if g := got["switch/Celeste (Update)"]; g.Parent != got["switch/Celeste"].Parent || len(g.Problems) != 1 || g.Problems[0].Kind != ProblemUpdate {
		t.Errorf("update %+v", g)
	}
	if g := got["pc/Doom.Eternal-CODEX"]; g.Title != "Doom Eternal" || g.Status != gamedat.Identified {
		t.Errorf("pc %+v", g)
	}

	// Looking up a Switch game finds it on the eShop's list; a PC game is
	// what's asked for.
	ls, err := s.LookupGames(ctx, "celeste", []string{"switch"})
	if err != nil || len(ls) != 1 || ls[0].Best.Serial != "01002B30028F6000" || !ls[0].OnDisk {
		t.Fatalf("switch lookup %+v %v", ls, err)
	}
	ls, err = s.LookupGames(ctx, "half-life 2", []string{"pc"})
	if err != nil || len(ls) != 1 || !ls[0].Free || ls[0].Title != "half-life 2" {
		t.Fatalf("pc lookup %+v %v", ls, err)
	}

	// A download of a PlayStation game is checked by the serial on its
	// disc, and goes in with its tracks, named as Redump names it.
	if _, err := s.Want(ctx, "psx", "Final Test (Europe)"); err != ErrOnDisk {
		t.Errorf("wanted a game on disk: %v", err)
	}
	w, err := s.Want(ctx, "psx", "Another Game (Europe)")
	if err != nil {
		t.Fatal(err)
	}
	dl := t.TempDir()
	touch(t, filepath.Join(dl, "ft.bin"), psxImage("SLES_001.00"))
	touch(t, filepath.Join(dl, "ft.cue"), cue("ft.bin"))
	if why, err := s.MatchDownload(ctx, w.ID, dl, []string{"ft.bin", "ft.cue"}); why != "" || err != nil {
		t.Fatalf("match: %q %v", why, err)
	}
	wrong := t.TempDir()
	touch(t, filepath.Join(wrong, "x.bin"), psxImage("SLUS_009.99"))
	if why, _ := s.MatchDownload(ctx, w.ID, wrong, []string{"x.bin"}); !strings.Contains(why, "Named Right") {
		t.Errorf("wrong game: %q", why)
	}
	to, err := s.ImportDownload(ctx, w.ID, dl, []string{"ft.bin", "ft.cue"})
	if err != nil {
		t.Fatal(err)
	}
	if to != filepath.Join(root, "psx", "Another Game (Europe)", "ft.cue") {
		t.Errorf("went to %s", to)
	}
	if _, err := os.Stat(filepath.Join(root, "psx", "Another Game (Europe)", "ft.bin")); err != nil {
		t.Error("its track didn't come along")
	}

	// A PC game's download is the game whole.
	w, err = s.Want(ctx, "pc", "Half-Life 2")
	if err != nil {
		t.Fatal(err)
	}
	pcdl := t.TempDir()
	touch(t, filepath.Join(pcdl, "Half-Life.2-GOG", "hl2.exe"), make([]byte, 10))
	if why, err := s.MatchDownload(ctx, w.ID, pcdl, nil); why != "" || err != nil {
		t.Fatalf("pc match: %q %v", why, err)
	}
	if to, err := s.ImportDownload(ctx, w.ID, pcdl, nil); err != nil || to != filepath.Join(root, "pc", "Half-Life 2") {
		t.Fatalf("pc import %s %v", to, err)
	}
	if _, err := os.Stat(filepath.Join(root, "pc", "Half-Life 2", "Half-Life.2-GOG", "hl2.exe")); err != nil {
		t.Error("the game's files didn't come along")
	}

	// Imported by hand: a folder that's one game goes in whole.
	imp := t.TempDir()
	touch(t, filepath.Join(imp, "NPUB30024", "PS3_GAME", "PARAM.SFO"), sfo("TITLE_ID", "NPUB30024", "TITLE", "flOw"))
	placed, skipped, err := s.ImportGames(ctx, "", filepath.Join(imp, "NPUB30024"))
	if err != nil || len(skipped) != 0 || len(placed) != 1 || placed[0].To != filepath.Join(root, "PS3 Games", "NPUB30024") {
		t.Fatalf("import %+v %v %v", placed, skipped, err)
	}
	wait(t, s)
}

func TestTitleFromName(t *testing.T) {
	for in, want := range map[string]string{
		"Bloodborne.PS4-DUPLEX":                       "Bloodborne",
		"Final Fantasy VII (USA) (Disc 1)":            "Final Fantasy VII",
		"Celeste [01002B30028F6000][v0]":              "Celeste",
		"The.Witcher.3.Wild.Hunt.GOTY-GOG":            "The Witcher 3 Wild Hunt GOTY",
		"Portal 2":                                    "Portal 2",
		"Super_Mario_Odyssey_NSW-VENOM":               "Super Mario Odyssey",
		"Halo 3 (USA, Europe) (En,Fr,De,Es,It,Ja,Ko)": "Halo 3",
	} {
		if got := TitleFromName(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	if !nintendoArt("https://img-eshop.cdn.nintendo.net/i/x.jpg") || nintendoArt("https://example.com/x.jpg") || nintendoArt("http://img-eshop.cdn.nintendo.net/x.jpg") {
		t.Error("nintendoArt")
	}
}
