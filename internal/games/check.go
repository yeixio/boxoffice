package games

import (
	"archive/zip"
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/store"
)

// Progress is the last check: how far it got, and what it found.
type Progress struct {
	Running bool `json:"running"`
	// Step is what it's doing: finding (looking through the root folders
	// for each system's folder), list (downloading a game list), reading
	// (reading a system's files), or checking (checking them against the
	// list). System is the system it's on.
	Step   string `json:"step,omitempty"`
	System string `json:"system,omitempty"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	// Systems and Games count what it found.
	Systems int `json:"systems"`
	Games   int `json:"games"`
	// Unsure are folders in a games root folder it couldn't tell the
	// system of.
	Unsure   []string  `json:"unsure"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// Progress gives the last check's progress.
func (s *Service) Progress() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.progress
	p.Unsure = slices.Clone(p.Unsure)
	if p.Unsure == nil {
		p.Unsure = []string{}
	}
	return p
}

func (s *Service) set(f func(p *Progress)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.progress)
}

// checkTimeout bounds one check, however big the collection.
const checkTimeout = 6 * time.Hour

// Start checks the games root folders in the background: one root folder
// (rootID), or all of them (0); only one system's folders (system), or
// every system's (""). It finds each system's folder, reads its games and
// checks them against the system's list. Systems set to Do not touch are
// left as they are.
func (s *Service) Start(ctx context.Context, rootID int, system string) (Progress, error) {
	if err := s.on(ctx); err != nil {
		return Progress{}, err
	}
	s.mu.Lock()
	if s.progress.Running {
		s.mu.Unlock()
		return Progress{}, ErrBusy
	}
	s.progress = Progress{Running: true, Step: "finding", Started: time.Now().UTC(), Unsure: []string{}}
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		err := s.check(ctx, rootID, system)
		s.set(func(p *Progress) {
			p.Running, p.Step, p.System = false, "", ""
			p.Finished = time.Now().UTC()
			if err != nil {
				p.Error = err.Error()
			}
		})
		if err != nil {
			s.log().Warn("checking games", "err", err)
		}
		s.startQueued()
	}()
	return s.Progress(), nil
}

// CheckSoon checks one system's games (all of them for "") now, or as
// soon as the check running finishes, as after a game is moved in.
func (s *Service) CheckSoon(ctx context.Context, system string) error {
	_, err := s.Start(ctx, 0, system)
	if !errors.Is(err, ErrBusy) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued == nil {
		s.queued = map[string]bool{}
	}
	s.queued[system] = true
	return nil
}

// startQueued starts the check asked for while another ran.
func (s *Service) startQueued() {
	s.mu.Lock()
	q := s.queued
	s.queued = nil
	s.mu.Unlock()
	if len(q) == 0 {
		return
	}
	system := ""
	if len(q) == 1 {
		for k := range q {
			system = k
		}
	}
	if _, err := s.Start(context.Background(), 0, system); err != nil {
		s.log().Warn("checking games", "err", err)
	}
}

// Run checks every games root folder a few minutes after Box Office
// starts if it hasn't been for a week, and weekly after that, to pick up
// games added and see sets changed.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		t.Reset(6 * time.Hour)
		if s.on(ctx) != nil {
			continue
		}
		systems, err := s.Store.GameSystems(ctx)
		if err != nil {
			continue
		}
		roots, _ := s.Store.RootFolders(ctx, mediatypes.Game)
		due := len(systems) == 0 && len(roots) > 0
		for _, sys := range systems {
			if !sys.Locked && time.Since(sys.CheckedAt) > 7*24*time.Hour {
				due = true
			}
		}
		if due {
			if _, err := s.Start(ctx, 0, ""); err != nil && !errors.Is(err, ErrBusy) {
				s.log().Warn("checking games", "err", err)
			}
		}
	}
}

// found is a system's folder found in a root folder.
type found struct {
	path string
	sys  gamedat.System
}

