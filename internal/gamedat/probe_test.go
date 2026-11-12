package gamedat

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

// makeSFO builds a PARAM.SFO with text values.
func makeSFO(vals map[string]string) []byte {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var kt, dt bytes.Buffer
	var idx []byte
	for _, k := range keys {
		e := make([]byte, 16)
		binary.LittleEndian.PutUint16(e, uint16(kt.Len()))
		binary.LittleEndian.PutUint16(e[2:], 0x0204)
		v := append([]byte(vals[k]), 0)
		binary.LittleEndian.PutUint32(e[4:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[8:], uint32(len(v)))
		binary.LittleEndian.PutUint32(e[12:], uint32(dt.Len()))
		kt.WriteString(k + "\x00")
		dt.Write(v)
		idx = append(idx, e...)
	}
	h := make([]byte, 20)
	copy(h, "\x00PSF")
	binary.LittleEndian.PutUint32(h[4:], 0x101)
	binary.LittleEndian.PutUint32(h[8:], uint32(20+len(idx)))
	binary.LittleEndian.PutUint32(h[12:], uint32(20+len(idx)+kt.Len()))
	binary.LittleEndian.PutUint32(h[16:], uint32(len(keys)))
	return slices.Concat(h, idx, kt.Bytes(), dt.Bytes())
}

// makeISO builds a small ISO 9660 image holding files ("A/B.TXT" makes
// folder A), as 2048-byte sectors, or raw 2352-byte ones.
func makeISO(system string, files map[string][]byte, raw bool) []byte {
	type node struct {
		name  string
		dir   bool
		data  []byte
		kids  []*node
		lba   int
		size  int
		isDir bool
	}
	root := &node{dir: true}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		parts := strings.Split(n, "/")
		cur := root
		for i, p := range parts {
			var next *node
			for _, k := range cur.kids {
				if k.name == p {
					next = k
				}
			}
			if next == nil {
				next = &node{name: p, dir: i < len(parts)-1}
				if !next.dir {
					next.data = files[n]
				}
				cur.kids = append(cur.kids, next)
			}
			cur = next
		}
	}
	lba := 18
	var place func(n *node)
	place = func(n *node) {
		n.lba = lba
		if n.dir {
			n.size = 2048
			lba++
			for _, k := range n.kids {
				place(k)
			}
		} else {
			n.size = len(n.data)
			lba += (len(n.data) + 2047) / 2048
		}
	}
	place(root)
	img := make([]byte, lba*2048)
	record := func(n *node, name string) []byte {
		r := make([]byte, 33+len(name))
		if len(r)%2 == 1 {
			r = append(r, 0)
		}
		r[0] = byte(len(r))
		binary.LittleEndian.PutUint32(r[2:], uint32(n.lba))
		binary.LittleEndian.PutUint32(r[10:], uint32(n.size))
		if n.dir {
			r[25] = 2
		}
		r[32] = byte(len(name))
		copy(r[33:], name)
		return r
	}
	var write func(n *node)
	write = func(n *node) {
		if !n.dir {
			copy(img[n.lba*2048:], n.data)
			return
		}
		var b []byte
		b = append(b, record(n, "\x00")...)
		b = append(b, record(n, "\x01")...)
		for _, k := range n.kids {
			name := k.name
			if !k.dir {
				name += ";1"
			}
			b = append(b, record(k, name)...)
			write(k)
		}
		copy(img[n.lba*2048:], b)
	}
	write(root)
	pvd := img[16*2048:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	copy(pvd[8:40], strings.Repeat(" ", 32))
	copy(pvd[8:], system)
	copy(pvd[156:], record(root, "\x00"))
	if !raw {
		return img
	}
	out := make([]byte, 0, lba*2352)
	for i := 0; i < lba; i++ {
		s := make([]byte, 2352)
		copy(s, syncPattern)
		s[15] = 2
		copy(s[24:], img[i*2048:(i+1)*2048])
		out = append(out, s...)
	}
	return out
}

func write(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func probe(t *testing.T, p string) Ident {
	t.Helper()
	id, err := Probe(p)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(p), err)
	}
	return id
}

