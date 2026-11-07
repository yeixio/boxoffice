package unpack

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Obfuscated posts name their RAR volumes anything: random hashes, a .rar
// on every volume, or no extension at all. So RAR sets are found by what
// the files hold. Each volume's header says whether it's part of a set,
// whether it's the first, and (in RAR 5, and in RAR 3 and 4's end block)
// its number.

// rarVolume is what a file's RAR headers say about it.
type rarVolume struct {
	path  string
	ver   int  // 4 (RAR 1.5 to 4) or 5
	multi bool // part of a set of volumes
	first bool
	num   int // from 0; -1 when the headers don't say
	// ended is set when a RAR 3/4 volume's end block was read, and last
	// when that says no volume follows.
	ended, last bool
}

var (
	sigRAR4 = []byte("Rar!\x1a\x07\x00")
	sigRAR5 = []byte("Rar!\x1a\x07\x01\x00")
	// reStem takes the volume part off a name, to tell sets apart.
	reStem = regexp.MustCompile(`(?i)(\.part\d+)?\.(rar|r\d\d|s\d\d|\d{3})$`)
)

// probeRAR reads a file's RAR headers. It's false for anything that isn't
// a RAR archive.
func probeRAR(path string) (rarVolume, bool) {
	f, err := os.Open(path)
	if err != nil {
		return rarVolume{}, false
	}
	defer f.Close()
	sig := make([]byte, 8)
	if _, err := io.ReadFull(f, sig); err != nil {
		return rarVolume{}, false
	}
	v := rarVolume{path: path, num: -1}
	switch {
	case string(sig) == string(sigRAR5):
		v.ver = 5
		return v, probeRAR5(bufio.NewReader(f), &v)
	case string(sig[:7]) == string(sigRAR4):
		v.ver = 4
		if _, err := f.Seek(7, io.SeekStart); err != nil {
			return v, false
		}
		return v, probeRAR4(f, &v)
	}
	return v, false
}

func readVint(r io.ByteReader) (uint64, error) {
	var n uint64
	for shift := uint(0); shift < 70; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return n, nil
		}
	}
	return 0, errors.New("bad vint")
}

// probeRAR5 reads the main archive header, which comes right after the
// signature unless the headers are encrypted.
func probeRAR5(r *bufio.Reader, v *rarVolume) bool {
	if _, err := r.Discard(4); err != nil { // CRC
		return false
	}
	var fields [3]uint64 // size, type, flags
	for i := range fields {
		n, err := readVint(r)
		if err != nil {
			return false
		}
		fields[i] = n
	}
	typ, flags := fields[1], fields[2]
	if typ == 4 {
		// Encrypted headers: it's a RAR, but the rest needs the password.
		return true
	}
	if typ != 1 {
		return false
	}
	if flags&0x1 != 0 { // extra area size
		if _, err := readVint(r); err != nil {
			return false
		}
	}
	if flags&0x2 != 0 { // data size
		if _, err := readVint(r); err != nil {
			return false
		}
	}
	arc, err := readVint(r)
	if err != nil {
		return false
	}
	v.multi = arc&0x1 != 0
	v.num = 0
	if arc&0x2 != 0 {
		n, err := readVint(r)
		if err != nil {
			return false
		}
		v.num = int(n)
	}
	v.first = v.num == 0
	return true
}