func (s *Service) check(ctx context.Context, rootID int, only string) error {
	roots, err := s.Store.RootFolders(ctx, mediatypes.Game)
	if err != nil {
		return err
	}
	// Folders of root folders no longer there are forgotten.
	known, err := s.Store.GameSystems(ctx)
	if err != nil {
		return err
	}
	for _, k := range known {
		if !slices.ContainsFunc(roots, func(r store.RootFolder) bool { return r.Path == k.RootFolder }) {
			s.Store.DeleteGameSystem(ctx, k.ID)
		}
	}
	var folders []found
	var unsure []string
	for _, r := range roots {
		if rootID != 0 && r.ID != rootID {
			continue
		}
		f, u, err := Detect(r.Path)
		if err != nil {
			s.log().Warn("looking through a games root folder", "path", r.Path, "err", err)
			continue
		}
		unsure = append(unsure, u...)
		// Folders found before but gone now are forgotten too.
		for _, k := range known {
			if k.RootFolder == r.Path && !slices.ContainsFunc(f, func(x found) bool { return x.path == k.Path }) {
				if _, err := os.Stat(k.Path); errors.Is(err, os.ErrNotExist) {
					s.Store.DeleteGameSystem(ctx, k.ID)
				}
			}
		}
		for _, x := range f {
			sys, err := s.Store.GameSystemByPath(ctx, x.path)
			if errors.Is(err, store.ErrNotFound) {
				sys, err = s.Store.AddGameSystem(ctx, store.GameSystem{System: x.sys.Key, Path: x.path, RootFolder: r.Path})
			} else if err == nil && sys.System != x.sys.Key {
				err = s.Store.SetGameSystem(ctx, sys.ID, x.sys.Key)
			}
			if err != nil {
				return err
			}
			folders = append(folders, x)
		}
	}
	s.set(func(p *Progress) { p.Unsure = unsure })
	var failed []string
	for _, x := range folders {
		if only != "" && x.sys.Key != only {
			continue
		}
		sys, err := s.Store.GameSystemByPath(ctx, x.path)
		if err != nil {
			return err
		}
		if sys.Locked {
			continue
		}
		s.set(func(p *Progress) { p.Systems++; p.System, p.Step, p.Done, p.Total = x.sys.Name, "reading", 0, 0 })
		var c store.GameCheck
		switch {
		case x.sys.Arcade:
			c, err = s.checkMAME(ctx, sys)
		case x.sys.Hash:
			c, err = s.checkConsole(ctx, sys, x.sys)
		default:
			c, err = s.checkDiscs(ctx, sys, x.sys)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log().Warn("checking a system's games", "system", x.sys.Name, "path", x.path, "err", err)
			s.Store.GameSystemNote(ctx, sys.ID, err.Error())
			failed = append(failed, x.sys.Name)
			continue
		}
		if err := s.Store.SaveGameCheck(ctx, sys.ID, c); err != nil {
			return err
		}
		// Games wanted that are here now are wanted no longer.
		if n, err := s.Store.ForgetGamesOnDisk(ctx, x.sys.Key); err != nil {
			s.log().Warn("forgetting games wanted that are on disk", "system", x.sys.Name, "err", err)
		} else if n > 0 {
			s.log().Info("games wanted are on disk now", "system", x.sys.Name, "games", n)
		}
		s.set(func(p *Progress) { p.Games += len(c.Games) })
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't check %s: see the system for why", strings.Join(failed, ", "))
	}
	return nil
}

