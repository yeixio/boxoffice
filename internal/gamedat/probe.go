package gamedat

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Ident is what a game's own files say about it: which system it's for,
// its serial or title ID, and the name written in it. Discs, downloads and
// dumped folders are too big to hash, so this is how they're known.
type Ident struct {
	// System is the system's key, when the game says which it's for.
	System string
	// Serial is its serial or title ID, as written: SLUS-00067, BLUS30001,
	// CTR-P-BP4J, GW7E (a GameCube or Wii game's ID), 0100ABCD12340000 (a
	// Switch title ID).
	Serial string
	// Title is the name written in it, where there is one.
	Title string
	// Disc is what kind of disc a CHD holds: cd, gd, dvd, or hd (a hard
	// disk, which is MAME's).
	Disc string
	// Parts are the other files that are part of the game: a cue sheet's
	// tracks.
	Parts []string
	// Update and DLC say a Switch or PlayStation download is an update or
	// added content, not the game.
	Update, DLC bool
}

// ErrUnknown means a file says nothing Box Office can read about the game.
var ErrUnknown = errors.New("nothing in it says what game it is")

// Probe reads what a game file or folder says about itself. It reads only
// a few small parts of the file, however big.
func Probe(p string) (Ident, error) {
	st, err := os.Stat(p)
	if err != nil {
		return Ident{}, err
	}
	if st.IsDir() {
		return probeFolder(p)
	}
	id, err := probeFile(p)
	// A Switch title ID in the name, as most downloads have.
	if id.Serial == "" {
		if tid, ok := SwitchTitleID(filepath.Base(p)); ok && (id.System == "" || id.System == "switch") {
			id.System, id.Serial, err = "switch", tid, nil
			id.Update, id.DLC = switchKind(tid)
		}
	}
	return id, err
}

func probeFile(p string) (Ident, error) {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".cue":
		return probeTracks(p, readCue)
	case ".gdi":
		id, err := probeTracks(p, readGDI)
		if id.System == "" && err == nil {
			id.System = "dreamcast"
		}
		return id, err
	case ".vpk":
		return probeVPK(p)
	case ".m3u":
		discs := readM3U(p)
		if len(discs) == 0 {
			return Ident{}, ErrUnknown
		}
		id, err := Probe(discs[0])
		id.Parts = append(discs, id.Parts...)
		return id, err
	}
	f, err := os.Open(p)
	if err != nil {
		return Ident{}, err
	}
	defer f.Close()
	head := make([]byte, 0x400)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if n < 16 {
		return Ident{}, ErrUnknown
	}
	switch {
	case string(head[:8]) == "MComprHD":
		return probeCHD(f, head)
	case string(head[:4]) == "\x00PBP":
		return probePBP(f, head)
	case string(head[:4]) == "\x7fCNT":
		return probePS4PKG(f, head)
	case string(head[:4]) == "\x7fPKG":
		// A PlayStation 3 (or PSP, Vita) download: its content ID names it,
		// UP0001-NPUB30024_00-…
		cid := cString(head[0x30:0x54])
		if _, serial, ok := strings.Cut(cid, "-"); ok && len(serial) >= 9 {
			return Ident{System: "ps3", Serial: serial[:9]}, nil
		}
		return Ident{System: "ps3"}, nil
	case string(head[:4]) == "PFS0":
		return probeNSP(f, head)
	case n >= 0x104 && string(head[0x100:0x104]) == "HEAD":
		return Ident{System: "switch"}, nil // a cartridge dump (.xci): its contents are locked
	case string(head[:4]) == "WBFS":
		return probeNintendoAt(f, 0x200)
	case string(head[:4]) == "RVZ\x01" || string(head[:4]) == "WIA\x01":
		return probeNintendoAt(f, 0x58)
	case string(head[:4]) == "CISO" && ext != ".cso":
		return probeNintendoAt(f, 0x8000)
	case binary.LittleEndian.Uint32(head) == 0xB10BC001: // Dolphin's .gcz
		if binary.LittleEndian.Uint32(head[4:]) == 1 {
			return Ident{System: "wii"}, nil
		}
		return Ident{System: "gamecube"}, nil
	case n >= 0x104 && string(head[0x100:0x104]) == "NCSD":
		return probe3DS(f, head)
	case n >= 0x104 && string(head[0x100:0x104]) == "NCCH":
		return probeNCCH(f, 0)
	case ext == ".cia":
		return probeCIA(f, head)
	case string(head[:4]) == "XEX2":
		return probeXEX(f)
	case string(head[:4]) == "XBEH":
		return probeXBE(f)
	}
	if id, ok := nintendoHeader(head); ok {
		return id, nil
	}
	return probeImage(f)
}

