// Package importer brings media in by hand: files uploaded from a browser,
// or copied from a folder on the server, are put in the imports folder in
// the downloads folder. Box Office works out what each one is (a movie, an
// episode or season of a show, an album, a book, a game) and, once that's
// right, files it in the library the way a finished download is: the
// title is added if it isn't on the list yet, and the file is moved into
// its folder and named as the library names it. Games are copied into
// their system's folder.
//
// What's waiting is whatever is in the imports folder, so it survives a
// restart; what Box Office made of each item is kept in memory.
package importer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/books"
	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/gamedat"
	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/libscan"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

// Folder is the imports folder's name in the downloads folder.
const Folder = "imports"

// Kinds an item can be.
const (
	Movie     = "movie"
	Series    = "series"
	Album     = store.AlbumKind
	Ebook     = store.Ebook
	Audiobook = store.Audiobook
	Game      = mediatypes.Game
)

// Statuses of an item.
const (
	Guessing  = "guessing"
	Ready     = "ready"
	Importing = "importing"
	Done      = "done"
	Failed    = "failed"
)

// Service keeps the imports folder.
type Service struct {
	Store     *store.Store
	Downloads *downloader.Engine
	Scan      *libscan.Service
	Catalog   *catalog.Service
	Music     *music.Service
	Books     *books.Service
	Games     *games.Service
	Log       *slog.Logger

	mu      sync.Mutex
	items   map[string]*Item
	copying Copying
	running bool
	waking  chan struct{}
}

// Item is one thing in the imports folder: a file, or a folder of them.
type Item struct {
	// Path is where it is, in the downloads folder: imports/<name>.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Files int    `json:"files"`
	Size  int64  `json:"size"`
	// Kind is what it is: movie, series, album, ebook, audiobook or game;
	// empty when Box Office can't tell.
	Kind string `json:"kind"`
	// Title and Year are what its name says. Season and Episode are an
	// episode's, from its name.
	Title   string `json:"title,omitempty"`
	Year    int    `json:"year,omitempty"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
	// Match is the title it is: a movie or show on TMDB, or the artist or
	// author of an album or book, whose title is Part.
	Match *libscan.Match `json:"match,omitempty"`
	Part  string         `json:"part,omitempty"`
	// System is a game's system, and Game the game it is: its name on
	// the system's list, or for a system with no list the name it goes in
	// as.
	System string `json:"system,omitempty"`
	Game   string `json:"game,omitempty"`
	// Sure says the name agrees with the match; one that isn't should be
	// checked. Picked says it was chosen by hand.
	Sure   bool   `json:"sure"`
	Picked bool   `json:"picked,omitempty"`
	Status string `json:"status"`
	// Problem says why it can't go in as it is.
	Problem string `json:"problem,omitempty"`
	// To is where it went.
	To string `json:"to,omitempty"`

	stamp time.Time
	// off says its kind was turned off when it was looked at.
	off bool
}

// Copying is a copy from a folder on the server into the imports folder.
type Copying struct {
	Running bool      `json:"running"`
	Done    int64     `json:"done"`
	Total   int64     `json:"total"`
	Current string    `json:"current,omitempty"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started,omitzero"`
}

// State is the imports folder and what's happening to it.
type State struct {
	Folder  string  `json:"folder"`
	Items   []Item  `json:"items"`
	Copying Copying `json:"copying"`
	Running bool    `json:"running"`
	// Kinds are the kinds the library takes: movies and shows, and the
	// others turned on in Settings.
	Kinds []string `json:"kinds"`
	// Systems are the game systems Box Office knows, to pick from.
	Systems []gamedat.System `json:"systems"`
}

// Error is a mistake in what was asked, fit to show whoever asked.
type Error = catalog.Error

func badf(format string, a ...any) error { return &Error{Message: fmt.Sprintf(format, a...)} }

// ErrBusy says a copy or import is running already.
var ErrBusy = errors.New("Box Office is already bringing files in; wait for it to finish")

func (s *Service) log() *slog.Logger { return cmp.Or(s.Log, slog.Default()) }

// dir is the imports folder, made if it isn't there.
func (s *Service) dir() (string, error) {
	if s.Downloads == nil || s.Downloads.Dir == "" {
		return "", badf("set up the downloads folder first: imports wait there")
	}
	d := filepath.Join(s.Downloads.Dir, Folder)
	if err := os.MkdirAll(d, 0o775); err != nil {
		return "", writeErr(err, s.Downloads.Dir)
	}
	return d, nil
}

