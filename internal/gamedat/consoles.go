package gamedat

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Index finds a game in a No-Intro DAT by a file's CRC or name.
type Index struct {
	byCRC    map[uint32][]hit
	byName   map[string]*Entry
	byTitle  map[string]*Entry
	bySerial map[string][]*Entry
	games    int
}

type hit struct {
	entry *Entry
	size  int64
}

// NewIndex indexes a DAT's games.
func NewIndex(dat *DAT) *Index {
	ix := &Index{byCRC: map[uint32][]hit{}, byName: map[string]*Entry{}, byTitle: map[string]*Entry{}, bySerial: map[string][]*Entry{}}
	ix.Add(dat)
	return ix
}

// Add takes in another list's games, such as a system's downloads after
// its discs.
func (ix *Index) Add(dat *DAT) {
	for i := range dat.Games {
		e := &dat.Games[i]
		ix.games++
		for _, k := range SerialKeys(e.Fields["serial"]) {
			if !slices.Contains(ix.bySerial[k], e) {
				ix.bySerial[k] = append(ix.bySerial[k], e)
			}
		}
		ix.byName[nameKey(e.Name())] = e
		if k := TitleKey(Title(e.Name())); k != "" {
			if have, ok := ix.byTitle[k]; !ok || betterVersion(e.Name(), have.Name()) {
				ix.byTitle[k] = e
			}
		}
		for _, r := range e.Roms {
			if r.HasCRC {
				ix.byCRC[r.CRC] = append(ix.byCRC[r.CRC], hit{e, r.Size})
			}
			ix.byName[nameKey(strings.TrimSuffix(r.Name, path.Ext(r.Name)))] = e
		}
	}
}

var reNintendoCode = regexp.MustCompile(`(?:DOL|RVL)-([A-Z0-9]{4})`)

// SerialKeys are the ways a list's serial, or one read from a game, is
// looked up: in capitals with only its letters and digits (SLUS-00067 is
// SLUS00067), each of a list of them, without a disc's -0 on the end, and
// for a GameCube or Wii game its four-letter ID (DL-DOL-GW7E-USA is GW7E).
func SerialKeys(serial string) []string {
	var out []string
	add := func(s string) {
		k := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 32
			}
			if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, s)
		if len(k) >= 4 && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, s := range strings.FieldsFunc(serial, func(r rune) bool { return r == ',' || r == ';' }) {
		s = strings.TrimSpace(s)
		up := strings.ToUpper(s)
		if m := reNintendoCode.FindStringSubmatch(up); m != nil {
			add(m[1])
			continue
		}
		add(s)
		// SLPM-65002-0: the first disc of SLPM-65002.
		if i := strings.LastIndexByte(s, '-'); i > 0 && len(s)-i <= 2 {
			add(s[:i])
		}
	}
	return out
}

// BySerial finds the versions of a game a serial or title ID is for.
func (ix *Index) BySerial(serial string) []*Entry {
	if ix == nil {
		return nil
	}
	for _, k := range SerialKeys(serial) {
		if es := ix.bySerial[k]; len(es) > 0 {
			return es
		}
	}
	return nil
}

// Len is how many games the index knows.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return ix.games
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

// ByTitle finds a version of a game by its title alone, as in a file
// named "Alter_Ego.nes": a finished one for everyone, where there is one.
func (ix *Index) ByTitle(name string) (*Entry, bool) {
	if ix == nil {
		return nil, false
	}
	e, ok := ix.byTitle[TitleKey(Title(name))]
	return e, ok
}

// TitleKey is a title's letters and digits, in lower case, so "Alter_Ego"
// and "Alter Ego" are the same.
func TitleKey(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// betterVersion says whether a is a better version of a game to show
// than b: not a demo, beta or prototype, then for the world or the USA.
func betterVersion(a, b string) bool {
	rank := func(n string) int {
		l := strings.ToLower(n)
		r := 0
		for _, w := range []string{"(demo", "(beta", "(proto", "(sample", "(pirate", "(hack"} {
			if strings.Contains(l, w) {
				r += 4
			}
		}
		if !strings.Contains(l, "(world") && !strings.Contains(l, "usa") {
			r++
		}
		return r
	}
	return rank(a) < rank(b)
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

// BetterVersion says whether a is a better version of a game to pick than
// b: not a demo, beta or prototype, then for the world or the USA.
func BetterVersion(a, b string) bool { return betterVersion(a, b) }
