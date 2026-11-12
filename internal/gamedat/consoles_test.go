package gamedat

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const noIntro = `clrmamepro (
	name "Nintendo - Super Nintendo Entertainment System"
	version "2026.10.07"
)

game (
	name "Super Metroid (Japan, USA) (En,Ja)"
	region "USA"
	rom ( name "Super Metroid (Japan, USA) (En,Ja).sfc" size 3145728 crc D63ED5F8 md5 21F3E98DF4780EE1C667B84E57D88675 sha1 DA957F0D63D14CB441D215462904C4FA8519C613 )
)

game (
	name "\"Quoted\" Game (World)"
	rom ( name "\"Quoted\" Game (World).sfc" size 4 crc 0000ABCD )
)
`

const genres = `clrmamepro (
	name "Nintendo - Super Nintendo Entertainment System"
)

game (
	comment "Super Metroid (Japan, USA) (En,Ja)"
	genre "Adventure"
	rom ( crc D63ED5F8 )
)
`

func TestParseClrMame(t *testing.T) {
	dat, err := ParseClrMame(strings.NewReader(noIntro))
	if err != nil {
		t.Fatal(err)
	}
	if dat.Header["version"] != "2026.10.07" || len(dat.Games) != 2 {
		t.Fatalf("%+v", dat)
	}
	g := dat.Games[0]
	if g.Name() != "Super Metroid (Japan, USA) (En,Ja)" || g.Fields["region"] != "USA" || len(g.Roms) != 1 ||
		g.Roms[0].CRC != 0xD63ED5F8 || g.Roms[0].Size != 3145728 || g.SHA1s[g.Roms[0].Name] != "da957f0d63d14cb441d215462904c4fa8519c613" {
		t.Errorf("%+v", g)
	}
	if dat.Games[1].Name() != `"Quoted" Game (World)` {
		t.Errorf("quoted name %q", dat.Games[1].Name())
	}
	ix := NewIndex(dat)
	if e, crc, ok := ix.ByCRC([]uint32{0xD63ED5F8}, 3145728); !ok || e.Name() != g.Name() || crc != 0xD63ED5F8 {
		t.Errorf("by CRC: %v %v", e, ok)
	}
	if _, _, ok := ix.ByCRC([]uint32{0xD63ED5F8}, 12); ok {
		t.Error("matched a file of another size")
	}
	// A second CRC is the file without its header, so it's smaller.
	if _, _, ok := ix.ByCRC([]uint32{1, 0xD63ED5F8}, 3145728+512); !ok {
		t.Error("didn't match without the header")
	}
	if e, ok := ix.ByName("super metroid (japan, usa) (en,ja)"); !ok || e.Name() != g.Name() {
		t.Error("by name")
	}

	meta, err := ParseClrMame(strings.NewReader(genres))
	if err != nil {
		t.Fatal(err)
	}
	fi := FactIndex{}
	fi.Add(meta)
	if f := fi[0xD63ED5F8]; f == nil || f.Genre != "Adventure" {
		t.Errorf("facts %+v", f)
	}
	if _, err := ParseClrMame(strings.NewReader("game ( name ")); err == nil {
		t.Error("read a DAT cut short")
	}
}

func TestTitleAndTags(t *testing.T) {
	name := "Legend of Zelda, The - A Link to the Past (USA) (Rev 1) [b]"
	if got := Title(name); got != "Legend of Zelda, The - A Link to the Past" {
		t.Errorf("title %q", got)
	}
	if got := Tags(name); !slices.Equal(got, []string{"USA", "Rev 1", "b"}) {
		t.Errorf("tags %q", got)
	}
	if got := ThumbName("Circus / Acrobat TV: \"Q*bert\"?"); got != "Circus _ Acrobat TV_ _Q_bert__" {
		t.Errorf("thumb %q", got)
	}
}

func TestCRCsWithoutHeaders(t *testing.T) {
	rom := bytes.Repeat([]byte("game"), 1024)
	want := crc32.ChecksumIEEE(rom)
	has := func(h Header, b []byte) bool {
		crcs, err := CRCs(bytes.NewReader(b), int64(len(b)), h)
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(crcs, want)
	}
	if !has(INES, append([]byte("NES\x1a"+strings.Repeat("\x00", 12)), rom...)) {
		t.Error("iNES header")
	}
	if !has(Copier, append(make([]byte, 512), rom...)) {
		t.Error("copier header")
	}
	n64 := append([]byte{0x80, 0x37, 0x12, 0x40}, rom[4:]...)
	want = crc32.ChecksumIEEE(n64)
	v64 := make([]byte, len(n64))
	le := make([]byte, len(n64))
	for i := 0; i < len(n64); i += 4 {
		v64[i], v64[i+1], v64[i+2], v64[i+3] = n64[i+1], n64[i], n64[i+3], n64[i+2]
		le[i], le[i+1], le[i+2], le[i+3] = n64[i+3], n64[i+2], n64[i+1], n64[i]
	}
	if !has(N64Order, v64) || !has(N64Order, le) || !has(N64Order, n64) {
		t.Error("N64 byte orders")
	}
	if has(NoHeader, append(make([]byte, 512), n64...)) {
		t.Error("took a header off a system without one")
	}
}

func TestListArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pacman.zip")
	f, _ := os.Create(p)
	z := zip.NewWriter(f)
	w, _ := z.Create("pacman.6e")
	w.Write([]byte("pac"))
	z.Create("folder/")
	z.Close()
	f.Close()
	files, err := ListArchive(p)
	if err != nil || len(files) != 1 || files[0].CRC != crc32.ChecksumIEEE([]byte("pac")) || files[0].Size != 3 {
		t.Fatalf("%+v %v", files, err)
	}
	rc, err := OpenInArchive(p, "pacman.6e")
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestSystems(t *testing.T) {
	for name, want := range map[string]string{
		"snes": "snes", "SNES": "snes", "Super Nintendo": "snes", "Nintendo - Super Nintendo Entertainment System": "snes",
		"mame": "mame", "Arcade": "mame", "megadrive": "genesis", "Game Boy Advance": "gba",
	} {
		if s, ok := SystemByFolder(name); !ok || s.Key != want {
			t.Errorf("%q is %q, not %q", name, s.Key, want)
		}
	}
	if _, ok := SystemByFolder("roms"); ok {
		t.Error("roms is a system")
	}
	if s, ok := SystemByExt("Game.SFC"); !ok || s.Key != "snes" {
		t.Error("by extension")
	}
	if !IsGameFile("pacman.zip") || IsGameFile("readme.txt") {
		t.Error("game files")
	}
}