func writeErr(err error, dir string) error {
	if errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission) {
		return badf("Box Office can't write to %s", dir)
	}
	if errors.Is(err, syscall.ENOSPC) {
		return badf("%s is full", dir)
	}
	return err
}

// Kinds says which kinds the library takes now.
func (s *Service) Kinds(ctx context.Context) []string {
	out := []string{Movie, Series}
	for _, k := range []struct{ kind, typ string }{{Album, mediatypes.Music}, {Ebook, mediatypes.Ebook}, {Audiobook, mediatypes.Audiobook}, {Game, mediatypes.Game}} {
		if on, _ := mediatypes.Enabled(ctx, s.Store, k.typ); on {
			out = append(out, k.kind)
		}
	}
	return out
}

// State reads the imports folder: what's in it, and what Box Office made
// of each. Anything new is looked at in the background.
func (s *Service) State(ctx context.Context) (State, error) {
	dir, err := s.dir()
	if err != nil {
		return State{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]*Item{}
	}
	seen := map[string]bool{}
	todo := false
	kinds := s.Kinds(ctx)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rel := path.Join(Folder, e.Name())
		seen[rel] = true
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := s.items[rel]
		if it != nil && (it.Status == Importing || it.Status == Done) {
			continue
		}
		// While a copy runs, what it's writing isn't looked at yet.
		if s.copying.Running && s.copying.Current == e.Name() {
			continue
		}
		if it == nil || (!it.stamp.Equal(info.ModTime()) && !it.Picked) {
			it = &Item{Path: rel, Name: e.Name(), Dir: e.IsDir(), Status: Guessing, stamp: info.ModTime()}
			s.items[rel] = it
		}
		// One looked at while its kind was off is looked at again once
		// it's on.
		if it.off && it.Status == Ready && slices.Contains(kinds, it.Kind) {
			it.Status = Guessing
		}
		if it.Status == Guessing {
			todo = true
		}
	}
	for rel, it := range s.items {
		if !seen[rel] && it.Status != Done && it.Status != Importing {
			delete(s.items, rel)
		}
	}
	if todo {
		s.wake()
	}
	out := State{Folder: dir, Items: []Item{}, Copying: s.copying, Running: s.running, Kinds: kinds, Systems: gamedat.Systems}
	for _, it := range s.items {
		out.Items = append(out.Items, *it)
	}
	slices.SortFunc(out.Items, func(a, b Item) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// wake starts the guesser if it isn't running. s.mu is held.
func (s *Service) wake() {
	if s.waking != nil {
		select {
		case s.waking <- struct{}{}:
		default:
		}
		return
	}
	s.waking = make(chan struct{}, 1)
	go s.guessAll()
}

// guessAll works out what each new item is, one at a time.
func (s *Service) guessAll() {
	for {
		s.mu.Lock()
		var next *Item
		for _, it := range s.items {
			if it.Status == Guessing && (next == nil || it.Name < next.Name) {
				next = it
			}
		}
		if next == nil {
			s.waking = nil
			s.mu.Unlock()
			return
		}
		it := *next
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		kind := ""
		if it.off {
			kind = it.Kind
		}
		s.guess(ctx, &it, kind)
		cancel()

		s.mu.Lock()
		if cur := s.items[it.Path]; cur == next && cur.Status == Guessing {
			*cur = it
		}
		s.mu.Unlock()
	}
}

// what is what's inside an item.
type what struct {
	files, videos, episodes, audio, m4b, ebooks, games int
	// discs are disc images no system could be told for.
	discs           int
	size, audioSize int64
	firstGame       string
	// emulator is the emulator install the folder is, such as MAME 0.154.
	emulator string
	system   string
}

// add counts one file in what's in an item.
func (w *what) add(p string, size int64) {
	name := filepath.Base(p)
	w.files++
	w.size += size
	// A disc image can be a film's DVD or a game's disc: what's on it
	// says which.
	isGame := func() bool {
		if w.games >= 5 && w.system != "" {
			w.games++
			return true
		}
		sys, ok := games.Identify(p)
		if !ok {
			return false
		}
		if w.system == "" {
			w.system = sys.Key
		}
		w.games++
		if w.firstGame == "" {
			w.firstGame = name
		}
		return true
	}
	switch {
	case strings.EqualFold(filepath.Ext(name), ".iso") && isGame():
	case screen.IsVideo(name):
		if !strings.Contains(strings.ToLower(name), "sample") {
			w.videos++
			if _, _, ok := libscan.EpisodeCode(name); ok {
				w.episodes++
			}
		}
	case screen.IsAudio(name):
		w.audio++
		w.audioSize += size
		if strings.EqualFold(filepath.Ext(name), ".m4b") {
			w.m4b++
		}
	case screen.IsEbook(name):
		w.ebooks++
	case gamedat.IsGameFile(name) || strings.EqualFold(filepath.Ext(name), ".chd"):
		if !isGame() && discImage(name) {
			w.discs++
		}
	}
}

// look reads what's in an item, up to a limit.
func look(full string, dir bool) what {
	var w what
	add := w.add
	if !dir {
		if st, err := os.Stat(full); err == nil {
			add(full, st.Size())
		}
		return w
	}
	// A folder that's one game: a PlayStation 3 game dumped whole, say.
	if sys, ok := games.Identify(full); ok {
		w.games, w.system, w.firstGame = 1, sys.Key, filepath.Base(full)
		w.files, w.size = 1, dirSize(full)
		return w
	}
	// An emulator's install, such as an old MAME folder: its games are
	// the emulator's whatever their files look like.
	if emus := games.FindEmulators(full); len(emus) > 0 {
		w.system, w.emulator = emus[0].System, emus[0].Label()
		for _, e := range emus[1:] {
			if e.System != w.system {
				w.system, w.emulator = "", ""
				break
			}
		}
	}
	n := 0
	filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
		n++
		if err != nil || n > 20000 {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != full && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && d.Type().IsRegular() {
			add(p, info.Size())
		}
		return nil
	})
	return w
}

