package games

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/store"
)

// ErrNotWanted means a download's game is no longer wanted.
var ErrNotWanted = errors.New("the game it was for is no longer wanted")

// found is one game file in a download that's the game wanted.
type foundGame struct {
	path   string // the file, or the archive holding it
	inside string // its name inside the archive, or ""
	name   string // its name on the system's list
	ext    string // the game file's extension, such as .sfc
	exact  bool   // the very version wanted
	alone  bool   // the archive holds no other game file
}

// MatchDownload says whether a finished download holds the game wanted:
// a console game is matched by hash to No-Intro's list, any version of
// the same title doing; a MAME set by its name and its files' CRCs. Why
// is what's wrong when it doesn't.
func (s *Service) MatchDownload(ctx context.Context, wantID int64, dir string, files []string) (why string, err error) {
	w, err := s.Store.GameWant(ctx, wantID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotWanted
	}
	if err != nil {
		return "", err
	}
	sys, ok := gamedat.LookupSystem(w.System)
	if !ok {
		return "", fmt.Errorf("Box Office doesn't know a system called %q", w.System)
	}
	if sys.Arcade {
		sets, why, err := s.findSet(ctx, w, dir, files)
		if err != nil || len(sets) == 0 {
			return why, err
		}
		return "", nil
	}
	if sys.Disc() {
		_, why, err := s.findDisc(ctx, w, sys, dir)
		return why, err
	}
	got, checked, err := s.findConsole(ctx, w, sys, dir, files)
	if err != nil {
		return "", err
	}
	if len(got) == 0 {
		return notIt(w, sys, checked), nil
	}
	return "", nil
}

func notIt(w store.GameWant, sys gamedat.System, checked int) string {
	if checked == 0 {
		return fmt.Sprintf("there's no %s game in it", sys.Name)
	}
	return fmt.Sprintf("none of the %d %s games in it is %s, going by No-Intro's list", checked, sys.Short, w.Title)
}