// Detect finds each system's folder in a games root folder: the root
// folder itself when its games lie in it, or the folders in it, by their
// names (snes, mame, "Nintendo - Super Nintendo Entertainment System") or
// else by the games in them. unsure are folders it couldn't tell.
func Detect(root string) (systems []found, unsure []string, err error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	if hasGames(entries) {
		if sys, ok := systemOf(root, entries); ok {
			return []found{{root, sys}}, nil, nil
		}
		return nil, []string{root}, nil
	}
	var look func(dir string, entries []os.DirEntry, depth int)
	look = func(dir string, entries []os.DirEntry, depth int) {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			sub, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			if sys, ok := gamedat.SystemByFolder(e.Name()); ok {
				systems = append(systems, found{p, sys})
				continue
			}
			if hasGames(sub) || hasGameFolders(p, sub) {
				if sys, ok := systemOf(p, sub); ok {
					systems = append(systems, found{p, sys})
				} else {
					unsure = append(unsure, p)
				}
				continue
			}
			// A folder of folders, such as roms/, one more level down.
			if depth == 0 {
				look(p, sub, 1)
			}
		}
	}
	look(root, entries, 0)
	return systems, unsure, nil
}

func hasGames(entries []os.DirEntry) bool {
	for _, e := range entries {
		if !e.IsDir() && gamedat.IsGameFile(e.Name()) {
			return true
		}
	}
	return false
}

// hasGameFolders says whether a few of a folder's folders are games kept
// as folders, such as PlayStation 3 games dumped whole.
func hasGameFolders(dir string, entries []os.DirEntry) bool {
	n := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if n++; n > 10 {
			break
		}
		if id, err := gamedat.Probe(filepath.Join(dir, e.Name())); err == nil && id.System != "" {
			return true
		}
	}
	return false
}

// systemOf tells a folder's system by its name, or else by its games:
// files by their extension, or by what's written in them (a disc's
// serial, a download's header), folders by what's in them, and archives
// by what's in a few of them. Archives holding no console's files are
// MAME's sets.
func systemOf(dir string, entries []os.DirEntry) (gamedat.System, bool) {
	if sys, ok := gamedat.SystemByFolder(filepath.Base(dir)); ok {
		return sys, true
	}
	count := map[string]int{}
	archives, peeked, probed := 0, 0, 0
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if probed < 20 && !strings.HasPrefix(e.Name(), ".") {
				probed++
				if id, err := gamedat.Probe(p); err == nil && id.System != "" {
					count[id.System]++
				}
			}
			continue
		}
		if sys, ok := gamedat.SystemByExt(e.Name()); ok {
			count[sys.Key]++
			continue
		}
		if gamedat.SharedExt(e.Name()) || strings.EqualFold(filepath.Ext(e.Name()), ".cue") {
			if probed >= 20 {
				continue
			}
			probed++
			if sys, ok := Identify(p); ok {
				count[sys.Key]++
			}
			continue
		}
		if !gamedat.IsArchive(e.Name()) {
			continue
		}
		archives++
		if peeked >= 20 {
			continue
		}
		peeked++
		files, err := gamedat.ListArchive(p)
		if err != nil {
			continue
		}
		key := "mame"
		for _, f := range files {
			if sys, ok := gamedat.SystemByExt(f.Name); ok {
				key = sys.Key
				break
			}
		}
		count[key]++
	}
	best, n := "", 0
	for k, c := range count {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	if best == "" {
		return gamedat.System{}, false
	}
	return gamedat.LookupSystem(best)
}

// MAME.