// probeTracks reads a cue sheet's (or .gdi's) data tracks.
func probeTracks(p string, read func(string) ([]cueTrack, error)) (Ident, error) {
	tracks, err := read(p)
	if err != nil {
		return Ident{}, err
	}
	var parts []string
	for _, t := range tracks {
		if !containsString(parts, t.file) {
			parts = append(parts, t.file)
		}
	}
	id := Ident{}
	for _, t := range tracks {
		if !t.data {
			continue
		}
		f, err := os.Open(t.file)
		if err != nil {
			continue
		}
		got, err := probeImage(f)
		f.Close()
		// A Dreamcast's IP.BIN is on its third track, after a short one
		// any PC can read: keep looking until a track says.
		if err == nil && got.System != "" {
			id = got
			break
		}
	}
	id.Parts = parts
	if id.System == "" {
		return id, ErrUnknown
	}
	return id, nil
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// probeImage reads a CD or DVD image's first sectors: the headers Sega's
// and the 3DO's discs start with, and the files on an ISO 9660 disc that
// PlayStation games keep their serial in.
func probeImage(f *os.File) (Ident, error) {
	s := newSectors(f, 0)
	first, err := s.read(0, 1)
	if err != nil {
		return Ident{}, ErrUnknown
	}
	if id, ok := segaHeader(first); ok {
		return id, nil
	}
	if bytes.HasPrefix(first, []byte{0x01, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x01}) {
		return Ident{System: "3do", Title: strings.TrimSpace(cString(first[0x28:0x48]))}, nil
	}
	if bytes.HasPrefix(first, []byte("PC-FX:Hu_CD-ROM")) {
		return Ident{System: "pcfx"}, nil
	}
	if more, err := s.read(1, 1); err == nil && bytes.Contains(more, []byte("PC Engine CD-ROM SYSTEM")) {
		return Ident{System: "pcecd"}, nil
	}
	if id, ok := probeXDVDFS(f); ok {
		return id, nil
	}
	fs, err := openISO(s)
	if err != nil {
		if cdi, err := s.read(16, 1); err == nil && string(cdi[1:6]) == "CD-I " {
			return Ident{System: "cdi"}, nil
		}
		return Ident{}, ErrUnknown
	}
	return probeISO(fs)
}

// probeISO reads what's on an ISO 9660 disc.
func probeISO(fs *isoFS) (Ident, error) {
	// PlayStation and PlayStation 2: SYSTEM.CNF names the program to
	// start, which is named for the serial: BOOT2 = cdrom0:\SLUS_203.12;1.
	if b, err := fs.readFile("SYSTEM.CNF", 4096); err == nil {
		id := Ident{System: "psx"}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.ToUpper(strings.TrimSpace(k)) {
			case "BOOT2":
				id.System = "ps2"
				id.Serial = bootSerial(v)
			case "BOOT":
				id.Serial = bootSerial(v)
			}
		}
		return id, nil
	}
	if b, err := fs.readFile("PSP_GAME/PARAM.SFO", 64<<10); err == nil {
		id := Ident{System: "psp"}
		if sfo, err := ParseSFO(b); err == nil {
			id.Serial, id.Title = sfo["DISC_ID"], sfo["TITLE"]
		}
		if u, err := fs.readFile("UMD_DATA.BIN", 64); err == nil && id.Serial == "" {
			id.Serial, _, _ = strings.Cut(string(u), "|")
		}
		return id, nil
	}
	if b, err := fs.readFile("PS3_GAME/PARAM.SFO", 64<<10); err == nil {
		id := Ident{System: "ps3"}
		if sfo, err := ParseSFO(b); err == nil {
			id.Serial, id.Title = sfo["TITLE_ID"], sfo["TITLE"]
		}
		return id, nil
	}
	if _, _, _, err := fs.find("IPL.TXT"); err == nil {
		return Ident{System: "neocd", Title: fs.volume}, nil
	}
	if fs.system == "PLAYSTATION" {
		return Ident{System: "psx"}, nil
	}
	if fs.system == "CD-RTOS CD-BRIDGE" || strings.HasPrefix(fs.system, "CD-RTOS") {
		return Ident{System: "cdi"}, nil
	}
	return Ident{Title: fs.volume}, ErrUnknown
}

