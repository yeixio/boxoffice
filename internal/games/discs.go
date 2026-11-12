package games

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/store"
)

// Discs, downloads and PC games: too big to hash, so each is known by the
// serial or title ID written in it (read from a few small parts of it),
// or by the name written in it, or else by its file's name, and matched to
// its system's list (Redump's, No-Intro's, or the eShop's) that way.

// Problems a disc system's game can have.
const (
	// ProblemOtherSystem: what's in it says it's for another system.
	ProblemOtherSystem = "otherSystem"
	// ProblemUpdate and ProblemDLC: it's an update to a game, or added
	// content, not the game.
	ProblemUpdate = "update"
	ProblemDLC    = "dlc"
)

// unit is one game in a folder: a file, a cue sheet and its tracks, or a
// folder that's one game.
type unit struct {
	path  string
	parts []string // the files a sheet names, besides itself
	dir   bool
}

// files are all the unit's files on disk, itself first.
func (u unit) files() []string {
	return append([]string{u.path}, u.parts...)
}

// folderPerGame says each folder in a system's folder is one game, as PC
// games are, whatever's in it.
func folderPerGame(sys gamedat.System) bool {
	return sys.Key == "pc" || sys.Key == "dos" || sys.Key == "xboxone"
}

// sheet says whether a file names a game's other files: a cue sheet, a
// .gdi, a CloneCD .ccd.
func sheet(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cue", ".gdi", ".ccd":
		return true
	}
	return false
}

// findUnits finds the games in a system's folder: sheets with their
// tracks, files of the system's kinds, and folders that are one game
// (a PlayStation 3 game dumped whole, a PC game).
func findUnits(root string, sys gamedat.System) []unit {
	return collectUnits(root, func(name string, top bool) bool {
		switch {
		case sys.Takes(name) && (sys.Key != "pc" && sys.Key != "dos" || top):
		case gamedat.IsArchive(name) && (top || !folderPerGame(sys)):
		default:
			return false
		}
		return true
	}, func(p string, depth int) bool { return sys.GameFolders && gameFolder(p, sys, depth) })
}

// anyUnits finds the games of any system in a folder.
func anyUnits(root string) []unit {
	return collectUnits(root, func(name string, top bool) bool {
		return gamedat.IsGameFile(name) || strings.EqualFold(filepath.Ext(name), ".chd")
	}, func(p string, depth int) bool {
		id, err := gamedat.Probe(p)
		return err == nil && id.System != ""
	})
}

// collectUnits finds the games in a folder: sheets with their tracks,
// files takes wants (top: in the folder itself, not a folder in it), and
// folders folder says are one game.
func collectUnits(root string, takes func(name string, top bool) bool, folder func(p string, depth int) bool) []unit {
	var out []unit
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		depth := strings.Count(rel, string(filepath.Separator))
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if folder(p, depth) {
				out = append(out, unit{path: p, dir: true})
				return filepath.SkipDir
			}
			// Three folders down is as deep as a system's games go.
			if depth >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	claimed := map[string]bool{}
	for _, f := range files {
		if !sheet(f) {
			continue
		}
		parts := sheetParts(f)
		for _, x := range parts {
			claimed[x] = true
		}
		out = append(out, unit{path: f, parts: parts})
	}
	for _, f := range files {
		name := filepath.Base(f)
		ext := strings.ToLower(filepath.Ext(name))
		if claimed[f] || sheet(f) || ext == ".m3u" || ext == ".sub" || ext == ".sbi" {
			continue
		}
		if !takes(name, filepath.Dir(f) == root) {
			continue
		}
		out = append(out, unit{path: f})
	}
	slices.SortFunc(out, func(a, b unit) int { return strings.Compare(a.path, b.path) })
	return out
}

// gameFolder says whether a folder is one game: one that says so (a
// PlayStation 3 or Vita game, a Wii U or Xbox game), or for a PC any
// folder in the system's folder.
func gameFolder(p string, sys gamedat.System, depth int) bool {
	if id, err := gamedat.Probe(p); err == nil && id.System != "" {
		return true
	}
	return folderPerGame(sys) && depth == 0
}