// checkMAME reads a MAME folder's sets and checks them against MAME's
// list: each archive named for a set, and each folder of CHDs.
func (s *Service) checkMAME(ctx context.Context, sys store.GameSystem) (store.GameCheck, error) {
	entries, err := os.ReadDir(sys.Path)
	if err != nil {
		return store.GameCheck{}, err
	}
	sets := map[string]*gamedat.Set{}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			chds := chdsIn(filepath.Join(sys.Path, e.Name()))
			if len(chds) == 0 {
				continue
			}
			set := sets[e.Name()]
			if set == nil {
				set = &gamedat.Set{Name: e.Name()}
				sets[e.Name()] = set
				names = append(names, e.Name())
			}
			set.CHDs = chds
			continue
		}
		if !gamedat.IsArchive(e.Name()) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if set, ok := sets[name]; ok && set.Path != "" {
			continue // pacman.zip and pacman.7z: the first is checked
		}
		set := sets[name]
		if set == nil {
			set = &gamedat.Set{Name: name}
			sets[name] = set
			names = append(names, name)
		}
		set.Path = filepath.Join(sys.Path, e.Name())
	}
	s.set(func(p *Progress) { p.Total = len(names) })
	for i, n := range names {
		if ctx.Err() != nil {
			return store.GameCheck{}, ctx.Err()
		}
		set := sets[n]
		if set.Path != "" {
			if st, err := os.Stat(set.Path); err == nil {
				set.Size = st.Size()
			}
			set.Files, set.Err = gamedat.ListArchive(set.Path)
		}
		if i%50 == 0 {
			s.set(func(p *Progress) { p.Done = i })
		}
	}
	version, err := s.MAMEVersion(ctx)
	if err != nil {
		return store.GameCheck{}, err
	}
	s.set(func(p *Progress) { p.Step = "list" })
	listPath, err := s.mameList(ctx, version)
	if err != nil {
		return store.GameCheck{}, err
	}
	s.set(func(p *Progress) { p.Step = "checking" })
	list, err := gamedat.LoadMAME(func() (io.ReadCloser, error) { return openZipped(listPath) }, names)
	if err != nil {
		return store.GameCheck{}, fmt.Errorf("couldn't read MAME %s's list: %w", version, err)
	}
	result := gamedat.CheckMAME(list, sets, gamedat.CHDSHA1)
	genres := readCatver(sys.Path, sys.RootFolder)
	c := store.GameCheck{Format: result.Format, List: "MAME " + cmp.Or(list.Version, version), Support: len(result.Support)}
	for _, g := range result.Games {
		m := g.Machine
		parent := g.Parent()
		parentTitle := m.Description
		if pm, ok := list.Machines[parent]; ok {
			parentTitle = pm.Description
		}
		game := store.Game{
			Name: m.Name, Title: m.Description, Parent: parent, ParentTitle: parentTitle,
			Year: m.Year, Maker: m.Manufacturer, Players: m.Players,
			Genre: cmp.Or(genres[m.Name], genres[parent]), InParent: g.InParent, Status: g.Status,
			Art: gamedat.ThumbName(m.Description),
		}
		if g.Set != nil {
			game.Path, game.Size = g.Set.Path, g.Set.Size
			if game.Path == "" {
				game.Path = filepath.Join(sys.Path, g.Set.Name)
			}
			if !g.InParent {
				for _, p := range g.Set.CHDs {
					if st, err := os.Stat(p); err == nil {
						game.Size += st.Size()
					}
				}
			}
		}
		for _, p := range g.Problems {
			game.Problems = append(game.Problems, store.GameProblem{Kind: p.Kind, Name: p.Name})
		}
		c.Games = append(c.Games, game)
	}
	return c, nil
}

// chdsIn lists the CHD files in a set's folder, by disk name.
func chdsIn(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out map[string]string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".chd") {
			if out == nil {
				out = map[string]string{}
			}
			out[strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))] = filepath.Join(dir, e.Name())
		}
	}
	return out
}

// openZipped opens the one file in a zip.
func openZipped(p string) (io.ReadCloser, error) {
	z, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	for _, f := range z.File {
		if !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				z.Close()
				return nil, err
			}
			return struct {
				io.Reader
				io.Closer
			}{rc, closers{rc, z}}, nil
		}
	}
	z.Close()
	return nil, errors.New("the list's zip is empty")
}

type closers []io.Closer

func (c closers) Close() error {
	var errs []error
	for _, x := range c {
		errs = append(errs, x.Close())
	}
	return errors.Join(errs...)
}