// probeRAR4 reads the main header for the flags, then walks the blocks to
// the end block, which has the volume number (RAR 3 and later) and says
// whether another volume follows.
func probeRAR4(f *os.File, v *rarVolume) bool {
	var h [7]byte
	if _, err := io.ReadFull(f, h[:]); err != nil || h[2] != 0x73 {
		return false
	}
	flags := binary.LittleEndian.Uint16(h[3:])
	size := int64(binary.LittleEndian.Uint16(h[5:]))
	v.multi = flags&0x0001 != 0
	if flags&0x0100 != 0 {
		v.first, v.num = true, 0
	}
	if flags&0x0080 != 0 {
		return true // encrypted headers
	}
	pos := 7 + size
	for range 100000 {
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return true
		}
		var b [11]byte
		if _, err := io.ReadFull(f, b[:7]); err != nil {
			return true
		}
		typ := b[2]
		bf := binary.LittleEndian.Uint16(b[3:])
		hsize := int64(binary.LittleEndian.Uint16(b[5:]))
		if hsize < 7 {
			return true
		}
		if typ == 0x7b { // end of archive
			v.ended = true
			v.last = bf&0x0001 == 0 // EARC_NEXT_VOLUME
			if bf&0x0008 != 0 {     // EARC_VOLNUMBER
				skip := int64(0)
				if bf&0x0002 != 0 {
					skip = 4 // data CRC
				}
				var n [2]byte
				if _, err := f.ReadAt(n[:], pos+7+skip); err == nil {
					v.num = int(binary.LittleEndian.Uint16(n[:]))
					v.first = v.num == 0
				}
			}
			return true
		}
		data := int64(0)
		if bf&0x8000 != 0 || typ == 0x74 || typ == 0x7a {
			var add [4]byte
			if _, err := io.ReadFull(f, add[:]); err != nil {
				return true
			}
			data = int64(binary.LittleEndian.Uint32(add[:]))
			if (typ == 0x74 || typ == 0x7a) && bf&0x0100 != 0 { // high pack size
				var hi [4]byte
				if _, err := f.ReadAt(hi[:], pos+32); err == nil {
					data |= int64(binary.LittleEndian.Uint32(hi[:])) << 32
				}
			}
		}
		pos += hsize + data
	}
	return true
}

// rarSet is one RAR archive: its volumes, first one first.
type rarSet []rarVolume

// findRARSets probes files (names in dir) for RAR sets, by content.
// Files the name-based rules call 7-Zip or ZIP aren't probed.
func findRARSets(dir string, files []string) []rarSet {
	var vols []rarVolume
	for _, f := range files {
		low := strings.ToLower(f)
		if strings.HasSuffix(low, ".par2") || strings.HasSuffix(low, ".7z") || re7zSplit.MatchString(f) || strings.HasSuffix(low, ".zip") || reZipVol.MatchString(f) {
			continue
		}
		if v, ok := probeRAR(filepath.Join(dir, f)); ok {
			vols = append(vols, v)
		}
	}
	var sets []rarSet
	var loose []rarVolume // later volumes of a set, not yet placed
	for _, v := range vols {
		switch {
		case !v.multi:
			sets = append(sets, rarSet{v})
		case v.first:
			sets = append(sets, rarSet{v})
		default:
			loose = append(loose, v)
		}
	}
	// Volumes whose headers don't number them (RAR 2 and older, or
	// encrypted headers) and that share a name no set has make a set of
	// their own, first by name: an old release posted beside a newer
	// archive (subtitles, say) mustn't be poured into that one.
	byStem := map[string][]rarVolume{}
	for _, v := range loose {
		st := stem(v.path)
		if v.num < 0 && !slices.ContainsFunc(sets, func(s rarSet) bool { return s[0].multi && stem(s[0].path) == st }) {
			byStem[st] = append(byStem[st], v)
		}
	}
	for _, vs := range byStem {
		if len(vs) < 2 {
			continue
		}
		slices.SortFunc(vs, func(a, b rarVolume) int { return strings.Compare(volOrder(a.path), volOrder(b.path)) })
		first := vs[0]
		first.first, first.num = true, 0
		sets = append(sets, rarSet{first})
		loose = slices.DeleteFunc(loose, func(v rarVolume) bool { return v.path == first.path })
	}
	// A set whose first volume's header couldn't be read (encrypted
	// headers, or RAR 2 and older): the name says which is first.
	if len(sets) == 0 && len(loose) > 0 {
		slices.SortFunc(loose, func(a, b rarVolume) int { return strings.Compare(volOrder(a.path), volOrder(b.path)) })
		first := loose[0]
		first.first, first.num = true, 0
		sets = append(sets, rarSet{first})
		loose = loose[1:]
	}

	// Each loose volume goes to the set its name matches. With one set
	// (the usual case) or names that say nothing, it goes to the only set
	// that takes it.
	multi := slices.IndexFunc(sets, func(s rarSet) bool { return s[0].multi })
	for _, v := range loose {
		i := slices.IndexFunc(sets, func(s rarSet) bool { return s[0].multi && stem(s[0].path) == stem(v.path) })
		if i < 0 {
			n := 0
			for j, s := range sets {
				if s[0].multi && s[0].ver == v.ver {
					i, n = j, n+1
				}
			}
			if n != 1 {
				i = -1
			}
		}
		if i < 0 {
			i = multi
		}
		if i >= 0 {
			sets[i] = append(sets[i], v)
		}
	}
	for i, s := range sets {
		rest := s[1:]
		slices.SortStableFunc(rest, func(a, b rarVolume) int {
			if a.num >= 0 && b.num >= 0 && a.num != b.num {
				return a.num - b.num
			}
			return strings.Compare(volOrder(a.path), volOrder(b.path))
		})
		// The same volume twice (a repost) only counts once.
		sets[i] = slices.CompactFunc(s, func(a, b rarVolume) bool { return a.num >= 0 && a.num == b.num })
	}
	slices.SortFunc(sets, func(a, b rarSet) int { return strings.Compare(a[0].path, b[0].path) })
	return sets
}

