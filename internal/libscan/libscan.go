// Package libscan looks through a root folder for titles that are on disk
// but not on Box Office's list, matches each folder to TMDB (movies and
// shows) or MusicBrainz (artists), and adds the ones an admin picks, with
// the files already there.
//
// It only ever reads the disk. Nothing is moved, renamed or deleted, while
// scanning or adding: a title added from a scan keeps its folder as it is.
//
// Scans run in the background, since a big library takes longer to read
// and look up than a proxy in front of Box Office waits for one request.
// They're kept in memory: one per root folder, until it's dismissed or a
// day old.
package libscan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/catalog"
	"github.com/yeixio/boxoffice/internal/mediatypes"
	"github.com/yeixio/boxoffice/internal/music"
	"github.com/yeixio/boxoffice/internal/musicbrainz"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/tmdb"
)

// Service runs root folder scans.
type Service struct {
	Store   *store.Store
	Catalog *catalog.Service
	Music   *music.Service
	Log     *slog.Logger

	mu    sync.Mutex
	scans map[int]*Scan // by root folder
}

// Statuses of a scan.
const (
	Scanning = "scanning"
	Ready    = "ready"
	Adding   = "adding"
	Done     = "done"
	Failed   = "failed"
)

// Scan is one look through a root folder.
type Scan struct {
	RootID int    `json:"rootId"`
	Root   string `json:"root"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Step   string `json:"step"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Error  string `json:"error,omitempty"`
	// Folders is how many folders the root folder has, Listed how many of
	// them are on the list already, and Elsewhere how many are named like a
	// title on the list that's kept at another path (the same library
	// mounted twice, say). Loose is files lying in the root folder itself,
	// outside any title's folder, which a scan leaves alone.
	Folders   int `json:"folders"`
	Listed    int `json:"listed"`
	Elsewhere int `json:"elsewhere"`
	Loose     int `json:"loose"`
	// Found are the folders not on the list, by name.
	Found []Found `json:"found"`
	// Added and NotAdded count what the last add did.
	Added    int       `json:"added"`
	NotAdded int       `json:"notAdded"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

// Found is a folder in the root folder that isn't on the list.
type Found struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Files is how many videos (or for music, tracks) are in it, and Size
	// their size. Episodes is how many of a show's videos are named for an
	// episode (like S01E02).
	Files    int   `json:"files"`
	Size     int64 `json:"size"`
	Episodes int   `json:"episodes,omitempty"`
	// Guess is what its name says.
	Guess Guess `json:"guess"`
	// Match is the title it's taken to be, and Sure says the name and year
	// agree with it; one that isn't sure should be checked.
	Match *Match `json:"match,omitempty"`
	Sure  bool   `json:"sure"`
	// Have is the title on the list that the match already is, kept in
	// another folder; it isn't added twice.
	Have *Have `json:"have,omitempty"`
	// Note says what's odd about what's in it, such as no video, which
	// doesn't stop it being added.
	Note string `json:"note,omitempty"`
	// Problem says why it can't be matched or added.
	Problem string `json:"problem,omitempty"`
	// Added is set once it's on the list.
	Added int64 `json:"added,omitempty"`
}

// Match is a title on TMDB or an artist on MusicBrainz.
type Match struct {
	TMDBID         int64  `json:"tmdbId,omitempty"`
	MBID           string `json:"mbid,omitempty"`
	Title          string `json:"title"`
	Year           int    `json:"year,omitempty"`
	Poster         string `json:"poster,omitempty"`
	Overview       string `json:"overview,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
}

// Have is a title on the list.
type Have struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// Error is a mistake in what was asked, fit to show whoever asked.
type Error = catalog.Error

func badf(format string, a ...any) error { return &Error{Message: fmt.Sprintf(format, a...)} }

// ErrBusy means the scan is still running.
var ErrBusy = errors.New("that root folder is being scanned or added from already")

// keep is how long a finished scan stays.
const keep = 24 * time.Hour

// scanTimeout bounds one scan or add, however big the library.
const scanTimeout = 2 * time.Hour

// Scannable says whether root folders of a kind can be scanned: those
// whose titles Box Office can add.
func Scannable(kind string) bool {
	return kind == "movie" || kind == "series" || kind == mediatypes.Music
}