// readCatver reads genres from a catver.ini next to the sets or in the
// root folder, if there's one: MAME's list has none of its own. "Maze /
// Collect" is Maze.
func readCatver(dirs ...string) map[string]string {
	out := map[string]string{}
	for _, d := range dirs {
		f, err := os.Open(filepath.Join(d, "catver.ini"))
		if err != nil {
			continue
		}
		defer f.Close()
		in := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "[") {
				in = strings.EqualFold(line, "[Category]")
				continue
			}
			if !in {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.TrimSpace(strings.ReplaceAll(v, "* Mature *", ""))
			if main, _, ok := strings.Cut(v, " / "); ok {
				v = main
			}
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		return out
	}
	return out
}

// Consoles.

// checkConsole reads a console folder's games, matches each by hash to
// No-Intro's list, and fills in what libretro's database knows about it.
func (s *Service) checkConsole(ctx context.Context, sys store.GameSystem, def gamedat.System) (store.GameCheck, error) {
	s.set(func(p *Progress) { p.Step = "list" })
	ix, version, err := s.noIntro(ctx, def)
	if err != nil {
		return store.GameCheck{}, err
	}
	facts := s.facts(ctx, def)
	s.set(func(p *Progress) { p.Step = "reading" })
	before, err := s.Store.Games(ctx, sys.ID)
	if err != nil {
		return store.GameCheck{}, err
	}
	type was struct {
		size, mtime int64
		crc         string
	}
	// A loose file unchanged since the last check isn't read again.
	prev := map[string]was{}
	for _, g := range before {
		if g.CRC != "" && !gamedat.IsArchive(g.Path) {
			prev[g.Path] = was{g.Size, g.MTime, g.CRC}
		}
	}
	var files []string
	filepath.WalkDir(sys.Path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != sys.Path {
				return filepath.SkipDir
			}
			// Three folders down is as deep as a system's games go.
			if rel, _ := filepath.Rel(sys.Path, p); strings.Count(rel, string(filepath.Separator)) >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if gamedat.IsArchive(d.Name()) || def.Takes(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	s.set(func(p *Progress) { p.Total = len(files) })
	c := store.GameCheck{List: "No-Intro " + version}
	for i, p := range files {
		if ctx.Err() != nil {
			return store.GameCheck{}, ctx.Err()
		}
		if i%25 == 0 {
			s.set(func(pr *Progress) { pr.Done = i })
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		type part struct {
			name  string // inside an archive, or ""
			size  int64
			crcs  []uint32
			error error
		}
		var parts []part
		if gamedat.IsArchive(p) {
			inside, err := gamedat.ListArchive(p)
			if err != nil {
				parts = append(parts, part{error: err})
			}
			for _, f := range inside {
				if !def.Takes(f.Name) && len(inside) > 1 {
					continue
				}
				pt := part{name: f.Name, size: f.Size, crcs: []uint32{f.CRC}}
				// A file with a header has to be read to hash it without.
				if _, _, ok := ix.ByCRC(pt.crcs, f.Size); !ok && def.Header != gamedat.NoHeader {
					if rc, err := gamedat.OpenInArchive(p, f.Name); err == nil {
						if more, err := gamedat.CRCs(rc, f.Size, def.Header); err == nil {
							pt.crcs = append(pt.crcs, more[1:]...)
						}
						rc.Close()
					}
				}
				parts = append(parts, pt)
			}
		} else {
			pt := part{size: st.Size()}
			if w, ok := prev[p]; ok && w.size == st.Size() && w.mtime == st.ModTime().Unix() {
				if c, err := strconv.ParseUint(w.crc, 16, 32); err == nil {
					pt.crcs, pt.size = []uint32{uint32(c)}, 0
				}
			}
			if pt.crcs == nil {
				if st.Size() > gamedat.MaxHashSize {
					pt.error = errors.New("too big to check")
				} else if f, err := os.Open(p); err != nil {
					pt.error = err
				} else {
					pt.crcs, pt.error = gamedat.CRCs(f, st.Size(), def.Header)
					f.Close()
				}
			}
			parts = append(parts, pt)
		}
		for _, pt := range parts {
			base := filepath.Base(p)
			if pt.name != "" {
				base = filepath.Base(pt.name)
			}
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			g := store.Game{Path: p, Size: st.Size(), MTime: st.ModTime().Unix()}
			if pt.name != "" {
				g.Name = pt.name
			}
			if pt.error != nil {
				g.Name, g.Title, g.Status = cmp.Or(g.Name, stem), gamedat.Title(stem), gamedat.Unreadable
				g.Problems = []store.GameProblem{{Kind: gamedat.ProblemUnreadable, Name: pt.error.Error()}}
			} else if e, crc, ok := ix.ByCRC(pt.crcs, pt.size); ok {
				g.Name, g.Title, g.Status = e.Name(), gamedat.Title(e.Name()), gamedat.Complete
				g.CRC = fmt.Sprintf("%08X", crc)
				g.Art = gamedat.ThumbName(e.Name())
				if f := facts[crc]; f != nil {
					g.Year, g.Genre, g.Players = f.Year, f.Genre, f.Players
					g.Maker = cmp.Or(f.Publisher, f.Developer)
				}
			} else {
				g.Name, g.Title = stem, gamedat.Title(stem)
				if len(pt.crcs) > 0 {
					g.CRC = fmt.Sprintf("%08X", pt.crcs[len(pt.crcs)-1])
				}
				if e, ok := ix.ByName(stem); ok {
					// Named like a game on the list, but not the same:
					// a bad copy, or one changed since.
					g.Name, g.Title, g.Status = e.Name(), gamedat.Title(e.Name()), gamedat.Wrong
					g.Art = gamedat.ThumbName(e.Name())
					g.Problems = []store.GameProblem{{Kind: gamedat.ProblemWrong, Name: base}}
					if len(e.Roms) > 0 {
						if f := facts[e.Roms[0].CRC]; f != nil {
							g.Year, g.Genre, g.Players = f.Year, f.Genre, f.Players
							g.Maker = cmp.Or(f.Publisher, f.Developer)
						}
					}
				} else {
					g.Status = gamedat.Unknown
					g.Art = gamedat.ThumbName(stem)
					if e, ok := ix.ByTitle(stem); ok {
						// Not a version on the list, but the same game: its
						// picture will do.
						g.Art = gamedat.ThumbName(e.Name())
					}
				}
			}
			g.Parent, g.ParentTitle = groupKey(g.Title), g.Title
			if len(parts) > 1 {
				// A merged archive (GoodMerged, 1G1R packs) holds one
				// game's every version, hacks and translations: they're
				// that game's, named for the archive.
				t := gamedat.Title(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)))
				g.Parent, g.ParentTitle = groupKey(t), t
			}
			c.Games = append(c.Games, g)
		}
	}
	return c, nil
}

// groupKey puts a console game's versions together: the same title in
// any region or revision.
func groupKey(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, title)
}

// facts are what libretro's database knows about a console's games, by
// CRC. Any part it can't get is left out.
func (s *Service) facts(ctx context.Context, sys gamedat.System) gamedat.FactIndex {
	fi := gamedat.FactIndex{}
	for _, kind := range []string{"releaseyear", "publisher", "developer", "genre", "maxusers"} {
		p, err := s.libretroDAT(ctx, kind, sys.Libretro)
		if err != nil {
			s.log().Warn("couldn't download a game list", "list", kind+"/"+sys.Libretro, "err", err)
			continue
		}
		if p == "" {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		dat, err := gamedat.ParseClrMame(f)
		f.Close()
		if err != nil {
			s.log().Warn("couldn't read a game list", "list", kind+"/"+sys.Libretro, "err", err)
			continue
		}
		fi.Add(dat)
	}
	return fi
}