// unitSize is a game's size: its files', or everything in its folder.
func unitSize(u unit) (size, mtime int64) {
	if u.dir {
		st, err := os.Stat(u.path)
		if err == nil {
			mtime = st.ModTime().Unix()
		}
		filepath.WalkDir(u.path, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if i, err := d.Info(); err == nil {
					size += i.Size()
				}
			}
			return nil
		})
		return size, mtime
	}
	for i, f := range u.files() {
		if st, err := os.Stat(f); err == nil {
			size += st.Size()
			if i == 0 {
				mtime = st.ModTime().Unix()
			}
		}
	}
	return size, mtime
}

// Names.

var (
	// reSceneTags are the words a release's name has after the game's.
	reSceneTags = regexp.MustCompile(`(?i)[ ._-](ps[1-5]|psx|psv|psp|vita|nsw|switch|pc|gog|steam|x360|xbox(360|one)?|wiiu|wii|ngc|gamecube|3ds|nds|dc|iso|multi\d*|eur|usa|pal|ntsc(-[uj])?|jpn?|repack|proper|internal|update|dlc|v\d[\w.]*|\d{4}-\d{2}-\d{2}|incl|cracked|readnfo|rip|dvd\d*|cd\d)\b`)
	// reGroup is the scene group a release's name ends with: -CODEX.
	reGroup = regexp.MustCompile(`-[A-Za-z0-9]+$`)
)

// TitleFromName makes a game's title from a file's or folder's name:
// "Bloodborne.PS4-DUPLEX" is Bloodborne, and "Final Fantasy VII (USA)
// (Disc 1)" Final Fantasy VII.
func TitleFromName(name string) string {
	name = strings.TrimSpace(name)
	if !strings.Contains(name, " ") {
		name = reGroup.ReplaceAllString(name, "")
		name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	}
	if loc := reSceneTags.FindStringIndex(" " + name); loc != nil && loc[0] > 1 {
		name = name[:loc[0]-1]
	}
	return strings.TrimSpace(strings.Trim(gamedat.Title(name), "-–_. "))
}

