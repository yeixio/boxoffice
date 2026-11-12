package gamedat

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A disc image's sectors, read for their data alone: a plain .iso has
// 2048 bytes a sector, a raw .bin 2352 with a header before the data.

var syncPattern = []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0}

// sectors reads a data track's sectors.
type sectors struct {
	r    io.ReaderAt
	base int64 // where the track starts in the file
	size int64 // bytes a sector: 2048, 2336 or 2352
	data int64 // where a sector's data starts in it
}

// newSectors sees how a track's sectors are laid out from its first one.
func newSectors(r io.ReaderAt, base int64) *sectors {
	head := make([]byte, 16)
	if _, err := r.ReadAt(head, base); err == nil && bytes.Equal(head[:12], syncPattern) {
		s := &sectors{r: r, base: base, size: 2352, data: 16}
		if head[15] == 2 { // mode 2, form 1: 8 more bytes of subheader
			s.data = 24
		}
		return s
	}
	return &sectors{r: r, base: base, size: 2048}
}

// read gives n sectors' data from lba on.
func (s *sectors) read(lba, n int64) ([]byte, error) {
	if n <= 0 || n > 4096 {
		return nil, errors.New("bad sector count")
	}
	out := make([]byte, 0, n*2048)
	buf := make([]byte, 2048)
	for i := range n {
		if _, err := s.r.ReadAt(buf, s.base+(lba+i)*s.size+s.data); err != nil {
			return nil, err
		}
		out = append(out, buf...)
	}
	return out, nil
}

// ISO 9660, the file system on most CDs and DVDs.

type isoFS struct {
	s        *sectors
	root     int64 // root folder's sector
	rootSize int64
	system   string // the volume's system name: PLAYSTATION, PSP GAME…
	volume   string
}

func openISO(s *sectors) (*isoFS, error) {
	pvd, err := s.read(16, 1)
	if err != nil {
		return nil, err
	}
	if pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, errors.New("not an ISO 9660 disc")
	}
	fs := &isoFS{s: s, system: strings.TrimSpace(string(pvd[8:40])), volume: strings.TrimSpace(string(pvd[40:72]))}
	fs.root = int64(binary.LittleEndian.Uint32(pvd[156+2:]))
	fs.rootSize = int64(binary.LittleEndian.Uint32(pvd[156+10:]))
	return fs, nil
}

// find finds a file or folder by its path ("PSP_GAME/PARAM.SFO"), any
// case, and says where it is and how big.
func (fs *isoFS) find(p string) (lba, size int64, dir bool, err error) {
	lba, size, dir = fs.root, fs.rootSize, true
	for _, part := range strings.Split(p, "/") {
		if !dir {
			return 0, 0, false, os.ErrNotExist
		}
		if size > 1<<20 {
			size = 1 << 20
		}
		b, err := fs.s.read(lba, (size+2047)/2048)
		if err != nil {
			return 0, 0, false, err
		}
		found := false
		for i := 0; i < len(b); {
			n := int(b[i])
			if n == 0 {
				// Records don't cross sectors: on to the next one.
				i = (i/2048 + 1) * 2048
				continue
			}
			if i+n > len(b) || n < 34 {
				break
			}
			rec := b[i : i+n]
			nameLen := int(rec[32])
			if 33+nameLen <= len(rec) {
				name := string(rec[33 : 33+nameLen])
				if j := strings.IndexByte(name, ';'); j >= 0 {
					name = name[:j]
				}
				name = strings.TrimSuffix(name, ".")
				if strings.EqualFold(name, part) {
					lba = int64(binary.LittleEndian.Uint32(rec[2:]))
					size = int64(binary.LittleEndian.Uint32(rec[10:]))
					dir = rec[25]&2 != 0
					found = true
					break
				}
			}
			i += n
		}
		if !found {
			return 0, 0, false, os.ErrNotExist
		}
	}
	return lba, size, dir, nil
}

// rootNames are the names of the files and folders in the disc's top
// folder, as written (upper case on most discs).
func (fs *isoFS) rootNames() []string {
	size := min(fs.rootSize, 1<<20)
	b, err := fs.s.read(fs.root, (size+2047)/2048)
	if err != nil {
		return nil
	}
	var names []string
	for i := 0; i < len(b); {
		n := int(b[i])
		if n == 0 {
			i = (i/2048 + 1) * 2048
			continue
		}
		if i+n > len(b) || n < 34 {
			break
		}
		rec := b[i : i+n]
		if l := int(rec[32]); 33+l <= len(rec) && !(l == 1 && rec[33] <= 1) {
			name := string(rec[33 : 33+l])
			if j := strings.IndexByte(name, ';'); j >= 0 {
				name = name[:j]
			}
			names = append(names, strings.TrimSuffix(name, "."))
		}
		i += n
	}
	return names
}

// readFile reads a small file off the disc, up to max bytes.
func (fs *isoFS) readFile(p string, max int64) ([]byte, error) {
	lba, size, dir, err := fs.find(p)
	if err != nil {
		return nil, err
	}
	if dir {
		return nil, os.ErrNotExist
	}
	if size > max {
		size = max
	}
	b, err := fs.s.read(lba, (size+2047)/2048)
	if err != nil {
		return nil, err
	}
	return b[:size], nil
}

// Cue sheets and GD-ROM track lists.

// cueTrack is one track a cue sheet or .gdi lists.
type cueTrack struct {
	file string // its file, next to the sheet
	data bool   // a data track, not audio
	at   int64  // where it starts in its file
}

// readCue reads a .cue sheet's tracks.
func readCue(p string) ([]cueTrack, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []cueTrack
	file := ""
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "FILE "):
			rest := strings.TrimSpace(line[5:])
			if strings.HasPrefix(rest, `"`) {
				if j := strings.Index(rest[1:], `"`); j >= 0 {
					rest = rest[1 : j+1]
				}
			} else if j := strings.LastIndexByte(rest, ' '); j > 0 {
				rest = rest[:j]
			}
			file = filepath.Join(filepath.Dir(p), filepath.Base(strings.ReplaceAll(rest, `\`, "/")))
		case strings.HasPrefix(upper, "TRACK "):
			if file != "" {
				out = append(out, cueTrack{file: file, data: !strings.Contains(upper, "AUDIO")})
			}
		}
	}
	return out, sc.Err()
}

// readGDI reads a Dreamcast .gdi's tracks: "3 45000 4 2352 track03.bin 0".
func readGDI(p string) ([]cueTrack, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []cueTrack
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 {
			continue // the number of tracks
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		name := fields[4]
		if strings.HasPrefix(name, `"`) {
			// A name with spaces.
			rest := strings.TrimSpace(line[strings.Index(line, `"`)+1:])
			if j := strings.Index(rest, `"`); j >= 0 {
				name = rest[:j]
			}
		}
		kind, _ := strconv.Atoi(fields[2])
		out = append(out, cueTrack{file: filepath.Join(filepath.Dir(p), filepath.Base(name)), data: kind == 4})
	}
	return out, nil
}

// readM3U reads a playlist of discs.
func readM3U(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil || len(b) > 64<<10 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, filepath.Join(filepath.Dir(p), filepath.Base(strings.ReplaceAll(line, `\`, "/"))))
	}
	return out
}
