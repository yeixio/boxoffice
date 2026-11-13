package games

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/mediatypes"
)

// ErrHave says a game being added is in its system's folder already.
var ErrHave = errors.New("it's in the games folder already")

// ErrClash says a different file of the same name is in the games folder:
// the game being added isn't put in, and what's there is left as it is.
var ErrClash = errors.New("a different file of that name is in the games folder")

// Identify says which system a file or folder is a game for: a file by
// its extension where only one system's games have it, or else by what's
// written in it (a disc's serial or header, a download's), an archive by
// what's in it, and a folder by what's in it (a PlayStation 3 game dumped
// whole). A CHD of a hard disk, and an archive holding no console's games
// and no video, music or books, is taken to be a MAME set.
func Identify(p string) (gamedat.System, bool) {
	name := filepath.Base(p)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		if id, err := gamedat.Probe(p); err == nil && id.System != "" {
			return gamedat.LookupSystem(id.System)
		}
		return gamedat.System{}, false
	}
	if sys, ok := gamedat.SystemByExt(name); ok {
		return sys, true
	}
	if !gamedat.IsArchive(name) {
		id, _ := gamedat.Probe(p)
		if id.System != "" {
			return gamedat.LookupSystem(id.System)
		}
		switch id.Disc {
		case "hd", "av":
			return gamedat.LookupSystem("mame")
		case "gd":
			// MAME's Naomi GD-ROMs are named for their sets: gdl-0001.
			if reSetName.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) {
				return gamedat.LookupSystem("mame")
			}
			return gamedat.LookupSystem("dreamcast")
		case "dvd":
			return gamedat.LookupSystem("ps2")
		case "cd":
			// A CD that MAME's arcade machines read, named for its set.
			if reSetName.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) {
				return gamedat.LookupSystem("mame")
			}
		}
		return gamedat.System{}, false
	}
	files, err := gamedat.ListArchive(p)
	if err != nil || len(files) == 0 {
		return gamedat.System{}, false
	}
	// A console game's zip holds the one game. An arcade set, named
	// short like MAME's, holds the board's many chips, and some of
	// those are named like a console's games (u42.int, ic1.col).
	if reSetName.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) && romCount(files) > 1 {
		return gamedat.LookupSystem("mame")
	}
	for _, f := range files {
		if sys, ok := gamedat.SystemByExt(f.Name); ok {
			return sys, true
		}
	}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".mkv", ".mp4", ".avi", ".m4v", ".mov", ".mp3", ".flac", ".m4a", ".m4b", ".ogg", ".epub", ".mobi", ".azw3", ".pdf", ".cbz", ".cbr",
			".rar", ".exe", ".iso", ".cue", ".txt", ".nfo", ".jpg", ".png", ".chd", ".gdi", ".cso", ".pbp":
			// A disc in an archive has to come out to be told. A .bin
			// alone isn't one: plenty of MAME sets name their ROMs .bin.
			return gamedat.System{}, false
		}
	}
	return gamedat.LookupSystem("mame")
}

// romCount is how many of an archive's files aren't notes or pictures.
func romCount(files []gamedat.File) int {
	n := 0
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".txt", ".nfo", ".diz", ".doc", ".pdf", ".htm", ".html", ".jpg", ".png", ".gif", ".url":
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		n++
	}
	return n
}

// reSetName is a name like MAME's sets have: short, lower case, no spaces.
var reSetName = regexp.MustCompile(`^[a-z0-9_-]{1,20}$`)

// Folder is the folder a system's games go in: the one Box Office already
// knows for it, or else a folder named for it in the first games root
// folder.
func (s *Service) Folder(ctx context.Context, key string) (string, error) {
	sys, ok := gamedat.LookupSystem(key)
	if !ok {
		return "", fmt.Errorf("Box Office doesn't know a system called %q", key)
	}
	known, err := s.Store.GameSystems(ctx)
	if err != nil {
		return "", err
	}
	for _, k := range known {
		if k.System == key && !k.Locked {
			return k.Path, nil
		}
	}
	for _, k := range known {
		if k.System == key {
			return "", fmt.Errorf("%s is set to Do not touch; turn that off to add games to it", sys.Name)
		}
	}
	roots, err := s.Store.RootFolders(ctx, mediatypes.Game)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", errors.New("add a games root folder in Settings first")
	}
	root := roots[0].Path
	// A root folder that's one system's folder can't take another's.
	if found, _, err := Detect(root); err == nil && len(found) == 1 && found[0].path == root {
		if found[0].sys.Key == key {
			return root, nil
		}
		return "", fmt.Errorf("the games root folder %s holds only %s games; add a games root folder with a folder for each system", root, found[0].sys.Name)
	}
	return filepath.Join(root, key), nil
}

// Add copies a game into its system's folder, and says where it went. A
// MAME CHD goes in the folder named for its set. The file it's copied
// from is left as it is.
func (s *Service) Add(ctx context.Context, key, src string) (string, error) {
	return s.AddAs(ctx, key, src, filepath.Base(src))
}