var reBoot = regexp.MustCompile(`([A-Za-z]{4})[_-]?(\d{3})\.?(\d{2})`)

// bootSerial turns cdrom0:\SLUS_203.12;1 into SLUS-20312.
func bootSerial(v string) string {
	m := reBoot.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2] + m[3]
}

// segaHeader reads the header a Saturn, Sega CD or Dreamcast disc starts
// with.
func segaHeader(b []byte) (Ident, bool) {
	text := func(from, to int) string {
		if to > len(b) {
			return ""
		}
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if r < 0x20 || r > 0x7e {
				return ' '
			}
			return r
		}, string(b[from:to])))
	}
	switch {
	case bytes.HasPrefix(b, []byte("SEGA SEGASATURN")):
		return Ident{System: "saturn", Serial: text(0x20, 0x2a), Title: text(0x60, 0xd0)}, true
	case bytes.HasPrefix(b, []byte("SEGA SEGAKATANA")):
		return Ident{System: "dreamcast", Serial: text(0x40, 0x4a), Title: text(0x80, 0x100)}, true
	case bytes.HasPrefix(b, []byte("SEGADISCSYSTEM")) || bytes.HasPrefix(b, []byte("SEGABOOTDISC")):
		serial := text(0x180, 0x18e)
		serial = strings.TrimPrefix(strings.TrimPrefix(serial, "GM"), "AI")
		serial = strings.TrimSpace(serial)
		if i := strings.LastIndex(serial, "-"); i > 2 {
			serial = strings.TrimSpace(serial[:i]) // the -00 revision
		}
		return Ident{System: "segacd", Serial: serial, Title: titleCase(cmpOr(text(0x150, 0x180), text(0x120, 0x150)))}, true
	}
	return Ident{}, false
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// titleCase tidies a name written in capitals: "SONIC THE HEDGEHOG CD".
func titleCase(s string) string {
	if strings.ToUpper(s) != s {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// GameCube and Wii.

// nintendoHeader reads a GameCube or Wii disc's header: its ID (GW7E01,
// the game's four letters and the maker's two) and title.
func nintendoHeader(b []byte) (Ident, bool) {
	if len(b) < 0x60 {
		return Ident{}, false
	}
	sys := ""
	switch {
	case binary.BigEndian.Uint32(b[0x18:]) == 0x5D1C9EA3:
		sys = "wii"
	case binary.BigEndian.Uint32(b[0x1C:]) == 0xC2339F3D:
		sys = "gamecube"
	default:
		return Ident{}, false
	}
	return Ident{System: sys, Serial: string(b[:4]), Title: strings.TrimSpace(cString(b[0x20:min(len(b), 0x80)]))}, true
}

func probeNintendoAt(f *os.File, at int64) (Ident, error) {
	b := make([]byte, 0x80)
	if _, err := f.ReadAt(b, at); err != nil {
		return Ident{}, ErrUnknown
	}
	if id, ok := nintendoHeader(b); ok {
		return id, nil
	}
	return Ident{}, ErrUnknown
}

// Nintendo 3DS.

func probe3DS(f *os.File, head []byte) (Ident, error) {
	// The first partition is the game.
	at := int64(binary.LittleEndian.Uint32(head[0x120:])) * 0x200
	return probeNCCH(f, at)
}

// probeNCCH reads a 3DS program's header, which is never locked: its
// product code is the serial, CTR-P-BP4J.
func probeNCCH(f *os.File, at int64) (Ident, error) {
	b := make([]byte, 0x200)
	if _, err := f.ReadAt(b, at); err != nil || string(b[0x100:0x104]) != "NCCH" {
		return Ident{System: "3ds"}, nil
	}
	return Ident{System: "3ds", Serial: strings.TrimSpace(cString(b[0x150:0x160]))}, nil
}

func probeCIA(f *os.File, head []byte) (Ident, error) {
	align := func(n int64) int64 { return (n + 63) &^ 63 }
	hdr := int64(binary.LittleEndian.Uint32(head))
	if hdr != 0x2020 {
		return Ident{}, ErrUnknown
	}
	cert := int64(binary.LittleEndian.Uint32(head[0x08:]))
	tik := int64(binary.LittleEndian.Uint32(head[0x0C:]))
	tmd := int64(binary.LittleEndian.Uint32(head[0x10:]))
	content := align(hdr) + align(cert) + align(tik) + align(tmd)
	id, _ := probeNCCH(f, content)
	return id, nil
}

// Switch.

var reTitleID = regexp.MustCompile(`(?i)\[?\b(01[0-9a-f]{14})\b\]?`)

// SwitchTitleID finds a Switch title ID in a file's name, as in "Celeste
// [01002B30028F6000][v0].nsp".
func SwitchTitleID(name string) (string, bool) {
	m := reTitleID.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]), true
}