// discImage says whether a file is a CD or DVD image.
func discImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".iso", ".cue", ".bin", ".img", ".mdf", ".nrg", ".chd", ".gdi", ".cso":
		return true
	}
	return false
}

// dirSize is how big everything in a folder is.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if i, err := d.Info(); err == nil {
				n += i.Size()
			}
		}
		return nil
	})
	return n
}

// kindOf says what an item most likely is from what's in it.
func kindOf(name string, w what) string {
	lower := strings.ToLower(name)
	switch {
	case w.games > 0 && w.games >= w.videos+w.audio+w.ebooks:
		return Game
	case w.discs > 0 && w.videos+w.audio+w.ebooks == 0:
		// Disc images, of a system Box Office can't tell.
		return Game
	case w.episodes > 0:
		return Series
	case w.videos > 0:
		return Movie
	case w.ebooks > 0 && w.ebooks >= w.audio:
		return Ebook
	case w.audio > 0:
		// Audiobooks come as .m4b, or say so, or are long files: an
		// album's tracks run a few minutes, a book's chapters far longer
		// (at a typical 64 to 128 kb/s, 60 MB is over an hour).
		if w.m4b > 0 || strings.Contains(lower, "audiobook") || strings.Contains(lower, "unabridged") || w.audioSize/int64(w.audio) > 60<<20 {
			return Audiobook
		}
		return Album
	}
	return ""
}

// guess works out what an item is, and what it's a copy of. kind, when
// given, is what it was said to be.
func (s *Service) guess(ctx context.Context, it *Item, kind string) {
	dir, err := s.dir()
	if err != nil {
		it.Status, it.Problem = Ready, err.Error()
		return
	}
	full := filepath.Join(dir, it.Name)
	w := look(full, it.Dir)
	it.Files, it.Size = w.files, w.size
	s.guessWhat(ctx, it, kind, w, full)
}