// AddAs is Add, naming the copy name.
func (s *Service) AddAs(ctx context.Context, key, src, name string) (string, error) {
	return s.addInto(ctx, key, src, "", name)
}

// cleanName makes a name from a list safe as a file's name.
func cleanName(name string) string {
	// Windows shares, where many keep their games, can't have : ? * and
	// the like in a name.
	name = strings.NewReplacer("/", "_", "\\", "_", "\x00", "", ": ", " - ", ":", "-", "?", "", "*", "", "<", "", ">", "", "|", "-", "\"", "'").Replace(name)
	name = strings.TrimRight(strings.TrimSpace(name), ".")
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// addInto is Add into a folder (sub) of the system's folder, naming the
// copy name. A MAME CHD with no folder given goes in the one named for it.
func (s *Service) addInto(ctx context.Context, key, src, sub, name string) (string, error) {
	dir, err := s.Folder(ctx, key)
	if err != nil {
		return "", err
	}
	name = cmp.Or(cleanName(name), filepath.Base(src))
	if sub = cleanName(sub); sub != "" {
		dir = filepath.Join(dir, sub)
	} else if key == "mame" && strings.EqualFold(filepath.Ext(name), ".chd") {
		dir = filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	dst := filepath.Join(dir, name)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	if have, err := os.Stat(dst); err == nil {
		if have.Size() == st.Size() {
			return dst, ErrHave
		}
		return dst, fmt.Errorf("another file called %s is in %s already: %w", name, dir, ErrClash)
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return "", writeErr(err, dir)
	}
	tmp := filepath.Join(dir, "."+name+".part")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return "", writeErr(err, dir)
	}
	_, err = io.Copy(out, readCtx{ctx, in})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return "", writeErr(err, dir)
	}
	return dst, nil
}

func writeErr(err error, dir string) error {
	if errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("Box Office can't write to %s: the games folder is read-only for it", dir)
	}
	return err
}

// readCtx stops a copy when its context ends.
type readCtx struct {
	ctx context.Context
	r   io.Reader
}

func (r readCtx) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// addFolder copies a folder that's one game into its system's folder,
// named name, whole or not at all. The folder it's copied from is left as
// it is.
func (s *Service) addFolder(ctx context.Context, key, src, name string) (string, error) {
	dir, err := s.Folder(ctx, key)
	if err != nil {
		return "", err
	}
	name = cmp.Or(cleanName(name), filepath.Base(src))
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return dst, ErrHave
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return "", writeErr(err, dir)
	}
	tmp := filepath.Join(dir, "."+name+".part")
	os.RemoveAll(tmp)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(tmp, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o775)
		case d.Type().IsRegular():
			return copyFile(ctx, p, to)
		}
		return nil // links and the like aren't brought along
	})
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.RemoveAll(tmp)
		return "", writeErr(err, dir)
	}
	return dst, nil
}

func copyFile(ctx context.Context, from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, readCtx{ctx, in})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// placeUnit copies one game into its system's folder, named name: a
// folder whole, a cue sheet and its tracks into a folder of their own
// (their names kept, as the sheet names them), and a file as it is.
func (s *Service) placeUnit(ctx context.Context, key string, u unit, name string) (string, error) {
	switch {
	case u.dir:
		return s.addFolder(ctx, key, u.path, name)
	case len(u.parts) > 0:
		sub := strings.TrimSuffix(name, filepath.Ext(name))
		first := ""
		for _, f := range u.files() {
			dst, err := s.addInto(ctx, key, f, sub, filepath.Base(f))
			if err != nil && !errors.Is(err, ErrHave) {
				return "", err
			}
			if first == "" {
				first = dst
			}
		}
		return first, nil
	}
	return s.AddAs(ctx, key, u.path, name)
}

// Skipped is a file ImportGames didn't put in: Clash is where a different
// file of its name is already; with none, Box Office couldn't tell the
// system it's for, or it isn't a game.
type Skipped struct {
	Path  string
	Clash string
}

// Placed is a game copied in, and the files it was copied from.
type Placed struct {
	To   string
	From []string
}