// findConsole hashes a download's game files for a console and keeps the
// ones that are the game wanted, the version wanted first.
func (s *Service) findConsole(ctx context.Context, w store.GameWant, sys gamedat.System, dir string, files []string) ([]foundGame, int, error) {
	ix, _, err := s.noIntro(ctx, sys)
	if err != nil {
		return nil, 0, err
	}
	match := func(crcs []uint32, size int64) (string, bool, bool) {
		e, _, ok := ix.ByCRC(crcs, size)
		if !ok {
			return "", false, false
		}
		if e.Name() == w.Name {
			return e.Name(), true, true
		}
		if w.Group != "" && gamedat.TitleKey(gamedat.Title(e.Name())) == w.Group {
			return e.Name(), false, true
		}
		return "", false, false
	}
	var out []foundGame
	checked := 0
	for _, f := range files {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		p := filepath.Join(dir, f)
		ext := strings.ToLower(filepath.Ext(f))
		switch {
		case sys.Takes(f):
			st, err := os.Stat(p)
			if err != nil || st.Size() > gamedat.MaxHashSize {
				continue
			}
			checked++
			fh, err := os.Open(p)
			if err != nil {
				continue
			}
			crcs, err := gamedat.CRCs(fh, st.Size(), sys.Header)
			fh.Close()
			if err != nil {
				continue
			}
			if name, exact, ok := match(crcs, st.Size()); ok {
				out = append(out, foundGame{path: p, name: name, ext: ext, exact: exact, alone: true})
			}
		case gamedat.IsArchive(f):
			inside, err := gamedat.ListArchive(p)
			if err != nil {
				continue
			}
			var games []gamedat.File
			for _, x := range inside {
				if sys.Takes(x.Name) {
					games = append(games, x)
				}
			}
			for _, x := range games {
				checked++
				crcs := []uint32{x.CRC}
				name, exact, ok := match(crcs, x.Size)
				if !ok && sys.Header != gamedat.NoHeader {
					if rc, err := gamedat.OpenInArchive(p, x.Name); err == nil {
						if more, err := gamedat.CRCs(rc, x.Size, sys.Header); err == nil {
							name, exact, ok = match(more, 0)
						}
						rc.Close()
					}
				}
				if ok {
					out = append(out, foundGame{path: p, inside: x.Name, name: name, ext: strings.ToLower(filepath.Ext(x.Name)), exact: exact, alone: len(games) == 1})
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b foundGame) int {
		switch {
		case a.exact != b.exact:
			if a.exact {
				return -1
			}
			return 1
		case gamedat.BetterVersion(a.name, b.name):
			return -1
		case gamedat.BetterVersion(b.name, a.name):
			return 1
		}
		return 0
	})
	return out, checked, nil
}

// findSet finds a MAME set in a download: the archive named for it (and
// its CHD folder), with at least one file of the set as MAME lists it.
// Sets it leans on that came with it, such as its parent or BIOS, are
// given too, after it.
func (s *Service) findSet(ctx context.Context, w store.GameWant, dir string, files []string) ([]string, string, error) {
	byName := map[string]string{} // set name -> archive or CHD folder
	for _, f := range files {
		base := filepath.Base(f)
		stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
		switch {
		case gamedat.IsArchive(base):
			if _, ok := byName[stem]; !ok {
				byName[stem] = filepath.Join(dir, f)
			}
		case strings.EqualFold(filepath.Ext(base), ".chd"):
			folder := strings.ToLower(filepath.Base(filepath.Dir(f)))
			if _, ok := byName[folder+"/"]; !ok && folder != "." {
				byName[folder+"/"] = filepath.Join(dir, filepath.Dir(f))
			}
		}
	}
	main, ok := byName[strings.ToLower(w.Name)]
	if !ok {
		return nil, fmt.Sprintf("there's no %s set in it (looked for %s.zip or %s.7z)", w.Name, w.Name, w.Name), nil
	}
	version, err := s.MAMEVersion(ctx)
	if err != nil {
		return nil, "", err
	}
	listPath, err := s.mameList(ctx, version)
	if err != nil {
		return nil, "", err
	}
	list, err := gamedat.LoadMAME(func() (io.ReadCloser, error) { return openZipped(listPath) }, []string{w.Name})
	if err != nil {
		return nil, "", fmt.Errorf("couldn't read MAME %s's list: %w", version, err)
	}
	m, ok := list.Machines[w.Name]
	if !ok {
		return nil, fmt.Sprintf("MAME %s has no set called %s", version, w.Name), nil
	}
	known := map[uint32]bool{}
	for _, r := range m.Roms {
		if r.HasCRC {
			known[r.CRC] = true
		}
	}
	inside, err := gamedat.ListArchive(main)
	if err != nil {
		return nil, fmt.Sprintf("%s can't be read: %v", filepath.Base(main), err), nil
	}
	hits := 0
	for _, f := range inside {
		if known[f.CRC] {
			hits++
		}
	}
	if hits == 0 && len(known) > 0 {
		return nil, fmt.Sprintf("%s has none of the files MAME %s lists for %s", filepath.Base(main), version, w.Name), nil
	}
	out := []string{main}
	if chd, ok := byName[strings.ToLower(w.Name)+"/"]; ok {
		out = append(out, chd)
	}
	// Its parent, BIOS and devices, where they came with it.
	seen := map[string]bool{w.Name: true}
	var lean func(string)
	lean = func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		if p, ok := byName[strings.ToLower(n)]; ok {
			out = append(out, p)
		}
		if lm, ok := list.Machines[n]; ok {
			lean(lm.CloneOf)
			lean(lm.RomOf)
		}
	}
	lean(m.CloneOf)
	lean(m.RomOf)
	for _, d := range m.Devices {
		if dm, ok := list.Machines[d]; ok && dm.HasRoms {
			lean(d)
		}
	}
	return out, "", nil
}

// ImportDownload copies the game wanted from a finished download into
// its system's folder, named as its system's list names it, and checks
// the system again. A MAME set brings along the sets it leans on that
// came with it, where they aren't there already. Nothing in the folder is
// replaced.
func (s *Service) ImportDownload(ctx context.Context, wantID int64, dir string, files []string) (string, error) {
	if err := s.on(ctx); err != nil {
		return "", err
	}
	w, err := s.Store.GameWant(ctx, wantID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotWanted
	}
	if err != nil {
		return "", err
	}
	if err := s.Store.CheckUnlocked(ctx, store.GameKind, w.ID); err != nil {
		return "", err
	}
	sys, ok := gamedat.LookupSystem(w.System)
	if !ok {
		return "", fmt.Errorf("Box Office doesn't know a system called %q", w.System)
	}
	var to string
	switch {
	case sys.Arcade:
		to, err = s.importSet(ctx, w, dir, files)
	case sys.Disc():
		to, err = s.importDisc(ctx, w, sys, dir)
	default:
		to, err = s.importConsole(ctx, w, sys, dir, files)
	}
	if err != nil {
		return "", err
	}
	if err := s.Store.GameWantGot(ctx, w.ID); err != nil {
		s.log().Warn("noting a game moved in", "game", w.Name, "err", err)
	}
	if err := s.CheckSoon(context.WithoutCancel(ctx), w.System); err != nil {
		s.log().Warn("checking games after one moved in", "system", sys.Name, "err", err)
	}
	return to, nil
}

func (s *Service) importConsole(ctx context.Context, w store.GameWant, sys gamedat.System, dir string, files []string) (string, error) {
	got, checked, err := s.findConsole(ctx, w, sys, dir, files)
	if err != nil {
		return "", err
	}
	if len(got) == 0 {
		return "", errors.New(notIt(w, sys, checked))
	}
	g := got[0]
	src, name := g.path, g.name+g.ext
	switch {
	case g.inside == "":
	case g.alone:
		// An archive of the one game goes in as it is.
		name = g.name + strings.ToLower(filepath.Ext(g.path))
	default:
		// A pack of games: only the one wanted comes out.
		tmp, err := extract(g.path, g.inside, dir)
		if err != nil {
			return "", fmt.Errorf("couldn't take %s out of %s: %w", filepath.Base(g.inside), filepath.Base(g.path), err)
		}
		defer os.Remove(tmp)
		src = tmp
	}
	dst, err := s.AddAs(ctx, sys.Key, src, name)
	if err != nil && !errors.Is(err, ErrHave) {
		return "", err
	}
	return dst, nil
}

// extract writes one file of an archive next to the download's files.
func extract(archive, inside, dir string) (string, error) {
	rc, err := gamedat.OpenInArchive(archive, inside)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	f, err := os.CreateTemp(dir, ".boxoffice-game-*"+strings.ToLower(filepath.Ext(inside)))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(rc, gamedat.MaxHashSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (s *Service) importSet(ctx context.Context, w store.GameWant, dir string, files []string) (string, error) {
	sets, why, err := s.findSet(ctx, w, dir, files)
	if err != nil {
		return "", err
	}
	if len(sets) == 0 {
		return "", errors.New(why)
	}
	var first string
	for i, p := range sets {
		st, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		var placed string
		if st.IsDir() {
			// A set's CHDs go in the folder named for it.
			entries, _ := os.ReadDir(p)
			for _, e := range entries {
				if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".chd") {
					continue
				}
				placed, err = s.addInto(ctx, "mame", filepath.Join(p, e.Name()), w.Name, e.Name())
				if err != nil && !errors.Is(err, ErrHave) {
					return "", err
				}
			}
		} else {
			name := filepath.Base(p)
			if i == 0 {
				name = w.Name + strings.ToLower(filepath.Ext(p))
			}
			placed, err = s.AddAs(ctx, "mame", p, name)
			if i > 0 && errors.Is(err, ErrHave) {
				continue
			}
			if err != nil && !errors.Is(err, ErrHave) && i == 0 {
				return "", err
			}
			if err != nil && i > 0 {
				// Another copy of a parent or BIOS is there already: leave it.
				s.log().Info("left a set that came with a game", "set", name, "why", err)
				continue
			}
		}
		if first == "" {
			first = placed
		}
	}
	return first, nil
}

// discFound is a game in a download that's the one wanted.
type discFound struct {
	unit  unit
	name  string // its name on the system's list, or the name wanted
	exact bool   // the very version wanted
}

// findDisc finds the game wanted in a download for a disc or download
// system: each game in it is known by the serial or title ID written in
// it, or the name written in it or on it, and must be a version of the
// game wanted. A PC game's download is the game whole.
func (s *Service) findDisc(ctx context.Context, w store.GameWant, sys gamedat.System, dir string) ([]discFound, string, error) {
	ix, _, err := s.listIndex(ctx, sys)
	if err != nil {
		return nil, "", err
	}
	var units []unit
	if folderPerGame(sys) {
		units = []unit{{path: dir, dir: true}}
	} else {
		units = findUnits(dir, sys)
	}
	var out []discFound
	var whys []string
	for _, u := range units {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		stem := stemOf(u)
		if u.path == dir {
			stem = w.Title
		}
		id, err := gamedat.Probe(u.path)
		if err != nil && !errors.Is(err, gamedat.ErrUnknown) {
			whys = append(whys, fmt.Sprintf("%s can't be read: %v", filepath.Base(u.path), err))
			continue
		}
		switch {
		case id.System != "" && id.System != sys.Key && !(sys.Key == "psp" && id.System == "psx"):
			whys = append(whys, fmt.Sprintf("%s is a %s game", filepath.Base(u.path), systemName(id.System)))
			continue
		case id.Update:
			whys = append(whys, fmt.Sprintf("%s is an update, not the game", filepath.Base(u.path)))
			continue
		case id.DLC:
			whys = append(whys, fmt.Sprintf("%s is added content, not the game", filepath.Base(u.path)))
			continue
		}
		if e := match(ix, id, stem); e != nil {
			if e.Name() == w.Name || w.Serial != "" && id.Serial != "" && sameSerial(w.Serial, id.Serial) {
				out = append(out, discFound{unit: u, name: e.Name(), exact: true})
			} else if w.Group != "" && gamedat.TitleKey(gamedat.Title(e.Name())) == w.Group {
				out = append(out, discFound{unit: u, name: e.Name()})
			} else {
				whys = append(whys, fmt.Sprintf("%s is %s", filepath.Base(u.path), gamedat.Title(e.Name())))
			}
			continue
		}
		if ix.Len() > 0 {
			whys = append(whys, fmt.Sprintf("%s isn't on %s's list", filepath.Base(u.path), sys.Name))
			continue
		}
		// No list: the name in it, or on it, must be the game's.
		for _, t := range []string{id.Title, TitleFromName(stem)} {
			if t != "" && w.Group != "" && strings.HasPrefix(gamedat.TitleKey(t), w.Group) {
				out = append(out, discFound{unit: u, name: w.Name, exact: true})
				break
			}
		}
	}
	if len(out) == 0 {
		switch {
		case len(units) == 0:
			return nil, fmt.Sprintf("there's no %s game in it", sys.Name), nil
		case len(whys) > 0:
			return nil, fmt.Sprintf("none of the %d %s games in it is %s: %s", len(units), sys.Short, w.Title, strings.Join(whys[:min(len(whys), 3)], "; ")), nil
		}
		return nil, fmt.Sprintf("none of the %d %s games in it is %s", len(units), sys.Short, w.Title), nil
	}
	slices.SortStableFunc(out, func(a, b discFound) int {
		switch {
		case a.exact != b.exact:
			if a.exact {
				return -1
			}
			return 1
		case gamedat.BetterVersion(a.name, b.name):
			return -1
		case gamedat.BetterVersion(b.name, a.name):
			return 1
		}
		return 0
	})
	return out, "", nil
}

func sameSerial(a, b string) bool {
	ka, kb := gamedat.SerialKeys(a), gamedat.SerialKeys(b)
	return slices.ContainsFunc(ka, func(k string) bool { return slices.Contains(kb, k) })
}

// importDisc copies the game wanted from a download into its system's
// folder, named as its list names it: a file as Name.ext, a cue sheet
// with its tracks in a folder of that name, a folder whole.
func (s *Service) importDisc(ctx context.Context, w store.GameWant, sys gamedat.System, dir string) (string, error) {
	got, why, err := s.findDisc(ctx, w, sys, dir)
	if err != nil {
		return "", err
	}
	if len(got) == 0 {
		return "", errors.New(why)
	}
	g := got[0]
	name := g.name
	if !g.unit.dir && len(g.unit.parts) == 0 {
		ext := strings.ToLower(filepath.Ext(g.unit.path))
		if strings.HasSuffix(strings.ToLower(g.unit.path), ".nkit.iso") {
			ext = ".nkit.iso"
		}
		name += ext
	}
	dst, err := s.placeUnit(ctx, sys.Key, g.unit, name)
	if err != nil && !errors.Is(err, ErrHave) {
		return "", err
	}
	return dst, nil
}