// guessWhat works out what an item is from what's in it. full is where it
// is, if it's on disk yet.
func (s *Service) guessWhat(ctx context.Context, it *Item, kind string, w what, full string) {
	it.Game = ""
	it.Match, it.Part, it.System, it.Sure, it.Problem, it.Title, it.Year, it.Season, it.Episode, it.off = nil, "", "", false, "", "", 0, 0, 0, false
	it.Kind = cmp.Or(kind, kindOf(it.Name, w))
	it.Status = Ready
	base := it.Name
	if !it.Dir {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if it.Kind == "" {
		it.Problem = "Box Office can't tell what this is; choose what it is"
		return
	}
	if !slices.Contains(s.Kinds(ctx), it.Kind) {
		it.off = true
		it.Problem = "turn on " + map[string]string{Album: "Music", Ebook: "Ebooks", Audiobook: "Audiobooks", Game: "Games"}[it.Kind] + " in Settings first"
		return
	}
	switch it.Kind {
	case Game:
		it.Title = base
		if w.system == "" {
			it.Problem = "Box Office can't tell which system it's for; choose the system"
			return
		}
		it.System, it.Sure = w.system, true
		if t := strings.TrimSpace(gamedat.Title(base)); t != "" {
			it.Title = t
		}
		s.guessGame(ctx, it, full)
		if w.emulator != "" {
			// Many games, not one to name.
			it.Title, it.Game, it.Sure = w.emulator, "", true
		}
		return
	case Movie, Series:
		f := s.Scan.Match(ctx, it.Kind, base)
		it.Title, it.Year = f.Guess.Title, f.Guess.Year
		if season, ep, ok := libscan.EpisodeCode(base); ok && !it.Dir {
			it.Season, it.Episode = season, ep
		}
		it.Match, it.Sure = f.Match, f.Sure
		if f.Problem != "" {
			it.Problem = f.Problem + "; find it by hand"
		}
		return
	case Album, Ebook, Audiobook:
		// "Artist - Album (2001)", "Author - Title".
		who, part, ok := strings.Cut(base, " - ")
		if !ok {
			it.Title = libscan.ParseName(base).Title
			it.Problem = "its name doesn't say who it's by; name it \"" + map[bool]string{true: "Artist - Album", false: "Author - Title"}[it.Kind == Album] + "\", or find it by hand"
			return
		}
		g := libscan.ParseName(part)
		it.Part, it.Year = cmp.Or(g.Title, strings.TrimSpace(part)), g.Year
		it.Title = strings.TrimSpace(who)
		kind := it.Kind
		if kind == Album {
			kind = mediatypes.Music
		}
		f := s.Scan.Match(ctx, kind, it.Title)
		it.Match, it.Sure = f.Match, f.Sure
		if f.Problem != "" {
			it.Problem = f.Problem + "; find it by hand"
		}
	}
}

// guessGame works out which game an item is, for its system: by the
// serial or title ID in it, or its name. One on a system with a list that
// the list doesn't have is to be checked.
func (s *Service) guessGame(ctx context.Context, it *Item, full string) {
	if s.Games == nil {
		return
	}
	sys, _ := gamedat.LookupSystem(it.System)
	it.Game = s.Games.GuessName(ctx, it.System, full)
	if it.Game != "" {
		it.Title = it.Game
	}
	if it.Game == "" && len(sys.Lists) > 0 && !sys.Hash && !sys.Arcade {
		it.Sure = false
	}
}

// Pick is what an item is, chosen by hand.
type Pick struct {
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdbId,omitempty"`
	MBID   string `json:"mbid,omitempty"`
	OLKey  string `json:"olKey,omitempty"`
	// Name is the title, artist or author picked, as shown.
	Name string `json:"name,omitempty"`
	Year int    `json:"year,omitempty"`
	// Part is the album or book's title.
	Part   string `json:"part,omitempty"`
	System string `json:"system,omitempty"`
	// Game is the game it is, picked from the system's list or typed.
	Game string `json:"game,omitempty"`
}

// Set says what an item is. A kind on its own looks it up again as that.
func (s *Service) Set(ctx context.Context, rel string, p Pick) (Item, error) {
	s.mu.Lock()
	cur, ok := s.items[rel]
	if !ok {
		s.mu.Unlock()
		return Item{}, store.ErrNotFound
	}
	if cur.Status == Importing || cur.Status == Done || cur.Status == Guessing {
		s.mu.Unlock()
		return Item{}, badf("wait until Box Office has finished with %s", cur.Name)
	}
	it := *cur
	s.mu.Unlock()
	if p.Kind != "" && !slices.Contains([]string{Movie, Series, Album, Ebook, Audiobook, Game}, p.Kind) {
		return Item{}, badf("Box Office doesn't import %s", p.Kind)
	}
	picked := p.TMDBID != 0 || p.MBID != "" || p.OLKey != "" || p.System != "" || p.Game != ""
	if p.Kind != "" && p.Kind != it.Kind || !picked && p.Part == "" {
		s.guess(ctx, &it, cmp.Or(p.Kind, it.Kind))
	}
	if p.Part != "" {
		it.Part = p.Part
	}
	if picked {
		it.Problem, it.Sure, it.Picked = "", true, true
		switch it.Kind {
		case Movie, Series:
			if p.TMDBID == 0 {
				return Item{}, badf("pick the movie or show")
			}
			it.Match = &libscan.Match{TMDBID: p.TMDBID, Title: p.Name, Year: p.Year}
		case Album:
			if p.MBID == "" {
				return Item{}, badf("pick the artist")
			}
			it.Match = &libscan.Match{MBID: p.MBID, Title: p.Name}
		case Ebook, Audiobook:
			if p.OLKey == "" {
				return Item{}, badf("pick the author")
			}
			it.Match = &libscan.Match{OLKey: p.OLKey, Title: p.Name}
		case Game:
			sys, ok := gamedat.LookupSystem(cmp.Or(p.System, it.System))
			if !ok {
				return Item{}, badf("pick the system")
			}
			changed := sys.Key != it.System
			it.System = sys.Key
			switch g := strings.TrimSpace(p.Game); {
			case g != "":
				it.Game, it.Title = g, g
			case changed:
				it.Game = ""
				if dir, err := s.dir(); err == nil {
					s.guessGame(ctx, &it, filepath.Join(dir, it.Name))
				}
				it.Sure = true
			}
		}
	}
	if (it.Kind == Album || it.Kind == Ebook || it.Kind == Audiobook) && it.Match != nil && it.Part == "" {
		it.Problem = "say which " + map[bool]string{true: "album", false: "book"}[it.Kind == Album] + " it is"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.items[rel]; cur != nil && cur.Status == Ready {
		*cur = it
	}
	return it, nil
}

// cleanName checks a name or relative path for the imports folder.
func cleanName(p string) (string, error) {
	p = path.Clean(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	if p == "." || p == "" || strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") {
		return "", badf("give the file's name")
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return "", badf("%s can't be brought in: its name starts with a dot", p)
		}
	}
	return p, nil
}

// Upload writes a file sent from a browser into the imports folder. name
// may be a path inside a folder sent whole ("Show/Season 1/e01.mkv").
func (s *Service) Upload(ctx context.Context, name string, size int64, r io.Reader) error {
	rel, err := cleanName(name)
	if err != nil {
		return err
	}
	dir, err := s.dir()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := os.Lstat(dst); err == nil {
		return badf("%s is in the imports folder already", rel)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return writeErr(err, dir)
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".part")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return writeErr(err, dir)
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && size >= 0 && n != size {
		err = badf("only %d of %d bytes of %s arrived", n, size, rel)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return writeErr(err, dir)
	}
	return nil
}

// CopyIn copies files and folders on the server into the imports folder,
// in the background. They're left where they are.
func (s *Service) CopyIn(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return badf("pick at least one file or folder")
	}
	dir, err := s.dir()
	if err != nil {
		return err
	}
	var total int64
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return badf("give the full path of %s", p)
		}
		p = filepath.Clean(p)
		if p == dir || strings.HasPrefix(p+string(filepath.Separator), dir+string(filepath.Separator)) {
			return badf("%s is in the imports folder already", p)
		}
		if strings.HasPrefix(dir+string(filepath.Separator), p+string(filepath.Separator)) {
			return badf("%s holds the imports folder; pick what's in it instead", p)
		}
		n, err := sizeOf(p)
		if err != nil {
			return badf("can't read %s", p)
		}
		total += n
	}
	s.mu.Lock()
	if s.copying.Running || s.running {
		s.mu.Unlock()
		return ErrBusy
	}
	s.copying = Copying{Running: true, Total: total, Started: time.Now().UTC()}
	s.mu.Unlock()
	var jobs []copyJob
	for _, p := range paths {
		name := unique(dir, filepath.Base(p))
		for slices.ContainsFunc(jobs, func(j copyJob) bool { return j.Name == name }) {
			name = unique(dir, name+" ")
		}
		jobs = append(jobs, copyJob{From: filepath.Clean(p), Name: name})
	}
	// Written down, so a restart part way through carries on after.
	if b, err := json.Marshal(jobs); err == nil {
		os.WriteFile(filepath.Join(dir, copyingFile), b, 0o664)
	}
	go s.copyJobs(dir, jobs)
	return nil
}

