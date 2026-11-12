package games

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/store"
)

// CatalogGame is one version of a game on a system's list: No-Intro's for
// a console, MAME's for arcade games.
type CatalogGame struct {
	System string `json:"system"`
	// Name is its name in the list: "Super Metroid (Japan, USA) (En,Ja)",
	// or MAME's set name.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Tags are a console version's words in brackets: its regions,
	// languages and revision.
	Tags  []string `json:"tags,omitempty"`
	Year  string   `json:"year,omitempty"`
	Maker string   `json:"maker,omitempty"`
	Genre string   `json:"genre,omitempty"`
	// Parent is the MAME set a clone is a version of.
	Parent string `json:"parent,omitempty"`
	CRC    string `json:"crc,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Art    string `json:"-"`

	words string // " super metroid ", for matching
}

// Group is the key the game's versions share.
func (g CatalogGame) Group() string {
	if g.Parent != "" {
		return g.Parent
	}
	if sys, ok := gamedat.LookupSystem(g.System); ok && sys.Arcade {
		return g.Name
	}
	return gamedat.TitleKey(g.Title)
}

// catalog is one system's list, kept in memory for a day.
type catalog struct {
	games   []CatalogGame
	byName  map[string]int
	version string
	at      time.Time
}

// catalogFor reads a system's list, the first time and again after a day
// (or when MAME's version changes).
func (s *Service) catalogFor(ctx context.Context, system string) (*catalog, error) {
	sys, ok := gamedat.LookupSystem(system)
	if !ok {
		return nil, fmt.Errorf("Box Office doesn't know a system called %q", system)
	}
	version := ""
	if sys.Arcade {
		v, err := s.MAMEVersion(ctx)
		if err != nil {
			return nil, err
		}
		version = v
	}
	s.catMu.Lock()
	c := s.catalogs[system]
	s.catMu.Unlock()
	if c != nil && c.version == version && time.Since(c.at) < 24*time.Hour {
		return c, nil
	}
	var err error
	if sys.Arcade {
		c, err = s.mameCatalog(ctx, version)
	} else {
		c, err = s.consoleCatalog(ctx, sys)
	}
	if err != nil {
		return nil, err
	}
	c.version, c.at = version, time.Now()
	c.byName = make(map[string]int, len(c.games))
	for i := range c.games {
		c.games[i].words = " " + strings.Join(searchWords(c.games[i].Title), " ") + " "
		c.byName[c.games[i].Name] = i
	}
	s.catMu.Lock()
	if s.catalogs == nil {
		s.catalogs = map[string]*catalog{}
	}
	s.catalogs[system] = c
	s.catMu.Unlock()
	return c, nil
}

func (s *Service) consoleCatalog(ctx context.Context, sys gamedat.System) (*catalog, error) {
	p, err := s.libretroDAT(ctx, "no-intro", sys.Libretro)
	if err != nil {
		return nil, fmt.Errorf("couldn't download No-Intro's list of %s games: %w", sys.Name, err)
	}
	if p == "" {
		return nil, fmt.Errorf("libretro's database has no No-Intro list of %s games", sys.Name)
	}
	dat, err := readDAT(p)
	if err != nil {
		return nil, fmt.Errorf("couldn't read No-Intro's list of %s games: %w", sys.Name, err)
	}
	facts := s.facts(ctx, sys)
	c := &catalog{games: make([]CatalogGame, 0, len(dat.Games))}
	for _, e := range dat.Games {
		name := e.Name()
		// BIOS files aren't games.
		if name == "" || strings.HasPrefix(name, "[BIOS]") || len(e.Roms) == 0 {
			continue
		}
		g := CatalogGame{System: sys.Key, Name: name, Title: gamedat.Title(name), Tags: gamedat.Tags(name), Art: gamedat.ThumbName(name)}
		for _, r := range e.Roms {
			g.Size += r.Size
		}
		if r := e.Roms[0]; r.HasCRC {
			g.CRC = fmt.Sprintf("%08X", r.CRC)
			if f := facts[r.CRC]; f != nil {
				g.Year, g.Genre, g.Maker = f.Year, f.Genre, cmp.Or(f.Publisher, f.Developer)
			}
		}
		c.games = append(c.games, g)
	}
	return c, nil
}

func (s *Service) mameCatalog(ctx context.Context, version string) (*catalog, error) {
	p, err := s.mameList(ctx, version)
	if err != nil {
		return nil, err
	}
	rc, err := openZipped(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Only names and facts: the files of every set are too many to keep.
	l, err := gamedat.ParseXML(rc, func(string) bool { return false })
	if err != nil {
		return nil, fmt.Errorf("couldn't read MAME %s's list: %w", version, err)
	}
	c := &catalog{games: make([]CatalogGame, 0, len(l.Machines))}
	for _, m := range l.Machines {
		if m.IsBIOS || m.IsDevice || m.IsMechanical || !m.Runnable || !m.HasRoms {
			continue
		}
		c.games = append(c.games, CatalogGame{System: "mame", Name: m.Name, Title: m.Description, Year: m.Year, Maker: m.Manufacturer,
			Parent: m.CloneOf, Art: gamedat.ThumbName(m.Description)})
	}
	slices.SortFunc(c.games, func(a, b CatalogGame) int { return cmp.Compare(a.Name, b.Name) })
	return c, nil
}

func readDAT(p string) (*gamedat.DAT, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return gamedat.ParseClrMame(f)
}

// searchWords are a title's words for matching: letters and digits, in
// lower case.
func searchWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Lookup is a game found on a system's list, with all its versions.
type Lookup struct {
	System string `json:"system"`
	Title  string `json:"title"`
	// Best is the version picked unless another is: not a demo or a
	// prototype, for the world or the USA where there is one. For MAME,
	// the parent set.
	Best     CatalogGame   `json:"best"`
	Versions []CatalogGame `json:"versions"`
	// OnDisk says a version of it is in the system's folder already, and
	// WantID which version is wanted, if one is.
	OnDisk   bool   `json:"onDisk"`
	WantID   int64  `json:"wantId,omitempty"`
	WantName string `json:"wantName,omitempty"`
}

// MaxLookups is how many games a lookup gives at most.
const MaxLookups = 60

// LookupGames finds games on the systems' lists whose titles hold every
// word of q (the last one may be the start of a word). systems are the
// systems to look in: all the ones with folders when empty, or every
// system when none has a folder yet.
func (s *Service) LookupGames(ctx context.Context, q string, systems []string) ([]Lookup, error) {
	if err := s.on(ctx); err != nil {
		return nil, err
	}
	want := searchWords(q)
	if len(want) == 0 {
		return []Lookup{}, nil
	}
	known, err := s.Store.GameSystems(ctx)
	if err != nil {
		return nil, err
	}
	if len(systems) == 0 {
		for _, k := range known {
			if !slices.Contains(systems, k.System) {
				systems = append(systems, k.System)
			}
		}
	}
	if len(systems) == 0 {
		for _, sys := range gamedat.Systems {
			systems = append(systems, sys.Key)
		}
	}
	type scored struct {
		l     Lookup
		score int
	}
	var found []scored
	var errs []error
	key := gamedat.TitleKey(q)
	for _, system := range systems {
		c, err := s.catalogFor(ctx, system)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			errs = append(errs, err)
			continue
		}
		groups := map[string]*Lookup{}
		var order []string
		for _, g := range c.games {
			if !matchWords(g.words, want) {
				continue
			}
			k := g.Group()
			l := groups[k]
			if l == nil {
				l = &Lookup{System: system}
				groups[k] = l
				order = append(order, k)
			}
			l.Versions = append(l.Versions, g)
		}
		for _, k := range order {
			l := groups[k]
			// A MAME clone found brings its parent along, as the game.
			if system == "mame" {
				if i, ok := c.byName[k]; ok && !slices.ContainsFunc(l.Versions, func(v CatalogGame) bool { return v.Name == k }) {
					l.Versions = append([]CatalogGame{c.games[i]}, l.Versions...)
				}
			}
			best := l.Versions[0]
			for _, v := range l.Versions[1:] {
				if system == "mame" {
					if v.Name == k {
						best = v
					}
				} else if gamedat.BetterVersion(v.Name, best.Name) || !gamedat.BetterVersion(best.Name, v.Name) && v.Name > best.Name {
					// As good otherwise, the later revision: (v1.4)
					// over (v1.1), (Rev 1) over none.
					best = v
				}
			}
			l.Best, l.Title = best, best.Title
			score := 2
			switch tk := gamedat.TitleKey(l.Title); {
			case tk == key:
				score = 0
			case strings.HasPrefix(tk, key):
				score = 1
			}
			found = append(found, scored{*l, score})
		}
	}
	if len(found) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	slices.SortStableFunc(found, func(a, b scored) int {
		if c := cmp.Compare(a.score, b.score); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.l.Title), len(b.l.Title)); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.l.Title), strings.ToLower(b.l.Title))
	})
	if len(found) > MaxLookups {
		found = found[:MaxLookups]
	}
	out := make([]Lookup, 0, len(found))
	for _, f := range found {
		out = append(out, f.l)
	}
	if err := s.markLookups(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// matchWords says whether every word wanted is in words (spaced), the
// last one perhaps as the start of a word.
func matchWords(words string, want []string) bool {
	for i, w := range want {
		if i == len(want)-1 {
			if !strings.Contains(words, " "+w) {
				return false
			}
		} else if !strings.Contains(words, " "+w+" ") {
			return false
		}
	}
	return true
}

// markLookups says which games found are on disk already, and which are
// wanted.
func (s *Service) markLookups(ctx context.Context, ls []Lookup) error {
	wants, err := s.Store.GameWants(ctx)
	if err != nil {
		return err
	}
	have := map[string]map[string]bool{} // system -> names and groups on disk
	systems, err := s.Store.GameSystems(ctx)
	if err != nil {
		return err
	}
	for _, sys := range systems {
		if !slices.ContainsFunc(ls, func(l Lookup) bool { return l.System == sys.System }) {
			continue
		}
		gs, err := s.Store.Games(ctx, sys.ID)
		if err != nil {
			return err
		}
		m := have[sys.System]
		if m == nil {
			m = map[string]bool{}
			have[sys.System] = m
		}
		for _, g := range gs {
			if g.Status == gamedat.Complete || g.Status == gamedat.NeedsBIOS {
				m[g.Name] = true
				if sys.System != "mame" {
					m["\x00"+g.Parent] = true
				}
			}
		}
	}
	for i := range ls {
		l := &ls[i]
		m := have[l.System]
		for _, v := range l.Versions {
			if m[v.Name] || l.System != "mame" && m["\x00"+gamedat.TitleKey(v.Title)] {
				l.OnDisk = true
			}
			for _, w := range wants {
				if w.System == l.System && w.Name == v.Name {
					l.WantID, l.WantName = w.ID, w.Name
				}
			}
		}
	}
	return nil
}

// CatalogGameByName finds a version on a system's list.
func (s *Service) CatalogGameByName(ctx context.Context, system, name string) (CatalogGame, error) {
	c, err := s.catalogFor(ctx, system)
	if err != nil {
		return CatalogGame{}, err
	}
	i, ok := c.byName[name]
	if !ok {
		return CatalogGame{}, NotOnListError{name, systemName(system)}
	}
	return c.games[i], nil
}

// NotOnListError means a game isn't on its system's list.
type NotOnListError struct{ Name, System string }

func (e NotOnListError) Error() string {
	return fmt.Sprintf("%s isn't on the list of %s games", e.Name, e.System)
}

func systemName(key string) string {
	if sys, ok := gamedat.LookupSystem(key); ok {
		return sys.Name
	}
	return key
}

// LookupArt is the picture of a game on a system's list, as ArtFile.
func (s *Service) LookupArt(ctx context.Context, system, name string) (string, error) {
	g, err := s.CatalogGameByName(ctx, system, name)
	if err != nil {
		return "", ErrNoArt
	}
	sg := store.Game{Name: g.Name, Title: g.Title, Art: g.Art}
	if g.Parent != "" {
		if p, err := s.CatalogGameByName(ctx, system, g.Parent); err == nil {
			sg.ParentTitle = p.Title
		}
	}
	return s.ArtFile(ctx, sg, system)
}