func TestProbeDiscs(t *testing.T) {
	dir := t.TempDir()

	// PlayStation: a raw .bin with its cue sheet.
	bin := write(t, dir, "Game (USA) (Track 1).bin", makeISO("PLAYSTATION", map[string][]byte{"SYSTEM.CNF": []byte("BOOT = cdrom:\\SLUS_000.67;1\r\nTCB = 4\r\n")}, true))
	write(t, dir, "Game (USA) (Track 2).bin", make([]byte, 2352*4))
	cue := write(t, dir, "Game (USA).cue", []byte("FILE \"Game (USA) (Track 1).bin\" BINARY\n  TRACK 01 MODE2/2352\n    INDEX 01 00:00:00\nFILE \"Game (USA) (Track 2).bin\" BINARY\n  TRACK 02 AUDIO\n    INDEX 01 00:00:00\n"))
	id := probe(t, cue)
	if id.System != "psx" || id.Serial != "SLUS-00067" || len(id.Parts) != 2 || id.Parts[0] != bin {
		t.Errorf("cue: %+v", id)
	}
	if id := probe(t, bin); id.System != "psx" || id.Serial != "SLUS-00067" {
		t.Errorf("bin: %+v", id)
	}

	// PlayStation 2 and PSP ISOs.
	if id := probe(t, write(t, dir, "ps2.iso", makeISO("PLAYSTATION", map[string][]byte{"SYSTEM.CNF": []byte("BOOT2 = cdrom0:\\SLES_523.41;1\nVER = 1.00\n")}, false))); id.System != "ps2" || id.Serial != "SLES-52341" {
		t.Errorf("ps2: %+v", id)
	}
	psp := write(t, dir, "psp.iso", makeISO("PSP GAME", map[string][]byte{
		"PSP_GAME/PARAM.SFO": makeSFO(map[string]string{"DISC_ID": "ULUS10041", "TITLE": "Lumines", "CATEGORY": "UG"}),
		"PSP_GAME/ICON0.PNG": []byte("\x89PNG icon"),
		"UMD_DATA.BIN":       []byte("ULUS-10041|0001|"),
	}, false))
	if id := probe(t, psp); id.System != "psp" || id.Serial != "ULUS10041" || id.Title != "Lumines" {
		t.Errorf("psp: %+v", id)
	}
	if b, err := Icon(psp); err != nil || string(b) != "\x89PNG icon" {
		t.Errorf("psp icon: %q %v", b, err)
	}
	if id := probe(t, write(t, dir, "ps3.iso", makeISO("PS3VOLUME", map[string][]byte{"PS3_GAME/PARAM.SFO": makeSFO(map[string]string{"TITLE_ID": "BLUS30001", "TITLE": "Resistance"})}, false))); id.System != "ps3" || id.Serial != "BLUS30001" {
		t.Errorf("ps3: %+v", id)
	}

	// Sega's discs start with a header.
	sat := make([]byte, 2048*20)
	copy(sat, "SEGA SEGASATURN SEGA ENTERPRISES")
	copy(sat[0x20:], "MK-81009  ")
	copy(sat[0x60:], "NIGHTS INTO DREAMS")
	if id := probe(t, write(t, dir, "saturn.iso", sat)); id.System != "saturn" || id.Serial != "MK-81009" || id.Title != "NIGHTS INTO DREAMS" {
		t.Errorf("saturn: %+v", id)
	}
	scd := make([]byte, 2048*20)
	copy(scd, "SEGADISCSYSTEM  ")
	copy(scd[0x150:], "SONIC THE HEDGEHOG CD")
	copy(scd[0x180:], "GM T-93175 -00")
	if id := probe(t, write(t, dir, "segacd.iso", scd)); id.System != "segacd" || id.Serial != "T-93175" || id.Title != "Sonic The Hedgehog Cd" {
		t.Errorf("segacd: %+v", id)
	}
	// A Dreamcast's IP.BIN is on track 3 of its .gdi.
	ip := make([]byte, 2352*20)
	copy(ip, syncPattern)
	ip[15] = 1
	copy(ip[16:], "SEGA SEGAKATANA SEGA ENTERPRISES")
	copy(ip[16+0x40:], "MK-51000  ")
	write(t, dir, "dc/track01.bin", makeISO("", nil, true))
	write(t, dir, "dc/track02.raw", make([]byte, 2352))
	write(t, dir, "dc/track03.bin", ip)
	gdi := write(t, dir, "dc/Crazy Taxi.gdi", []byte("3\n1 0 4 2352 track01.bin 0\n2 450 0 2352 track02.raw 0\n3 45000 4 2352 track03.bin 0\n"))
	if id := probe(t, gdi); id.System != "dreamcast" || id.Serial != "MK-51000" || len(id.Parts) != 3 {
		t.Errorf("gdi: %+v", id)
	}

	// GameCube and Wii: plain, and in Dolphin's and WBFS's wrappers.
	gc := make([]byte, 0x440)
	copy(gc, "GW7E69")
	binary.BigEndian.PutUint32(gc[0x1C:], 0xC2339F3D)
	copy(gc[0x20:], "007: Agent Under Fire")
	if id := probe(t, write(t, dir, "gc.iso", gc)); id.System != "gamecube" || id.Serial != "GW7E" || id.Title != "007: Agent Under Fire" {
		t.Errorf("gamecube: %+v", id)
	}
	wii := make([]byte, 0x80)
	copy(wii, "RSBE01")
	binary.BigEndian.PutUint32(wii[0x18:], 0x5D1C9EA3)
	if id := probe(t, write(t, dir, "wii.wbfs", slices.Concat([]byte("WBFS"), make([]byte, 0x1FC), wii))); id.System != "wii" || id.Serial != "RSBE" {
		t.Errorf("wbfs: %+v", id)
	}
	if id := probe(t, write(t, dir, "wii.rvz", slices.Concat([]byte("RVZ\x01"), make([]byte, 0x54), wii, make([]byte, 0x400)))); id.System != "wii" || id.Serial != "RSBE" {
		t.Errorf("rvz: %+v", id)
	}

	// An Xbox disc: its file system 64 KB in, with default.xbe.
	xbox := make([]byte, 0x30000)
	copy(xbox[0x10000:], "MICROSOFT*XBOX*MEDIA")
	binary.LittleEndian.PutUint32(xbox[0x10000+20:], 0x30) // root at sector 0x30
	binary.LittleEndian.PutUint32(xbox[0x10000+24:], 2048)
	ent := xbox[0x30*2048:]
	binary.LittleEndian.PutUint32(ent[4:], 0x40) // file at sector 0x40
	binary.LittleEndian.PutUint32(ent[8:], 0x1000)
	ent[13] = byte(len("default.xbe"))
	copy(ent[14:], "default.xbe")
	xbe := make([]byte, 0x1000)
	copy(xbe, "XBEH")
	binary.LittleEndian.PutUint32(xbe[0x104:], 0x10000)
	binary.LittleEndian.PutUint32(xbe[0x118:], 0x10000+0x200)
	binary.LittleEndian.PutUint32(xbe[0x200+8:], 0x4D530004)
	for i, r := range utf16.Encode([]rune("Halo")) {
		binary.LittleEndian.PutUint16(xbe[0x200+0x0C+i*2:], r)
	}
	xbox = append(xbox[:0x40*2048], xbe...)
	if id := probe(t, write(t, dir, "halo.iso", xbox)); id.System != "xbox" || id.Title != "Halo" || id.Serial != "4D530004" {
		t.Errorf("xbox: %+v", id)
	}
}