// copyingFile, in the imports folder, lists a copy from the server that
// hasn't finished, so that one cut off by a restart is carried on.
const copyingFile = ".copying.json"

// copyJob is one file or folder being copied in, and its name there.
type copyJob struct {
	From string `json:"from"`
	Name string `json:"name"`
}

// copyJobs copies files and folders in from the server, one at a time.
// dropHalfMade removes the hidden .part items in the imports folder that
// none of jobs will finish.
func (s *Service) dropHalfMade(dir string, jobs []copyJob) {
	keep := map[string]bool{}
	for _, j := range jobs {
		keep["."+j.Name+".part"] = true
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".part") || keep[n] {
			continue
		}
		s.log().Info("removing a half-made item from the imports folder", "name", n)
		os.RemoveAll(filepath.Join(dir, n))
	}
}

func (s *Service) copyJobs(dir string, jobs []copyJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	var failed error
	for _, j := range jobs {
		if _, err := os.Lstat(filepath.Join(dir, j.Name)); err == nil {
			continue // copied whole before a restart
		}
		s.mu.Lock()
		s.copying.Current = j.Name
		s.mu.Unlock()
		if err := s.copyTree(ctx, j.From, filepath.Join(dir, j.Name)); err != nil {
			s.log().Warn("copying into the imports folder", "path", j.From, "err", err)
			failed = fmt.Errorf("couldn't copy %s: %w", j.From, writeErr(err, dir))
			os.RemoveAll(filepath.Join(dir, j.Name))
			break
		}
	}
	os.Remove(filepath.Join(dir, copyingFile))
	s.mu.Lock()
	s.copying.Running, s.copying.Current = false, ""
	if failed != nil {
		s.copying.Error = failed.Error()
	}
	s.mu.Unlock()
}

