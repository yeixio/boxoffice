package gamedat

import (
	"path"
	"strconv"
	"strings"
)

// Index finds a game in a No-Intro DAT by a file's CRC or name.
type Index struct {
	byCRC  map[uint32][]hit
	byName map[string]*Entry
}

type hit struct {
	entry *Entry
	size  int64
}

// NewIndex indexes a DAT's games.
func NewIndex(dat *DAT) *Index {
	ix := &Index{byCRC: map[uint32][]hit{}, byName: map[string]*Entry{}}
	for i := range dat.Games {
		e := &dat.Games[i]
		ix.byName[nameKey(e.Name())] = e
		for _, r := range e.Roms {
			if r.HasCRC {
				ix.byCRC[r.CRC] = append(ix.byCRC[r.CRC], hit{e, r.Size})
			}
			ix.byName[nameKey(strings.TrimSuffix(r.Name, path.Ext(r.Name)))] = e
		}
	}
	return ix
}

// Len is how many games the index knows.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.byCRC)
}

// ByCRC finds the game a file is from any of its CRCs (see CRCs), and the
// CRC that matched. A size of 0 matches any size.
func (ix *Index) ByCRC(crcs []uint32, size int64) (*Entry, uint32, bool) {
	if ix == nil {
		return nil, 0, false
	}
	for i, c := range crcs {
		for _, h := range ix.byCRC[c] {
			// A file without its header is smaller than on disk.
			if i > 0 || size == 0 || h.size == 0 || h.size == size {
				return h.entry, c, true
			}
		}
	}
	return nil, 0, false
}

// ByName finds a game by its name in the DAT, or its file's name.
func (ix *Index) ByName(name string) (*Entry, bool) {
	if ix == nil {
		return nil, false
	}
	e, ok := ix.byName[nameKey(name)]
	return e, ok
}

func nameKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Facts are what libretro's database knows about a game beyond its name.
type Facts struct {
	Year      string
	Developer string
	Publisher string
	Genre     string
	Players   int
}

// FactIndex finds a game's facts by its CRC, from libretro's metadata
// DATs (one each for genre, developer, publisher, release year, players).
type FactIndex map[uint32]*Facts

// Add takes in one of libretro's metadata DATs.
func (fi FactIndex) Add(dat *DAT) {
	for _, g := range dat.Games {
		for _, r := range g.Roms {
			if !r.HasCRC {
				continue
			}
			f := fi[r.CRC]
			if f == nil {
				f = &Facts{}
				fi[r.CRC] = f
			}
			for k, v := range g.Fields {
				switch k {
				case "releaseyear", "year":
					f.Year = v
				case "developer":
					f.Developer = v
				case "publisher":
					f.Publisher = v
				case "genre":
					f.Genre = v
				case "users":
					f.Players, _ = strconv.Atoi(v)
				}
			}
		}
	}
}

// Title is a No-Intro name without its tags: "Super Metroid (Japan, USA)
// (En,Ja)" is "Super Metroid".
func Title(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.Index(name, " ("); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	if i := strings.Index(name, " ["); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	return name
}

// Tags are the words in brackets after a No-Intro name, such as "Japan,
// USA" and "En,Ja".
func Tags(name string) []string {
	var out []string
	for {
		i := strings.IndexAny(name, "([")
		if i < 0 {
			return out
		}
		close := ")"
		if name[i] == '[' {
			close = "]"
		}
		j := strings.Index(name[i:], close)
		if j < 0 {
			return out
		}
		if t := strings.TrimSpace(name[i+1 : i+j]); t != "" {
			out = append(out, t)
		}
		name = name[i+j+1:]
	}
}

// ThumbName is a name as libretro's thumbnails spell it: &*/:`<>?\| and "
// become _.
func ThumbName(name string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("&*/:`<>?\\|\"", r) {
			return '_'
		}
		return r
	}, name)
}