// stemOf is a game's name on disk: its file's without the extension, or
// its folder's.
func stemOf(u unit) string {
	base := filepath.Base(u.path)
	if u.dir {
		return base
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	// Game.nkit.iso, game.iso.chd…
	if ext := strings.ToLower(filepath.Ext(base)); ext == ".nkit" || ext == ".iso" {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return base
}

// Matching.

// match finds a game on its system's lists: by the serial or title ID in
// it, then the name written in it, then its file's name. It gives the
// version of the game, or nil.
func match(ix *gamedat.Index, id gamedat.Ident, stem string) *gamedat.Entry {
	if id.Serial != "" {
		serial := id.Serial
		if id.Update || id.DLC {
			serial = gamedat.SwitchBase(serial)
		}
		es := ix.BySerial(serial)
		if len(es) > 0 {
			// Several versions share a serial: the one named like it, or
			// the best.
			if e, ok := ix.ByName(stem); ok && slices.Contains(es, e) {
				return e
			}
			best := es[0]
			for _, e := range es[1:] {
				if gamedat.BetterVersion(e.Name(), best.Name()) {
					best = e
				}
			}
			return best
		}
	}
	if e, ok := ix.ByName(stem); ok {
		return e
	}
	if id.Title != "" {
		if e, ok := ix.ByTitle(id.Title); ok {
			return e
		}
	}
	if e, ok := ix.ByTitle(TitleFromName(stem)); ok {
		return e
	}
	return nil
}

// identify says what game a unit is, as the store keeps it.
func identify(ix *gamedat.Index, facts gamedat.FactIndex, sys gamedat.System, u unit, hasList bool) store.Game {
	stem := stemOf(u)
	g := store.Game{Path: u.path}
	g.Size, g.MTime = unitSize(u)
	id, err := gamedat.Probe(u.path)
	if err != nil && !errors.Is(err, gamedat.ErrUnknown) {
		g.Name, g.Title, g.Status = stem, TitleFromName(stem), gamedat.Unreadable
		g.Problems = []store.GameProblem{{Kind: gamedat.ProblemUnreadable, Name: err.Error()}}
		g.Parent, g.ParentTitle = groupKey(g.Title), g.Title
		return g
	}
	g.Serial = id.Serial
	e := match(ix, id, stem)
	if e != nil {
		g.Name, g.Title, g.Status, g.Art = e.Name(), gamedat.Title(e.Name()), gamedat.Identified, entryArt(e)
		f := entryFacts(facts, e)
		g.Year, g.Genre, g.Players, g.Maker = f.Year, f.Genre, f.Players, cmp.Or(f.Publisher, f.Developer)
		if e.Fields["serial"] != "" && g.Serial == "" {
			g.Serial = e.Fields["serial"]
		}
	} else {
		g.Name, g.Title = stem, cmp.Or(id.Title, TitleFromName(stem))
		g.Status = gamedat.Unknown
		// A system with no list knows its games by name: that's all it can.
		if !hasList {
			g.Status = gamedat.Identified
		}
		g.Art = gamedat.ThumbName(g.Title)
		if id.System != "" && id.System != sys.Key {
			g.Problems = append(g.Problems, store.GameProblem{Kind: ProblemOtherSystem, Name: systemName(id.System)})
		}
	}
	switch {
	case id.Update:
		g.Name += " (Update)"
		g.Problems = append(g.Problems, store.GameProblem{Kind: ProblemUpdate, Name: filepath.Base(u.path)})
	case id.DLC:
		g.Name += " (DLC)"
		g.Problems = append(g.Problems, store.GameProblem{Kind: ProblemDLC, Name: filepath.Base(u.path)})
	}
	g.Parent, g.ParentTitle = groupKey(g.Title), g.Title
	return g
}

// checkDiscs reads a disc or download system's folder: each game's
// serial or title ID, matched to its lists.
func (s *Service) checkDiscs(ctx context.Context, sys store.GameSystem, def gamedat.System) (store.GameCheck, error) {
	s.set(func(p *Progress) { p.Step = "list" })
	ix, label, err := s.listIndex(ctx, def)
	if err != nil {
		return store.GameCheck{}, err
	}
	facts := s.facts(ctx, def)
	s.set(func(p *Progress) { p.Step = "reading" })
	before, err := s.Store.Games(ctx, sys.ID)
	if err != nil {
		return store.GameCheck{}, err
	}
	// A file unchanged since the last check isn't read again.
	prev := map[string]store.Game{}
	for _, g := range before {
		prev[g.Path] = g
	}
	units := findUnits(sys.Path, def)
	s.set(func(p *Progress) { p.Total = len(units) })
	c := store.GameCheck{List: label}
	for i, u := range units {
		if ctx.Err() != nil {
			return store.GameCheck{}, ctx.Err()
		}
		if i%10 == 0 {
			s.set(func(pr *Progress) { pr.Done = i })
		}
		if !u.dir {
			if g, ok := prev[u.path]; ok && g.Status != gamedat.Unreadable {
				if size, mtime := unitSize(u); size == g.Size && mtime == g.MTime && g.Status != gamedat.Unknown {
					g.ID = 0
					c.Games = append(c.Games, g)
					continue
				}
			}
		}
		c.Games = append(c.Games, identify(ix, facts, def, u, ix.Len() > 0))
	}
	return c, nil
}

// Parts are the other files a game's cue sheet (or .gdi, .ccd) names: its
// tracks. A game that's one file has none.
func Parts(p string) []string {
	if !sheet(p) {
		return nil
	}
	return sheetParts(p)
}

// sheetParts are the files on disk a sheet names, besides itself.
func sheetParts(f string) []string {
	var parts []string
	switch strings.ToLower(filepath.Ext(f)) {
	case ".cue", ".gdi":
		id, _ := gamedat.Probe(f)
		parts = id.Parts
	case ".ccd":
		stem := strings.TrimSuffix(f, filepath.Ext(f))
		for _, ext := range []string{".img", ".sub"} {
			parts = append(parts, stem+ext)
		}
	}
	return slices.DeleteFunc(parts, func(x string) bool { _, err := os.Stat(x); return err != nil || x == f })
}