func (s *Service) root(ctx context.Context, id int) (store.RootFolder, error) {
	roots, err := s.Store.RootFolders(ctx, "")
	if err != nil {
		return store.RootFolder{}, err
	}
	i := slices.IndexFunc(roots, func(r store.RootFolder) bool { return r.ID == id })
	if i < 0 {
		return store.RootFolder{}, store.ErrNotFound
	}
	return roots[i], nil
}

func (s *Service) prune() {
	for id, sc := range s.scans {
		if (sc.Status == Ready || sc.Status == Done || sc.Status == Failed) && time.Since(sc.Finished) > keep {
			delete(s.scans, id)
		}
	}
}

// Get is the root folder's scan, if it has one.
func (s *Service) Get(rootID int) (Scan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	sc, ok := s.scans[rootID]
	if !ok {
		return Scan{}, false
	}
	out := *sc
	out.Found = slices.Clone(sc.Found)
	return out, true
}

// Dismiss forgets the root folder's scan, unless it's running.
func (s *Service) Dismiss(rootID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc, ok := s.scans[rootID]; ok && (sc.Status == Scanning || sc.Status == Adding) {
		return ErrBusy
	}
	delete(s.scans, rootID)
	return nil
}

func (s *Service) update(sc *Scan, f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// Start scans a root folder in the background. A scan already running
// comes back as it is.
func (s *Service) Start(ctx context.Context, rootID int) (Scan, error) {
	root, err := s.root(ctx, rootID)
	if err != nil {
		return Scan{}, err
	}
	if !Scannable(root.Kind) {
		t, _ := mediatypes.Lookup(root.Kind)
		return Scan{}, badf("Box Office can't add %s to its list yet, so there's nothing to scan for", strings.ToLower(cmp.Or(t.Name, root.Kind)))
	}
	if root.Kind == mediatypes.Music {
		if on, err := mediatypes.Enabled(ctx, s.Store, root.Kind); err != nil {
			return Scan{}, err
		} else if !on {
			return Scan{}, music.ErrOff
		}
	}
	if st, err := os.Stat(root.Path); err != nil || !st.IsDir() {
		return Scan{}, badf("Box Office can't see the folder %s", root.Path)
	}
	s.mu.Lock()
	if s.scans == nil {
		s.scans = map[int]*Scan{}
	}
	if sc, ok := s.scans[rootID]; ok && (sc.Status == Scanning || sc.Status == Adding) {
		out := *sc
		s.mu.Unlock()
		return out, nil
	}
	sc := &Scan{RootID: root.ID, Root: root.Path, Kind: root.Kind, Status: Scanning, Step: "Reading the folder", Found: []Found{}, Started: time.Now().UTC()}
	s.scans[rootID] = sc
	out := *sc
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		err := s.scan(ctx, sc)
		s.update(sc, func() {
			sc.Finished = time.Now().UTC()
			sc.Done, sc.Total = 0, 0
			if err != nil {
				sc.Status, sc.Error, sc.Step = Failed, err.Error(), ""
				return
			}
			sc.Status, sc.Step = Ready, ""
		})
		if err != nil {
			s.Log.Warn("root folder scan failed", "root", sc.Root, "err", err)
		} else {
			s.Log.Info("root folder scanned", "root", sc.Root, "folders", sc.Folders, "listed", sc.Listed, "found", len(sc.Found))
		}
	}()
	return out, nil
}

// key is how paths are compared: network shares often don't mind case.
func key(p string) string { return strings.ToLower(filepath.Clean(p)) }