// Resume carries on a copy from the server that a restart cut off, from
// where it got to. One whose files have gone from the server is given up,
// and says so.
// Resume runs once at startup. A copy into the imports folder that a
// restart cut off carries on from where it got to. Half-made items that
// no copy will finish (an upload cut off, or a copy from before copies
// were written down) are removed, since nothing would ever show them.
func (s *Service) Resume() {
	dir, err := s.dir()
	if err != nil {
		return
	}
	var jobs []copyJob
	if b, err := os.ReadFile(filepath.Join(dir, copyingFile)); err == nil {
		if json.Unmarshal(b, &jobs) != nil {
			jobs = nil
		}
	}
	s.dropHalfMade(dir, jobs)
	if len(jobs) == 0 {
		os.Remove(filepath.Join(dir, copyingFile))
		return
	}
	var total int64
	for _, j := range jobs {
		n, err := sizeOf(j.From)
		if err != nil {
			os.Remove(filepath.Join(dir, copyingFile))
			for _, j := range jobs {
				os.RemoveAll(filepath.Join(dir, "."+j.Name+".part"))
			}
			s.mu.Lock()
			s.copying.Error = fmt.Sprintf("a copy from %s stopped when Box Office restarted, and can't carry on: %s can't be read now", filepath.Dir(j.From), j.From)
			s.mu.Unlock()
			return
		}
		total += n
	}
	s.mu.Lock()
	if s.copying.Running {
		s.mu.Unlock()
		return
	}
	s.copying = Copying{Running: true, Total: total, Started: time.Now().UTC()}
	s.mu.Unlock()
	s.log().Info("carrying on a copy into the imports folder that a restart cut off", "files", len(jobs))
	go s.copyJobs(dir, jobs)
}

func sizeOf(p string) (int64, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	if !st.IsDir() {
		return st.Size(), nil
	}
	var n int64
	err = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if i, err := d.Info(); err == nil {
				n += i.Size()
			}
		}
		return nil
	})
	return n, err
}

// unique is a name not yet in dir: "x", or "x (2)".
func unique(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
		return name
	}
	ext := ""
	if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
		ext = filepath.Ext(name)
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, n)); errors.Is(err, os.ErrNotExist) {
			return n
		}
	}
}

// copyTree copies a file, or a folder and what's in it. A folder is
// written under a hidden name and renamed when it's whole.
func (s *Service) copyTree(ctx context.Context, src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return s.copyFile(ctx, src, dst)
	}
	// What's there from a copy a restart cut off is kept, and only what's
	// missing or short is copied.
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".part")
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(tmp, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o775)
		case d.Type().IsRegular():
			if have, err := os.Stat(to); err == nil {
				if src, err := d.Info(); err == nil && src.Size() == have.Size() {
					s.mu.Lock()
					s.copying.Done += have.Size()
					s.mu.Unlock()
					return nil
				}
			}
			return s.copyFile(ctx, p, to)
		}
		return nil
	})
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.RemoveAll(tmp)
	}
	return err
}

func (s *Service) copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".part")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o664)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				break
			}
			s.mu.Lock()
			s.copying.Done += int64(n)
			s.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			err = rerr
			break
		}
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Remove takes an item out of the imports folder, for good. Only what's in
// the imports folder can be removed: Box Office's own copy of it.
func (s *Service) Remove(ctx context.Context, rel string) error {
	name, err := cleanName(strings.TrimPrefix(rel, Folder+"/"))
	if err != nil || strings.Contains(name, "/") {
		return badf("that isn't in the imports folder")
	}
	dir, err := s.dir()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if it := s.items[path.Join(Folder, name)]; it != nil && it.Status == Importing {
		s.mu.Unlock()
		return badf("%s is being imported", name)
	}
	delete(s.items, path.Join(Folder, name))
	s.mu.Unlock()
	return os.RemoveAll(filepath.Join(dir, name))
}