// switchKind says whether a Switch title ID is an update or added
// content: games end in 000, updates in 800, the rest are DLC.
func switchKind(tid string) (update, dlc bool) {
	if len(tid) != 16 {
		return false, false
	}
	switch tail := tid[13:]; {
	case tail == "800":
		return true, false
	case tail != "000":
		return false, true
	}
	return false, false
}

// SwitchBase is the game a Switch update's or DLC's title ID is for.
func SwitchBase(tid string) string {
	var n uint64
	if _, err := fmt.Sscanf(tid, "%016X", &n); err != nil {
		return tid
	}
	update, dlc := switchKind(tid)
	switch {
	case update:
		n &^= 0xFFF
	case dlc:
		n = (n - 0x1000) &^ 0xFFF
	}
	return fmt.Sprintf("%016X", n)
}

// probeNSP reads a Switch download's file list: a ticket is named for the
// title it unlocks, and a .cnmt.xml says the title outright.
func probeNSP(f *os.File, head []byte) (Ident, error) {
	n := int(binary.LittleEndian.Uint32(head[4:]))
	strSize := int(binary.LittleEndian.Uint32(head[8:]))
	if n <= 0 || n > 4096 || strSize > 1<<20 {
		return Ident{System: "switch"}, nil
	}
	table := make([]byte, 0x10+n*0x18+strSize)
	if _, err := f.ReadAt(table, 0); err != nil {
		return Ident{System: "switch"}, nil
	}
	names := table[0x10+n*0x18:]
	dataStart := int64(len(table))
	id := Ident{System: "switch"}
	for i := range n {
		e := table[0x10+i*0x18:]
		off := int64(binary.LittleEndian.Uint64(e))
		size := int64(binary.LittleEndian.Uint64(e[8:]))
		no := int(binary.LittleEndian.Uint32(e[16:]))
		if no >= len(names) {
			continue
		}
		name := cString(names[no:])
		switch {
		case strings.HasSuffix(name, ".cnmt.xml") && size < 64<<10:
			b := make([]byte, size)
			if _, err := f.ReadAt(b, dataStart+off); err == nil {
				if m := regexp.MustCompile(`<Id>0x([0-9a-fA-F]{16})</Id>`).FindSubmatch(b); m != nil {
					id.Serial = strings.ToUpper(string(m[1]))
				}
			}
		case strings.HasSuffix(name, ".tik") && len(name) == 36 && id.Serial == "":
			if tid, ok := SwitchTitleID(name[:16]); ok {
				id.Serial = tid
			}
		}
	}
	if id.Serial != "" {
		id.Update, id.DLC = switchKind(id.Serial)
	}
	return id, nil
}