// ImportGames copies the games in a file or folder into their systems'
// folders. system is the system they're for, when it's known; otherwise
// each game is told by what it is. A folder that's one game (a
// PlayStation 3 game dumped whole, a PC game on several discs) goes in
// whole; a cue sheet goes in with its tracks. name, when given, is the
// game's name to file one game under (its name on the list, or one typed
// in). skipped are the files it couldn't tell the system of.
func (s *Service) ImportGames(ctx context.Context, system, p, name string) (placed []Placed, skipped []Skipped, err error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	var units []unit
	var emus []Emulator
	switch {
	case !st.IsDir():
		u := unit{path: p}
		u.parts = Parts(p)
		units = []unit{u}
	default:
		if id, err := gamedat.Probe(p); err == nil && id.System != "" {
			units, system = []unit{{path: p, dir: true}}, id.System
			break
		}
		if sys, ok := gamedat.LookupSystem(system); ok && folderPerGame(sys) {
			units = []unit{{path: p, dir: true}}
			break
		}
		units, system = anyUnits(p), ""
		emus = FindEmulators(p)
		// Files that are no game's are left, and said so.
		in := map[string]bool{}
		var dirs []string
		for _, u := range units {
			for _, f := range u.files() {
				in[f] = true
			}
			if u.dir {
				dirs = append(dirs, u.path+string(filepath.Separator))
			}
		}
		filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
			if err != nil || strings.HasPrefix(d.Name(), ".") && f != p {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() && f != p && emulatorFolder(d.Name()) {
				return filepath.SkipDir // an emulator's own files, not games
			}
			// Only what might be a game is worth a mention: an emulator's
			// programs, notes and settings, and a game's readme, aren't.
			if d.Type().IsRegular() && !in[f] && gamedat.IsGameFile(d.Name()) && !slices.ContainsFunc(dirs, func(x string) bool { return strings.HasPrefix(f, x) }) {
				skipped = append(skipped, Skipped{Path: f})
			}
			return nil
		})
	}
	for _, u := range units {
		key := system
		if e, ok := emulatorFor(emus, u.path); ok && key == "" && (u.dir || emulatorGame(u.path)) {
			key = e.System // a game in an emulator's roms folder is that emulator's
		}
		if key == "" {
			sys, ok := Identify(u.path)
			if !ok {
				for _, f := range u.files() {
					skipped = append(skipped, Skipped{Path: f})
				}
				continue
			}
			key = sys.Key
		}
		sys, ok := gamedat.LookupSystem(key)
		if !ok {
			return placed, skipped, fmt.Errorf("Box Office doesn't know a system called %q", key)
		}
		if name != "" && len(units) == 1 {
			dst, err := s.placeUnit(ctx, key, u, fileName(u, name))
			if errors.Is(err, ErrClash) {
				skipped = append(skipped, Skipped{Path: u.path, Clash: dst})
				continue
			}
			if err != nil && !errors.Is(err, ErrHave) {
				return placed, skipped, err
			}
			placed = append(placed, Placed{To: dst, From: u.files()})
			continue
		}
		if sys.Disc() {
			dst, err := s.placeUnit(ctx, key, u, filepath.Base(u.path))
			if errors.Is(err, ErrClash) {
				skipped = append(skipped, Skipped{Path: u.path, Clash: dst})
				continue
			}
			if err != nil && !errors.Is(err, ErrHave) {
				return placed, skipped, err
			}
			placed = append(placed, Placed{To: dst, From: u.files()})
			continue
		}
		for _, f := range u.files() {
			dst, err := s.Add(ctx, key, f)
			if errors.Is(err, ErrClash) {
				skipped = append(skipped, Skipped{Path: u.path, Clash: dst})
				continue
			}
			if err != nil && !errors.Is(err, ErrHave) {
				return placed, skipped, err
			}
			placed = append(placed, Placed{To: dst, From: []string{f}})
		}
	}
	return placed, skipped, nil
}

// fileName is what a game is filed as when it's given a name: a folder
// or a cue sheet's folder by the name, a file by the name and its
// extension. A Switch file keeps its title ID, which says which game,
// update or DLC it is.
func fileName(u unit, name string) string {
	name = strings.TrimSpace(name)
	if u.dir || len(u.parts) > 0 {
		return name
	}
	base := filepath.Base(u.path)
	ext := filepath.Ext(base)
	if e2 := filepath.Ext(strings.TrimSuffix(base, ext)); strings.EqualFold(e2, ".nkit") {
		ext = e2 + ext
	}
	if tid, ok := gamedat.SwitchTitleID(base); ok && !strings.Contains(strings.ToUpper(name), tid) {
		name += " [" + tid + "]"
	}
	return name + ext
}

// GuessName says which game a file or folder is, for the system it's
// for: its name on the system's list, found by the serial or title ID
// in it or its name, or for a system with no list the name written in
// it or on it. Cartridges, checked by their hashes when they're in, and
// MAME sets, named for their sets, give none.
func (s *Service) GuessName(ctx context.Context, key, p string) string {
	sys, ok := gamedat.LookupSystem(key)
	if !ok || sys.Arcade || sys.Hash {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	u := unit{path: p, dir: st.IsDir()}
	if !u.dir {
		u.parts = Parts(p)
	}
	id, _ := gamedat.Probe(p)
	if len(sys.Lists) == 0 {
		return cmp.Or(id.Title, TitleFromName(stemOf(u)))
	}
	ix, _, err := s.listIndex(ctx, sys)
	if err != nil {
		return ""
	}
	if e := match(ix, id, stemOf(u)); e != nil {
		return e.Name()
	}
	return ""
}