func stem(p string) string { return strings.ToLower(reStem.ReplaceAllString(filepath.Base(p), "")) }

// volOrder sorts names of one set into volume order: .rar before .r00,
// and part2 before part10.
func volOrder(p string) string {
	name := strings.ToLower(filepath.Base(p))
	if m := rePart.FindStringSubmatch(name); m != nil {
		return fmt.Sprintf("%s.part%08s", rePart.ReplaceAllString(name, ""), m[1])
	}
	if strings.HasSuffix(name, ".rar") {
		return strings.TrimSuffix(name, ".rar") + ".!"
	}
	return name
}

// volumeFS shows rardecode a set's volumes as vol.part0001.rar,
// vol.part0002.rar and so on, whatever they're really called, so the
// files themselves are never renamed.
type volumeFS struct {
	paths []string
	width int
}

func (v *volumeFS) name(i int) string { return fmt.Sprintf("vol.part%0*d.rar", v.width, i+1) }

// real is the real path of a volume rardecode names. RAR 3 and 4 sets
// made with old-style naming have it ask for .r00, .r01… after the first,
// so those names count too.
func (v *volumeFS) real(name string) string {
	old := v.name(0)
	for i := range v.paths {
		if i > 0 {
			old = nextOldName(old)
		}
		if v.name(i) == name || old == name {
			return v.paths[i]
		}
	}
	return ""
}

// nextOldName is the volume after name in old-style naming, as rardecode
// works it out: .rar, .r00 … .r99, .s00 …
func nextOldName(name string) string {
	i := strings.LastIndex(name, ".")
	b := []byte(name[i+1:])
	if len(b) < 3 || b[1] < '0' || b[1] > '9' || b[2] < '0' || b[2] > '9' {
		return name[:i+2] + "00"
	}
	for j := 2; j >= 0; j-- {
		if b[j] != '9' {
			b[j]++
			break
		}
		if j == 0 {
			b[j] = 'A'
		} else {
			b[j] = '0'
		}
	}
	return name[:i+1] + string(b)
}

func (v *volumeFS) Open(name string) (fs.File, error) {
	if p := v.real(name); p != "" {
		return os.Open(p)
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

var _ fs.FS = (*volumeFS)(nil)

// checkSet says what's wrong with a set's volumes before extracting, as
// rardecode reads volumes in the order it's given and, for RAR 3 and 4,
// can't tell when that's wrong.
func checkSet(paths []string) error {
	var vols []rarVolume
	for _, p := range paths {
		v, ok := probeRAR(p)
		if !ok {
			return nil // extracting says what's wrong
		}
		vols = append(vols, v)
	}
	if len(vols) < 2 || !vols[0].multi {
		return nil
	}
	for i, v := range vols {
		if v.num >= 0 && v.num != i {
			return fmt.Errorf("volume %d of the archive is missing", i+1)
		}
		if v.num < 0 && i > 0 && stem(v.path) != stem(vols[0].path) {
			return fmt.Errorf("the archive's volumes have scrambled names and don't say which comes first, so they can't be put in order")
		}
	}
	if last := vols[len(vols)-1]; last.ended && !last.last {
		return fmt.Errorf("the archive is missing volumes at its end: Box Office has %d, and the last of them says more follow", len(vols))
	}
	return nil
}