// PlayStation.

// probePBP reads a PSP program, or a PlayStation game made into one.
func probePBP(f *os.File, head []byte) (Ident, error) {
	sfoAt := int64(binary.LittleEndian.Uint32(head[0x08:]))
	iconAt := int64(binary.LittleEndian.Uint32(head[0x0C:]))
	if iconAt <= sfoAt || iconAt-sfoAt > 64<<10 {
		return Ident{System: "psp"}, nil
	}
	b := make([]byte, iconAt-sfoAt)
	if _, err := f.ReadAt(b, sfoAt); err != nil {
		return Ident{System: "psp"}, nil
	}
	sfo, err := ParseSFO(b)
	if err != nil {
		return Ident{System: "psp"}, nil
	}
	id := Ident{System: "psp", Serial: sfo["DISC_ID"], Title: sfo["TITLE"]}
	if sfo["CATEGORY"] == "ME" {
		id.System = "psx"
	}
	return id, nil
}

// pkgEntry finds a file in a PlayStation 4 download's table by its ID:
// 0x1000 is param.sfo, 0x1200 icon0.png.
func pkgEntry(f *os.File, head []byte, want uint32) (off, size int64, ok bool) {
	count := binary.BigEndian.Uint32(head[0x10:])
	table := int64(binary.BigEndian.Uint32(head[0x18:]))
	if count > 10000 {
		return 0, 0, false
	}
	b := make([]byte, int(count)*32)
	if _, err := f.ReadAt(b, table); err != nil {
		return 0, 0, false
	}
	for i := range int(count) {
		e := b[i*32:]
		if binary.BigEndian.Uint32(e) == want {
			return int64(binary.BigEndian.Uint32(e[16:])), int64(binary.BigEndian.Uint32(e[20:])), true
		}
	}
	return 0, 0, false
}

func probePS4PKG(f *os.File, head []byte) (Ident, error) {
	id := Ident{System: "ps4"}
	cid := cString(head[0x40:0x64])
	if _, serial, ok := strings.Cut(cid, "-"); ok && len(serial) >= 9 {
		id.Serial = serial[:9]
	}
	if off, size, ok := pkgEntry(f, head, 0x1000); ok && size < 64<<10 {
		b := make([]byte, size)
		if _, err := f.ReadAt(b, off); err == nil {
			if sfo, err := ParseSFO(b); err == nil {
				id.Title = sfo["TITLE"]
				id.Serial = cmpOr(sfo["TITLE_ID"], id.Serial)
				switch sfo["CATEGORY"] {
				case "gp":
					id.Update = true
				case "ac":
					id.DLC = true
				}
			}
		}
	}
	return id, nil
}

// probeVPK reads a Vita download: a zip with the game's PARAM.SFO.
func probeVPK(p string) (Ident, error) {
	z, err := zip.OpenReader(p)
	if err != nil {
		return Ident{}, err
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.EqualFold(f.Name, "sce_sys/param.sfo") && f.UncompressedSize64 < 64<<10 {
			rc, err := f.Open()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if sfo, err := ParseSFO(b); err == nil {
				return Ident{System: "vita", Serial: sfo["TITLE_ID"], Title: sfo["TITLE"]}, nil
			}
		}
	}
	return Ident{System: "vita"}, nil
}