// Clear forgets items that went in.
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, it := range s.items {
		if it.Status == Done {
			delete(s.items, k)
		}
	}
}

// Run imports the items given (or all that are ready) in the background.
func (s *Service) Run(ctx context.Context, paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.copying.Running {
		return ErrBusy
	}
	var todo []*Item
	for _, it := range s.items {
		if len(paths) > 0 && !slices.Contains(paths, it.Path) {
			continue
		}
		if (it.Status == Ready || it.Status == Failed) && it.Problem == "" && (it.Match != nil || it.Kind == Game && it.System != "") {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		return badf("nothing is ready to import: say what each one is first")
	}
	slices.SortFunc(todo, func(a, b *Item) int { return strings.Compare(a.Name, b.Name) })
	for _, it := range todo {
		it.Status, it.Problem = Importing, ""
	}
	s.running = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()
		gamesIn := false
		for _, cur := range todo {
			s.mu.Lock()
			it := *cur
			s.mu.Unlock()
			to, err := s.importOne(ctx, it)
			s.mu.Lock()
			if err != nil {
				cur.Status, cur.Problem = Failed, err.Error()
				var left *downloader.LeftOverError
				if errors.As(err, &left) && to != "" {
					cur.Status, cur.To, cur.Problem = Done, to, "some files didn't go in: "+left.Error()
				}
				if it.Kind == Game && to != "" {
					// Some games went in; the rest are still in the imports folder.
					cur.Status, cur.To = Done, to
					gamesIn = true
				}
			} else {
				cur.Status, cur.To = Done, to
				gamesIn = gamesIn || it.Kind == Game
			}
			s.mu.Unlock()
			if err != nil {
				s.log().Warn("importing", "name", it.Name, "kind", it.Kind, "err", err)
			} else {
				s.log().Info("imported", "name", it.Name, "kind", it.Kind, "to", to)
			}
		}
		if gamesIn && s.Games != nil {
			if _, err := s.Games.Start(ctx, 0, ""); err != nil && !errors.Is(err, games.ErrBusy) {
				s.log().Warn("checking games after an import", "err", err)
			}
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	return nil
}

// importOne files one item in the library, and says where it went.
func (s *Service) importOne(ctx context.Context, it Item) (string, error) {
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, it.Name)
	switch it.Kind {
	case Game:
		return s.importGames(ctx, it, full)
	case Movie:
		id, err := s.movie(ctx, it.Match.TMDBID)
		if err != nil {
			return "", err
		}
		return s.Downloads.MovePath(ctx, it.Path, Movie, id, 0)
	case Series:
		id, err := s.show(ctx, it.Match.TMDBID)
		if err != nil {
			return "", err
		}
		var episode int64
		if !it.Dir && (it.Season > 0 || it.Episode > 0) {
			if eps, err := s.Store.Episodes(ctx, id); err == nil {
				for _, e := range eps {
					if e.Season == it.Season && e.Number == it.Episode {
						episode = e.ID
					}
				}
			}
			if episode == 0 && !it.Dir {
				return "", badf("%s has no season %d episode %d on TMDB", it.Match.Title, it.Season, it.Episode)
			}
		}
		return s.Downloads.MovePath(ctx, it.Path, Series, id, episode)
	case Album:
		a, err := s.Store.ArtistByMBID(ctx, it.Match.MBID)
		if err != nil {
			a, err = s.Music.Add(ctx, music.AddOptions{MBID: it.Match.MBID, Monitored: false})
			if errors.Is(err, catalog.ErrExists) {
				a, err = s.Store.ArtistByMBID(ctx, it.Match.MBID)
			}
			if err != nil {
				return "", err
			}
		}
		albums, err := s.Store.Albums(ctx, a.ID)
		if err != nil {
			return "", err
		}
		for _, al := range albums {
			if libscan.Same(al.Title, it.Part) {
				return s.Downloads.MovePath(ctx, it.Path, Album, al.ID, 0)
			}
		}
		return "", badf("%s has no album called %s on MusicBrainz; check its name", a.Name, it.Part)
	case Ebook, Audiobook:
		a, err := s.Store.AuthorByKey(ctx, it.Match.OLKey)
		if err != nil {
			a, err = s.Books.Add(ctx, books.AddOptions{Key: it.Match.OLKey, Format: it.Kind})
			if errors.Is(err, catalog.ErrExists) {
				a, err = s.Store.AuthorByKey(ctx, it.Match.OLKey)
			}
			if err != nil && a.ID == 0 {
				return "", err
			}
		}
		list, err := s.Store.Books(ctx, a.ID)
		if err != nil {
			return "", err
		}
		for _, b := range list {
			if libscan.Same(b.Title, it.Part) || libscan.Same(strings.TrimSpace(b.Title+" "+b.Subtitle), it.Part) {
				return s.Downloads.MovePath(ctx, it.Path, it.Kind, b.ID, 0)
			}
		}
		return "", badf("Open Library has no book by %s called %s; check its name", a.Name, it.Part)
	}
	return "", badf("say what %s is first", it.Name)
}

// movie is the movie on the list for a TMDB id, added if it isn't.
func (s *Service) movie(ctx context.Context, tmdbID int64) (int64, error) {
	if m, err := s.Store.MovieByTMDB(ctx, tmdbID); err == nil {
		return m.ID, nil
	}
	m, err := s.Catalog.AddMovie(ctx, tmdbID, catalog.AddOptions{})
	if err != nil && !errors.Is(err, catalog.ErrExists) {
		return 0, err
	}
	return m.ID, nil
}

// show is the show on the list for a TMDB id, added if it isn't. A show
// added for an import isn't watched for new episodes.
func (s *Service) show(ctx context.Context, tmdbID int64) (int64, error) {
	if t, err := s.Store.SeriesByTMDB(ctx, tmdbID); err == nil {
		return t.ID, nil
	}
	t, err := s.Catalog.AddSeries(ctx, tmdbID, catalog.AddOptions{Monitor: "none"})
	if err != nil && !errors.Is(err, catalog.ErrExists) {
		return 0, err
	}
	return t.ID, nil
}

// importGames copies an item's games into their systems' folders, then
// takes the ones it placed out of the imports folder. A file it can't
// place stays there, and the item says which.
func (s *Service) importGames(ctx context.Context, it Item, full string) (string, error) {
	system := it.System
	if it.Dir && !it.Picked {
		// A folder of games: each is told by what it is.
		system = ""
	}
	got, skipped, err := s.Games.ImportGames(ctx, system, full, it.Game)
	var to, placed []string
	for _, g := range got {
		placed = append(placed, g.From...)
		if d := filepath.Dir(g.To); !slices.Contains(to, d) {
			to = append(to, d)
		}
	}
	if err != nil {
		s.unimport(full, placed)
		return strings.Join(to, ", "), err
	}
	var clashes, unknown []string
	for _, f := range skipped {
		name := filepath.Base(f.Path)
		if r, err := filepath.Rel(full, f.Path); err == nil && it.Dir {
			name = r
		}
		if f.Clash != "" {
			clashes = append(clashes, name)
		} else {
			unknown = append(unknown, name)
		}
	}
	if len(skipped) == 0 && len(to) > 0 {
		os.RemoveAll(full)
		return strings.Join(to, ", "), nil
	}
	if it.Dir {
		s.unimport(full, placed)
	} else if len(to) > 0 {
		os.RemoveAll(full)
	}
	if len(to) == 0 && len(skipped) == 0 {
		return "", badf("there are no games in it")
	}
	var why []string
	if len(clashes) > 0 {
		why = append(why, fmt.Sprintf("a different file of the same name is in the games folder already, so these weren't added: %s", someNames(clashes)))
	}
	if len(unknown) > 0 {
		why = append(why, fmt.Sprintf("Box Office couldn't tell which system these are for, or they aren't games: %s", someNames(unknown)))
	}
	msg := strings.Join(why, "; ") + ". They're still in the imports folder"
	if len(to) == 0 {
		return "", badf("%s", msg)
	}
	return strings.Join(to, ", "), badf("added games to %s, but %s", strings.Join(to, ", "), msg)
}

// someNames lists up to ten names, and says how many more there are.
func someNames(names []string) string {
	if len(names) <= 10 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:10], ", "), len(names)-10)
}

// unimport takes the files an import placed out of a folder item, and the
// folders that leaves empty, keeping everything else.
func (s *Service) unimport(full string, placed []string) {
	for _, f := range placed {
		os.Remove(f)
		for d := filepath.Dir(f); d != full && strings.HasPrefix(d, full+string(filepath.Separator)); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
}