func (s *Service) scan(ctx context.Context, sc *Scan) error {
	entries, err := os.ReadDir(sc.Root)
	if err != nil {
		return fmt.Errorf("Box Office can't read %s: %w", sc.Root, err)
	}
	listed, err := s.Store.ListedFolders(ctx)
	if err != nil {
		return err
	}
	files, err := s.Store.FilePaths(ctx)
	if err != nil {
		return err
	}
	// Folders on the list, and the folders holding files on it.
	have := map[string]bool{}
	byName := map[string]store.Listed{}
	for _, l := range listed {
		have[key(l.Path)] = true
		if l.Kind == sc.Kind {
			byName[strings.ToLower(filepath.Base(l.Path))] = l
		}
	}
	rootKey := key(sc.Root)
	for _, f := range files {
		for d := filepath.Dir(f); d != "/" && d != "." && key(d) != rootKey; d = filepath.Dir(d) {
			if have[key(d)] {
				break
			}
			if key(filepath.Dir(d)) == rootKey {
				have[key(d)] = true
				break
			}
		}
	}

	var todo []*Found
	var folders, listedN, elsewhere, loose int
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") || name == "lost+found" || name == "$RECYCLE.BIN" || name == "System Volume Information" {
			continue
		}
		path := filepath.Join(sc.Root, name)
		if !e.IsDir() {
			if e.Type().IsRegular() && (isVideo(name) || isAudio(name)) {
				loose++
			}
			continue
		}
		folders++
		if have[key(path)] {
			listedN++
			continue
		}
		if _, ok := byName[strings.ToLower(name)]; ok {
			elsewhere++
			continue
		}
		todo = append(todo, &Found{Path: path, Name: name})
	}
	s.update(sc, func() {
		sc.Folders, sc.Listed, sc.Elsewhere, sc.Loose = folders, listedN, elsewhere, loose
		sc.Step, sc.Total = "Looking inside folders", len(todo)
	})
	for i, f := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		count(f, sc.Kind)
		s.update(sc, func() { sc.Done = i + 1 })
	}

	s.update(sc, func() { sc.Step, sc.Done = s.lookupStep(sc.Kind), 0 })
	if err := s.matchAll(ctx, sc, todo); err != nil {
		return err
	}
	out := make([]Found, 0, len(todo))
	for _, f := range todo {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b Found) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	s.update(sc, func() { sc.Found = out })
	return nil
}

func (s *Service) lookupStep(kind string) string {
	if kind == mediatypes.Music {
		return "Looking them up on MusicBrainz"
	}
	return "Looking them up on TMDB"
}

var reSample = regexp.MustCompile(`(?i)(^|[\W_])sample([\W_]|$)`)

var (
	videoExts = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".wmv", ".mov", ".mpg", ".mpeg", ".webm"}
	audioExts = []string{".flac", ".mp3", ".m4a", ".m4b", ".aac", ".ogg", ".opus", ".wav", ".alac", ".wma", ".ape", ".aiff"}
)

func isVideo(name string) bool {
	return slices.Contains(videoExts, strings.ToLower(filepath.Ext(name)))
}
func isAudio(name string) bool {
	return slices.Contains(audioExts, strings.ToLower(filepath.Ext(name)))
}

func isSample(path string) bool {
	return reSample.MatchString(filepath.Base(path)) || strings.EqualFold(filepath.Base(filepath.Dir(path)), "sample")
}

// maxWalk is how many entries of one folder a scan reads at most.
const maxWalk = 20000

// video is a video file in a title's folder.
type video struct {
	path string
	size int64
}

// videosIn lists a folder's videos, without samples, biggest first.
func videosIn(dir string) []video {
	var out []video
	n := 0
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		n++
		if err != nil || n > maxWalk {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || strings.EqualFold(d.Name(), "extras") || strings.EqualFold(d.Name(), "featurettes")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isVideo(d.Name()) || isSample(path) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out = append(out, video{path, info.Size()})
		}
		return nil
	})
	slices.SortFunc(out, func(a, b video) int { return cmp.Or(cmp.Compare(b.size, a.size), strings.Compare(a.path, b.path)) })
	return out
}