// Xbox.

// probeXDVDFS reads an Xbox or Xbox 360 disc: its file system starts at
// 64 KB in, after the video part on discs dumped whole.
func probeXDVDFS(f *os.File) (Ident, bool) {
	for _, base := range []int64{0, 0x18300000, 0xFD90000, 0x2080000} {
		b := make([]byte, 28)
		if _, err := f.ReadAt(b, base+0x10000); err != nil || string(b[:20]) != "MICROSOFT*XBOX*MEDIA" {
			continue
		}
		root := int64(binary.LittleEndian.Uint32(b[20:]))
		size := int64(binary.LittleEndian.Uint32(b[24:]))
		if size > 1<<20 {
			size = 1 << 20
		}
		dir := make([]byte, size)
		if _, err := f.ReadAt(dir, base+root*2048); err != nil {
			return Ident{System: "xbox"}, true
		}
		for _, name := range []string{"default.xbe", "default.xex"} {
			sector, _, ok := xdvdfsFind(dir, name)
			if !ok {
				continue
			}
			r := io.NewSectionReader(f, base+sector*2048, 1<<20)
			if name == "default.xex" {
				id, _ := probeXEX(r)
				return id, true
			}
			id, _ := probeXBE(r)
			return id, true
		}
		return Ident{System: "xbox"}, true
	}
	return Ident{}, false
}

// xdvdfsFind looks through a folder of the Xbox's file system: a tree of
// entries, each with links to the ones before and after it.
func xdvdfsFind(dir []byte, name string) (sector, size int64, ok bool) {
	seen := map[int]bool{}
	var walk func(at int) bool
	walk = func(at int) bool {
		if at < 0 || at+14 > len(dir) || seen[at] {
			return false
		}
		seen[at] = true
		left := int(binary.LittleEndian.Uint16(dir[at:])) * 4
		right := int(binary.LittleEndian.Uint16(dir[at+2:])) * 4
		n := int(dir[at+13])
		if at+14+n > len(dir) {
			return false
		}
		if strings.EqualFold(string(dir[at+14:at+14+n]), name) {
			sector = int64(binary.LittleEndian.Uint32(dir[at+4:]))
			size = int64(binary.LittleEndian.Uint32(dir[at+8:]))
			return true
		}
		if left != 0 && left != 0xFFFF*4 && walk(left) {
			return true
		}
		return right != 0 && right != 0xFFFF*4 && walk(right)
	}
	ok = walk(0)
	return
}

// probeXBE reads an Xbox program's certificate: its title ID and name.
func probeXBE(r io.ReaderAt) (Ident, error) {
	b := make([]byte, 0x200)
	if _, err := r.ReadAt(b, 0); err != nil || string(b[:4]) != "XBEH" {
		return Ident{System: "xbox"}, nil
	}
	base := binary.LittleEndian.Uint32(b[0x104:])
	cert := binary.LittleEndian.Uint32(b[0x118:])
	c := make([]byte, 0x60)
	if cert < base || cert-base > 1<<20 {
		return Ident{System: "xbox"}, nil
	}
	if _, err := r.ReadAt(c, int64(cert-base)); err != nil {
		return Ident{System: "xbox"}, nil
	}
	u := make([]uint16, 0, 40)
	for i := 0; i < 80; i += 2 {
		v := binary.LittleEndian.Uint16(c[0x0C+i:])
		if v == 0 {
			break
		}
		u = append(u, v)
	}
	return Ident{System: "xbox", Serial: fmt.Sprintf("%08X", binary.LittleEndian.Uint32(c[8:])), Title: strings.TrimSpace(string(utf16.Decode(u)))}, nil
}