func TestProbeDownloads(t *testing.T) {
	dir := t.TempDir()

	// Switch: a title ID in the name, or a ticket inside.
	if id := probe(t, write(t, dir, "Celeste [01002B30028F6000][v0].xci", make([]byte, 0x200))); id.System != "switch" || id.Serial != "01002B30028F6000" || id.Update {
		t.Errorf("xci: %+v", id)
	}
	if id := probe(t, write(t, dir, "Celeste [01002B30028F6800][v65536].nsp", []byte("PFS0\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))); !id.Update || SwitchBase(id.Serial) != "01002B30028F6000" {
		t.Errorf("update: %+v", id)
	}
	if SwitchBase("01002B30028F7001") != "01002B30028F6000" {
		t.Errorf("dlc base %s", SwitchBase("01002B30028F7001"))
	}
	names := "01007EF00011E0000000000000000004.tik\x00abc.nca\x00"
	pfs := make([]byte, 0x10+2*0x18)
	copy(pfs, "PFS0")
	binary.LittleEndian.PutUint32(pfs[4:], 2)
	binary.LittleEndian.PutUint32(pfs[8:], uint32(len(names)))
	binary.LittleEndian.PutUint32(pfs[0x10+16:], 0)
	binary.LittleEndian.PutUint32(pfs[0x10+0x18+16:], 37)
	if id := probe(t, write(t, dir, "game.nsp", append(pfs, names...))); id.System != "switch" || id.Serial != "01007EF00011E000" {
		t.Errorf("nsp: %+v", id)
	}

	// 3DS: a cartridge dump's product code.
	ds := make([]byte, 0x4000+0x200)
	copy(ds[0x100:], "NCSD")
	binary.LittleEndian.PutUint32(ds[0x120:], 0x4000/0x200)
	copy(ds[0x4000+0x100:], "NCCH")
	copy(ds[0x4000+0x150:], "CTR-P-AREE")
	if id := probe(t, write(t, dir, "mario.3ds", ds)); id.System != "3ds" || id.Serial != "CTR-P-AREE" {
		t.Errorf("3ds: %+v", id)
	}

	// PSP's EBOOT.PBP, and a PlayStation game made into one.
	sfo := makeSFO(map[string]string{"DISC_ID": "SCUS94163", "TITLE": "Final Fantasy VII", "CATEGORY": "ME"})
	pbp := make([]byte, 0x28)
	copy(pbp, "\x00PBP")
	binary.LittleEndian.PutUint32(pbp[0x08:], 0x28)
	binary.LittleEndian.PutUint32(pbp[0x0C:], uint32(0x28+len(sfo)))
	binary.LittleEndian.PutUint32(pbp[0x10:], uint32(0x28+len(sfo)+4))
	p := write(t, dir, "ff7.pbp", slices.Concat(pbp, sfo, []byte("ICON")))
	if id := probe(t, p); id.System != "psx" || id.Serial != "SCUS94163" || id.Title != "Final Fantasy VII" {
		t.Errorf("pbp: %+v", id)
	}
	if b, _ := Icon(p); string(b) != "ICON" {
		t.Errorf("pbp icon %q", b)
	}

	// PS4: the package's PARAM.SFO.
	sfo = makeSFO(map[string]string{"TITLE_ID": "CUSA00552", "TITLE": "The Last of Us Remastered", "CATEGORY": "gd"})
	pkg := make([]byte, 0x1000)
	copy(pkg, "\x7fCNT")
	binary.BigEndian.PutUint32(pkg[0x10:], 1)
	binary.BigEndian.PutUint32(pkg[0x18:], 0x800)
	copy(pkg[0x40:], "UP9000-CUSA00552_00-THELASTOFUS00000")
	binary.BigEndian.PutUint32(pkg[0x800:], 0x1000)
	binary.BigEndian.PutUint32(pkg[0x800+16:], 0x1000)
	binary.BigEndian.PutUint32(pkg[0x800+20:], uint32(len(sfo)))
	if id := probe(t, write(t, dir, "tlou.pkg", append(pkg, sfo...))); id.System != "ps4" || id.Serial != "CUSA00552" || id.Title != "The Last of Us Remastered" {
		t.Errorf("ps4 pkg: %+v", id)
	}

	// Vita: a .vpk is a zip with the game's PARAM.SFO.
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("sce_sys/param.sfo")
	w.Write(makeSFO(map[string]string{"TITLE_ID": "PCSE00844", "TITLE": "Aegis of Earth"}))
	zw.Close()
	if id := probe(t, write(t, dir, "aegis.vpk", zb.Bytes())); id.System != "vita" || id.Serial != "PCSE00844" {
		t.Errorf("vpk: %+v", id)
	}

	// CHDs say what kind of disc they hold.
	chd := make([]byte, 0x200)
	copy(chd, "MComprHD")
	binary.BigEndian.PutUint32(chd[12:], 5)
	binary.BigEndian.PutUint64(chd[0x30:], 0x100)
	copy(chd[0x100:], "CHT2")
	if id := probe(t, write(t, dir, "game.chd", chd)); id.Disc != "cd" {
		t.Errorf("chd: %+v", id)
	}
	copy(chd[0x100:], "GDDD")
	if id := probe(t, write(t, dir, "kinst.chd", chd)); id.Disc != "hd" {
		t.Errorf("hd chd: %+v", id)
	}
}

func TestProbeFolders(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "BLUS30001/PS3_GAME/PARAM.SFO", makeSFO(map[string]string{"TITLE_ID": "BLUS30001", "TITLE": "Resistance: Fall of Man"}))
	write(t, dir, "BLUS30001/PS3_GAME/ICON0.PNG", []byte("png"))
	if id := probe(t, filepath.Join(dir, "BLUS30001")); id.System != "ps3" || id.Title != "Resistance: Fall of Man" {
		t.Errorf("ps3 folder: %+v", id)
	}
	if b, _ := Icon(filepath.Join(dir, "BLUS30001")); string(b) != "png" {
		t.Errorf("icon %q", b)
	}
	write(t, dir, "PCSE00844/sce_sys/param.sfo", makeSFO(map[string]string{"TITLE_ID": "PCSE00844", "TITLE": "Aegis"}))
	if id := probe(t, filepath.Join(dir, "PCSE00844")); id.System != "vita" {
		t.Errorf("vita folder: %+v", id)
	}
	write(t, dir, "Bloodborne/sce_sys/param.sfo", makeSFO(map[string]string{"TITLE_ID": "CUSA00900", "TITLE": "Bloodborne"}))
	if id := probe(t, filepath.Join(dir, "Bloodborne")); id.System != "ps4" || id.Serial != "CUSA00900" {
		t.Errorf("ps4 folder: %+v", id)
	}
	write(t, dir, "Astro/sce_sys/param.json", []byte(`{"titleId":"PPSA01325","localizedParameters":{"defaultLanguage":"en-US","en-US":{"titleName":"Astro's Playroom"}}}`))
	if id := probe(t, filepath.Join(dir, "Astro")); id.System != "ps5" || id.Title != "Astro's Playroom" {
		t.Errorf("ps5 folder: %+v", id)
	}
	write(t, dir, "Mario Kart 8/meta/meta.xml", []byte(`<menu><product_code type="string" length="32">WUP-P-AMKE</product_code><longname_en type="string" length="512">Mario Kart 8</longname_en></menu>`))
	if id := probe(t, filepath.Join(dir, "Mario Kart 8")); id.System != "wiiu" || id.Serial != "WUP-P-AMKE" || id.Title != "Mario Kart 8" {
		t.Errorf("wiiu folder: %+v", id)
	}
	write(t, dir, "4D5307E6/00007000/ABCDEF", []byte("x"))
	if id := probe(t, filepath.Join(dir, "4D5307E6")); id.System != "xbox360" || id.Serial != "4D5307E6" {
		t.Errorf("360 folder: %+v", id)
	}
	os.MkdirAll(filepath.Join(dir, "Doom"), 0o755)
	if _, err := Probe(filepath.Join(dir, "Doom")); err != ErrUnknown {
		t.Errorf("plain folder: %v", err)
	}
}

func TestSerialKeys(t *testing.T) {
	for in, want := range map[string]string{
		"SLUS-00067":             "SLUS00067",
		"DL-DOL-GW7E-USA":        "GW7E",
		"RVL-RSBE-USA-B0":        "RSBE",
		"SLPM-65002-0":           "SLPM650020 SLPM65002",
		"SLUS-00892, SLUS-00908": "SLUS00892 SLUS00908",
		"CTR-P-BP4J":             "CTRPBP4J",
	} {
		if got := strings.Join(SerialKeys(in), " "); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
	dat, _ := ParseClrMame(strings.NewReader(`game ( name "Game (USA)" serial "SLUS-00067" rom ( name "Game (USA).bin" size 10 crc 12345678 ) )
game ( name "Cube (USA)" serial "DL-DOL-GW7E-USA" rom ( name "Cube (USA).iso" size 10 crc 22345678 ) )`))
	ix := NewIndex(dat)
	if es := ix.BySerial("SLUS_000.67"); len(es) != 1 || es[0].Name() != "Game (USA)" {
		t.Errorf("by serial: %v", es)
	}
	if es := ix.BySerial("GW7E"); len(es) != 1 {
		t.Errorf("by code: %v", es)
	}
}

// A Windows game's discs: the ones that start by themselves or set the
// game up, alone or a folder of them, are a PC game's.
func TestProbePCDiscs(t *testing.T) {
	dir := t.TempDir()
	disc1 := makeISO("WIN32", map[string][]byte{"AUTORUN.INF": []byte("[autorun]\r\nopen=SETUP.EXE\r\nlabel=The Mystery Game Disc 1\r\n"), "SETUP.EXE": []byte("MZ")}, false)
	if id := probe(t, write(t, dir, "one.iso", disc1)); id.System != "pc" || id.Title != "The Mystery Game" {
		t.Errorf("autorun disc %+v", id)
	}
	if id := probe(t, write(t, dir, "setup.bin", makeISO("", map[string][]byte{"INSTALL.EXE": []byte("MZ")}, true))); id.System != "pc" {
		t.Errorf("raw disc with a setup program %+v", id)
	}
	if _, err := Probe(write(t, dir, "data.iso", makeISO("", map[string][]byte{"README.TXT": []byte("hi")}, false))); err == nil {
		t.Error("a disc of nothing said it was a game")
	}

	game := filepath.Join(t.TempDir(), "Mystery Game")
	os.MkdirAll(game, 0o755)
	write(t, game, "Disc 1.iso", disc1)
	for i := 2; i <= 7; i++ {
		write(t, game, "Disc "+string(rune('0'+i))+".iso", makeISO("", map[string][]byte{"DATA.CAB": []byte("x")}, false))
	}
	if id := probe(t, game); id.System != "pc" || id.Title != "" {
		t.Errorf("folder of discs %+v", id)
	}
	if got := autorunLabel([]byte("label=\"Space Quest (CD 2 of 3)\"")); got != "Space Quest" {
		t.Errorf("label %q", got)
	}
}