// count fills in what's in a found folder.
func count(f *Found, kind string) {
	f.Guess = ParseName(f.Name)
	if kind == mediatypes.Music {
		n := 0
		filepath.WalkDir(f.Path, func(path string, d fs.DirEntry, err error) error {
			n++
			if err != nil || n > maxWalk {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() && isAudio(d.Name()) {
				if info, err := d.Info(); err == nil {
					f.Files++
					f.Size += info.Size()
				}
			}
			return nil
		})
		f.Guess = Guess{Title: f.Name}
		if f.Files == 0 {
			f.Note = "there's no music in it"
		}
		return
	}
	for _, v := range videosIn(f.Path) {
		f.Files++
		f.Size += v.size
		if _, _, ok := episodeCode(filepath.Base(v.path)); ok {
			f.Episodes++
		}
	}
	switch {
	case f.Files == 0:
		f.Note = "there's no video in it"
	case kind == "series" && f.Episodes == 0:
		f.Note = "none of its videos are named for an episode (like S01E02)"
	}
}

// lookups is how many TMDB lookups run at once.
const lookups = 4

func (s *Service) matchAll(ctx context.Context, sc *Scan, todo []*Found) error {
	if sc.Kind != mediatypes.Music && len(todo) > 0 {
		if err := s.Catalog.TMDB(ctx).Check(ctx); err != nil {
			return fmt.Errorf("Box Office can't match the folders without TMDB: %w", err)
		}
	}
	work := make(chan *Found)
	var wg sync.WaitGroup
	done := 0
	workers := lookups
	if sc.Kind == mediatypes.Music {
		workers = 1 // MusicBrainz takes one request a second
	}
	for range workers {
		wg.Go(func() {
			for f := range work {
				s.match(ctx, sc.Kind, f)
				s.update(sc, func() { done++; sc.Done = done })
			}
		})
	}
	for _, f := range todo {
		if ctx.Err() != nil {
			break
		}
		work <- f
	}
	close(work)
	wg.Wait()
	return ctx.Err()
}

// match finds the title a folder is, and whether it's on the list in
// another folder.
func (s *Service) match(ctx context.Context, kind string, f *Found) {
	if f.Guess.Title == "" && f.Guess.TMDBID == 0 && f.Guess.TVDBID == 0 && f.Guess.IMDBID == "" {
		f.Problem = "its name doesn't say what it is"
		return
	}
	var err error
	switch kind {
	case "movie":
		err = s.matchMovie(ctx, f)
	case "series":
		err = s.matchShow(ctx, f)
	case mediatypes.Music:
		err = s.matchArtist(ctx, f)
	}
	if err != nil {
		f.Problem = "Box Office couldn't look it up: " + err.Error()
		return
	}
	if f.Match == nil {
		f.Problem = "Box Office found nothing by that name"
		return
	}
	s.checkHave(ctx, kind, f)
}

func (s *Service) checkHave(ctx context.Context, kind string, f *Found) {
	f.Have = nil
	switch kind {
	case "movie":
		if m, err := s.Store.MovieByTMDB(ctx, f.Match.TMDBID); err == nil {
			f.Have = &Have{m.ID, m.Title, m.Path}
		}
	case "series":
		if t, err := s.Store.SeriesByTMDB(ctx, f.Match.TMDBID); err == nil {
			f.Have = &Have{t.ID, t.Title, t.Path}
		}
	case mediatypes.Music:
		if a, err := s.Store.ArtistByMBID(ctx, f.Match.MBID); err == nil {
			f.Have = &Have{a.ID, a.Name, a.Path}
		}
	}
}

func movieMatch(m tmdb.Movie) *Match {
	return &Match{TMDBID: m.TMDBID, Title: m.Title, Year: m.Year, Poster: m.Poster, Overview: m.Overview}
}

func showMatch(t tmdb.Show) *Match {
	return &Match{TMDBID: t.TMDBID, Title: t.Title, Year: t.Year, Poster: t.Poster, Overview: t.Overview}
}

// close says a year is the guessed one, or a year off: a film's festival
// premiere and its release often fall either side of New Year.
func nearYear(year, guess int) bool {
	return guess == 0 || year != 0 && year >= guess-1 && year <= guess+1
}

func (s *Service) matchMovie(ctx context.Context, f *Found) error {
	c := s.Catalog.TMDB(ctx)
	g := f.Guess
	if g.TMDBID != 0 {
		m, err := c.Movie(ctx, g.TMDBID)
		if err == nil {
			f.Match, f.Sure = movieMatch(m), true
			return nil
		} else if !errors.Is(err, tmdb.ErrNotFound) {
			return err
		}
	}
	if g.IMDBID != "" {
		m, err := c.MovieByIMDB(ctx, g.IMDBID)
		if err == nil {
			f.Match, f.Sure = movieMatch(m), true
			return nil
		} else if !errors.Is(err, tmdb.ErrNotFound) {
			return err
		}
	}
	if g.Title == "" {
		return nil
	}
	term := g.Title
	if g.Year > 0 {
		term = fmt.Sprintf("%s %d", g.Title, g.Year)
	}
	found, err := c.SearchMovies(ctx, term)
	if err != nil {
		return err
	}
	if len(found) == 0 && g.Year > 0 {
		if found, err = c.SearchMovies(ctx, g.Title); err != nil {
			return err
		}
	}
	if len(found) == 0 {
		return nil
	}
	pick := found[0]
	if i := slices.IndexFunc(found, func(m tmdb.Movie) bool { return same(m.Title, g.Title) && nearYear(m.Year, g.Year) }); i >= 0 {
		pick = found[i]
		f.Sure = g.Year > 0 || len(found) == 1
	}
	f.Match = movieMatch(pick)
	return nil
}

func (s *Service) matchShow(ctx context.Context, f *Found) error {
	c := s.Catalog.TMDB(ctx)
	g := f.Guess
	if g.TMDBID != 0 {
		t, err := c.Show(ctx, g.TMDBID)
		if err == nil {
			f.Match, f.Sure = showMatch(t), true
			return nil
		} else if !errors.Is(err, tmdb.ErrNotFound) {
			return err
		}
	}
	if g.TVDBID != 0 {
		t, err := c.ShowByTVDB(ctx, g.TVDBID)
		if err == nil {
			f.Match, f.Sure = showMatch(t), true
			return nil
		} else if !errors.Is(err, tmdb.ErrNotFound) {
			return err
		}
	}
	if g.Title == "" {
		return nil
	}
	found, err := c.SearchShows(ctx, g.Title)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	pick := found[0]
	// Shows share names ("The Office"); the year tells them apart.
	named := slices.IndexFunc(found, func(t tmdb.Show) bool { return same(t.Title, g.Title) && nearYear(t.Year, g.Year) })
	if named >= 0 {
		pick = found[named]
		others := 0
		for _, t := range found {
			if same(t.Title, g.Title) {
				others++
			}
		}
		f.Sure = g.Year > 0 || others == 1
	}
	f.Match = showMatch(pick)
	return nil
}

func (s *Service) matchArtist(ctx context.Context, f *Found) error {
	found, err := s.Music.MusicBrainz.SearchArtists(ctx, f.Guess.Title)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	pick := found[0]
	matching := 0
	for _, a := range found {
		if same(a.Name, f.Guess.Title) {
			if matching == 0 {
				pick = a
			}
			matching++
		}
	}
	f.Sure = matching == 1
	f.Match = artistMatch(pick)
	return nil
}

func artistMatch(a musicbrainz.Artist) *Match {
	return &Match{MBID: a.MBID, Title: a.Name, Disambiguation: a.Disambiguation}
}

// Pick is one found folder to add, as the title it was matched to or
// another picked instead.
type Pick struct {
	Path   string `json:"path"`
	TMDBID int64  `json:"tmdbId,omitempty"`
	MBID   string `json:"mbid,omitempty"`
}

// AddRequest is which found folders to add, and how.
type AddRequest struct {
	Picks     []Pick `json:"picks"`
	ProfileID int    `json:"profileId"`
	Monitored bool   `json:"monitored"`
	// Monitor picks a show's episodes, as when adding one; empty is the
	// default from Settings.
	Monitor    string   `json:"monitor,omitempty"`
	AlbumTypes []string `json:"albumTypes,omitempty"`
}

// Add puts the picked folders on the list in the background, each with its
// folder as it is and the files in it.
func (s *Service) Add(ctx context.Context, rootID int, req AddRequest) (Scan, error) {
	if len(req.Picks) == 0 {
		return Scan{}, badf("pick at least one folder to add")
	}
	s.mu.Lock()
	sc, ok := s.scans[rootID]
	if !ok {
		s.mu.Unlock()
		return Scan{}, store.ErrNotFound
	}
	if sc.Status != Ready && sc.Status != Done {
		s.mu.Unlock()
		return Scan{}, ErrBusy
	}
	byPath := map[string]int{}
	for i, f := range sc.Found {
		byPath[f.Path] = i
	}
	for _, p := range req.Picks {
		i, ok := byPath[p.Path]
		if !ok {
			s.mu.Unlock()
			return Scan{}, badf("%s isn't one of the folders the scan found", p.Path)
		}
		if sc.Found[i].Added != 0 {
			s.mu.Unlock()
			return Scan{}, badf("%s is on the list already", sc.Found[i].Name)
		}
		if sc.Kind == mediatypes.Music && !musicbrainz.IsMBID(p.MBID) || sc.Kind != mediatypes.Music && p.TMDBID <= 0 {
			s.mu.Unlock()
			return Scan{}, badf("pick what %s is first", sc.Found[i].Name)
		}
	}
	sc.Status, sc.Step, sc.Done, sc.Total, sc.Added, sc.NotAdded = Adding, "Adding to the list", 0, len(req.Picks), 0, 0
	out := *sc
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		for _, p := range req.Picks {
			if ctx.Err() != nil {
				break
			}
			id, problem := s.addOne(ctx, sc, p, req)
			s.update(sc, func() {
				i := byPath[p.Path]
				sc.Done++
				if problem != "" {
					sc.NotAdded++
					sc.Found[i].Problem = problem
					return
				}
				sc.Added++
				sc.Found[i].Added, sc.Found[i].Problem = id, ""
			})
		}
		s.update(sc, func() {
			sc.Status, sc.Step, sc.Finished = Done, "", time.Now().UTC()
			sc.Done, sc.Total = 0, 0
		})
		s.Log.Info("added from a root folder scan", "root", sc.Root, "added", sc.Added, "not added", sc.NotAdded)
	}()
	return out, nil
}