// probeXEX reads an Xbox 360 program's title ID.
func probeXEX(r io.ReaderAt) (Ident, error) {
	b := make([]byte, 0x400)
	n, _ := r.ReadAt(b, 0)
	b = b[:n]
	if len(b) < 0x18 || string(b[:4]) != "XEX2" {
		return Ident{System: "xbox360"}, nil
	}
	count := int(binary.BigEndian.Uint32(b[0x14:]))
	for i := range min(count, 64) {
		at := 0x18 + i*8
		if at+8 > len(b) {
			break
		}
		if binary.BigEndian.Uint32(b[at:]) == 0x00040006 {
			info := int64(binary.BigEndian.Uint32(b[at+4:]))
			t := make([]byte, 16)
			if _, err := r.ReadAt(t, info); err == nil {
				return Ident{System: "xbox360", Serial: fmt.Sprintf("%08X", binary.BigEndian.Uint32(t[12:]))}, nil
			}
		}
	}
	return Ident{System: "xbox360"}, nil
}

// CHD.

// probeCHD says what a CHD holds, from the notes in its header: a CD,
// GD-ROM or DVD (a console's disc) or a hard disk (MAME's). Its sectors
// are packed, so the game itself is known by its name.
func probeCHD(f *os.File, head []byte) (Ident, error) {
	version := binary.BigEndian.Uint32(head[12:])
	var meta int64
	switch {
	case version >= 5:
		meta = int64(binary.BigEndian.Uint64(head[0x30:]))
	case version >= 3:
		meta = int64(binary.BigEndian.Uint64(head[0x24:]))
	default:
		return Ident{Disc: "hd"}, nil
	}
	for range 32 {
		if meta == 0 {
			break
		}
		e := make([]byte, 16)
		if _, err := f.ReadAt(e, meta); err != nil {
			break
		}
		switch string(e[:4]) {
		case "CHT2", "CHTR", "CHCD":
			return Ident{Disc: "cd"}, nil
		case "CHGD":
			return Ident{Disc: "gd"}, nil
		case "DVD ":
			return Ident{Disc: "dvd"}, nil
		case "GDDD", "IDNT", "KEY ":
			return Ident{Disc: "hd"}, nil
		case "AVAV", "AVLD":
			return Ident{Disc: "av"}, nil
		}
		meta = int64(binary.BigEndian.Uint64(e[8:]))
	}
	return Ident{Disc: "hd"}, nil
}

// Folders.