// addOne adds one folder, and says what it's now called, or why it
// wasn't added.
func (s *Service) addOne(ctx context.Context, sc *Scan, p Pick, req AddRequest) (int64, string) {
	// What's on disk may have changed since the scan.
	st, err := os.Lstat(p.Path)
	if err != nil || !st.IsDir() {
		return 0, "the folder isn't there any more"
	}
	if listed, err := s.Store.ListedFolders(ctx); err == nil {
		for _, l := range listed {
			if key(l.Path) == key(p.Path) {
				return 0, l.Title + " is in this folder on the list already"
			}
		}
	}
	explain := func(err error) string {
		var ce *catalog.Error
		switch {
		case errors.Is(err, catalog.ErrExists):
			return "it's on the list already, in another folder"
		case errors.As(err, &ce):
			return ce.Message
		}
		return err.Error()
	}
	o := catalog.AddOptions{ProfileID: req.ProfileID, RootFolder: sc.Root, Path: p.Path, Monitored: req.Monitored, Monitor: req.Monitor}
	switch sc.Kind {
	case "movie":
		m, err := s.Catalog.AddMovie(ctx, p.TMDBID, o)
		if err != nil {
			return 0, explain(err)
		}
		if vids := videosIn(p.Path); len(vids) > 0 {
			v := vids[0]
			if _, err := s.Store.AddFile(ctx, store.File{Kind: "movie", OwnerID: m.ID, Path: v.path, Size: v.size, Quality: fileQuality(v.path)}); err != nil {
				s.Log.Warn("couldn't record a movie's file", "title", m.Title, "file", v.path, "err", err)
			}
		}
		return m.ID, ""
	case "series":
		t, err := s.Catalog.AddSeries(ctx, p.TMDBID, o)
		if err != nil {
			return 0, explain(err)
		}
		eps, err := s.Store.Episodes(ctx, t.ID)
		if err != nil {
			return t.ID, ""
		}
		at := map[[2]int]store.Episode{}
		for _, e := range eps {
			at[[2]int{e.Season, e.Number}] = e
		}
		recorded := map[int64]bool{}
		// Biggest first, so a second copy of an episode doesn't win.
		for _, v := range videosIn(p.Path) {
			season, number, ok := episodeCode(filepath.Base(v.path))
			if !ok {
				continue
			}
			e, ok := at[[2]int{season, number}]
			if !ok || recorded[e.ID] {
				continue
			}
			if _, err := s.Store.AddFile(ctx, store.File{Kind: "episode", OwnerID: e.ID, Path: v.path, Size: v.size, Quality: fileQuality(v.path)}); err != nil {
				s.Log.Warn("couldn't record an episode's file", "title", t.Title, "file", v.path, "err", err)
				continue
			}
			recorded[e.ID] = true
		}
		return t.ID, ""
	case mediatypes.Music:
		a, err := s.Music.Add(ctx, music.AddOptions{MBID: p.MBID, RootFolder: sc.Root, Path: p.Path, Monitored: req.Monitored, AlbumTypes: req.AlbumTypes})
		if err != nil {
			if errors.Is(err, music.ErrOff) {
				return 0, err.Error()
			}
			return 0, explain(err)
		}
		return a.ID, ""
	}
	return 0, "Box Office can't add these yet"
}

// fileQuality reads a file's quality from its name, or its folder's.
func fileQuality(path string) int {
	if q := quality.Parse(filepath.Base(path)).ID; q != 0 {
		return q
	}
	return quality.Parse(filepath.Base(filepath.Dir(path))).ID
}