// probeFolder reads a game kept as a folder: a PlayStation 3, 4, 5 or
// Vita game dumped whole, a Wii U game, an Xbox game, or a PC game.
func probeFolder(p string) (Ident, error) {
	sfo := func(rel string) (SFO, bool) {
		b, err := readSmall(filepath.Join(p, rel), 64<<10)
		if err != nil {
			return nil, false
		}
		s, err := ParseSFO(b)
		return s, err == nil
	}
	if s, ok := sfo("PS3_GAME/PARAM.SFO"); ok {
		return Ident{System: "ps3", Serial: s["TITLE_ID"], Title: s["TITLE"]}, nil
	}
	if s, ok := sfo("PSP_GAME/PARAM.SFO"); ok {
		return Ident{System: "psp", Serial: s["DISC_ID"], Title: s["TITLE"]}, nil
	}
	if b, err := readSmall(filepath.Join(p, "sce_sys", "param.json"), 1<<20); err == nil {
		var pj struct {
			TitleID string                     `json:"titleId"`
			Params  map[string]json.RawMessage `json:"localizedParameters"`
		}
		if json.Unmarshal(b, &pj) == nil {
			id := Ident{System: "ps5", Serial: pj.TitleID}
			var lang string
			json.Unmarshal(pj.Params["defaultLanguage"], &lang)
			var names struct {
				TitleName string `json:"titleName"`
			}
			if json.Unmarshal(pj.Params[cmpOr(lang, "en-US")], &names) == nil {
				id.Title = names.TitleName
			}
			return id, nil
		}
	}
	if s, ok := sfo("sce_sys/param.sfo"); ok {
		id := Ident{Serial: s["TITLE_ID"], Title: s["TITLE"], System: "ps4"}
		// The Vita's IDs start PC (PCSE00844), the PS4's CUSA or PLAS.
		if strings.HasPrefix(id.Serial, "PC") || strings.HasPrefix(id.Serial, "NP") {
			id.System = "vita"
		}
		return id, nil
	}
	if b, err := readSmall(filepath.Join(p, "meta", "meta.xml"), 1<<20); err == nil {
		id := Ident{System: "wiiu"}
		if m := regexp.MustCompile(`<product_code[^>]*>([^<]+)<`).FindSubmatch(b); m != nil {
			id.Serial = strings.TrimSpace(string(m[1]))
		}
		if m := regexp.MustCompile(`<longname_en[^>]*>([^<]+)<`).FindSubmatch(b); m != nil {
			id.Title = strings.Join(strings.Fields(string(m[1])), " ")
		}
		return id, nil
	}
	for _, name := range []string{"default.xbe", "default.xex"} {
		if f, err := os.Open(filepath.Join(p, name)); err == nil {
			var id Ident
			if name == "default.xbe" {
				id, _ = probeXBE(f)
			} else {
				id, _ = probeXEX(f)
			}
			f.Close()
			return id, nil
		}
	}
	// An Xbox 360 game on demand: a folder named for its title ID, with
	// its package in 00007000.
	if regexp.MustCompile(`^[0-9A-Fa-f]{8}$`).MatchString(filepath.Base(p)) {
		if st, err := os.Stat(filepath.Join(p, "00007000")); err == nil && st.IsDir() {
			return Ident{System: "xbox360", Serial: strings.ToUpper(filepath.Base(p))}, nil
		}
	}
	return Ident{}, ErrUnknown
}

func readSmall(p string, max int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

// Icon is the picture a PlayStation game carries of itself: its
// ICON0.PNG. ErrUnknown if it has none Box Office can read.
func Icon(p string) ([]byte, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		for _, rel := range []string{"PS3_GAME/ICON0.PNG", "PSP_GAME/ICON0.PNG", "sce_sys/icon0.png", "sce_sys/pic0.png"} {
			if b, err := readSmall(filepath.Join(p, rel), 4<<20); err == nil {
				return b, nil
			}
		}
		return nil, ErrUnknown
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".vpk":
		z, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		for _, f := range z.File {
			if strings.EqualFold(f.Name, "sce_sys/icon0.png") && f.UncompressedSize64 < 4<<20 {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, ErrUnknown
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 0x100)
	if n, _ := io.ReadFull(f, head); n < 0x40 {
		return nil, ErrUnknown
	}
	read := func(off, size int64) ([]byte, error) {
		if size <= 0 || size > 4<<20 {
			return nil, ErrUnknown
		}
		b := make([]byte, size)
		_, err := f.ReadAt(b, off)
		return b, err
	}
	switch {
	case string(head[:4]) == "\x00PBP":
		at := int64(binary.LittleEndian.Uint32(head[0x0C:]))
		end := int64(binary.LittleEndian.Uint32(head[0x10:]))
		return read(at, end-at)
	case string(head[:4]) == "\x7fCNT":
		if off, size, ok := pkgEntry(f, head, 0x1200); ok {
			return read(off, size)
		}
		return nil, ErrUnknown
	}
	s := newSectors(f, 0)
	fs, err := openISO(s)
	if err != nil {
		return nil, ErrUnknown
	}
	for _, rel := range []string{"PSP_GAME/ICON0.PNG", "PS3_GAME/ICON0.PNG"} {
		if b, err := fs.readFile(rel, 4<<20); err == nil {
			return b, nil
		}
	}
	return nil, ErrUnknown
}
