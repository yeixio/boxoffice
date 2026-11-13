package importer

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yeixio/boxoffice/internal/downloader"
	"github.com/yeixio/boxoffice/internal/games"
	"github.com/yeixio/boxoffice/internal/libscan"
)

// ReleaseFile is one file in a release that hasn't been downloaded yet,
// as its NZB lists it.
type ReleaseFile struct {
	Name string
	Size int64
}

// Auto, as the kind a download is filed as, has Box Office work out what
// it is once it's downloaded.
const Auto = "auto"

// FileKinds are what a download added by hand can be filed as.
var FileKinds = []string{Auto, Movie, Series, Album, Ebook, Audiobook, Game}

// reSeason finds a show's season in a release's name: "S02", "S01E05",
// "Season 3", "Complete Series".
var reSeason = regexp.MustCompile(`(?i)(^|[\s._\-\[(])(s\d{1,2}(e\d{1,3})?|season[\s._]?\d{1,2}|complete[\s._]series)([\s._\-\])]|$)`)

// GuessRelease works out what a release is before it's downloaded, from
// its name, the names of the files in it, and the category its indexer
// gave it (as Newznab writes it: "TV > HD", "Audio > Audiobook"). Most
// releases are packed in RAR files, so the name usually says the most.
func (s *Service) GuessRelease(ctx context.Context, name string, files []ReleaseFile, category string) Item {
	var w what
	for _, f := range files {
		w.add(f.Name, f.Size)
	}
	it := Item{Name: name, Dir: true, Files: len(files), Status: Ready}
	for _, f := range files {
		it.Size += f.Size
	}
	kind := kindOf(name, w)
	if kind == "" {
		kind = kindFromName(name, category)
	}
	if kind == "" {
		it.Problem = "Box Office can't tell what this is from its name; it'll look again at its files once it's downloaded"
		return it
	}
	s.guessWhat(ctx, &it, kind, w, "")
	return it
}

// kindFromName says what a release is from its name and category, when
// its files don't say.
func kindFromName(name, category string) string {
	c := strings.ToLower(category)
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(c, "audiobook") || strings.Contains(lower, "audiobook") || strings.Contains(lower, "unabridged"):
		return Audiobook
	case strings.Contains(c, "ebook") || strings.Contains(c, "book") || strings.Contains(c, "comic") || strings.Contains(lower, "epub"):
		return Ebook
	case strings.HasPrefix(c, "audio") || strings.Contains(lower, "flac") || strings.Contains(lower, "discography"):
		return Album
	case strings.HasPrefix(c, "console") || strings.HasPrefix(c, "pc"):
		return Game
	case strings.HasPrefix(c, "tv") || reSeason.MatchString(name):
		return Series
	case strings.HasPrefix(c, "movie") || libscan.ParseName(name).Year > 0:
		return Movie
	}
	return ""
}

// Adopt takes a finished download's folder (src) into the imports folder
// and, if Box Office can tell for sure what it is, files it in the library
// the way Import does. kind is what it was said to be, or Auto. It says
// where it went or, when it's left on the Import files page for someone to
// check, why.
func (s *Service) Adopt(ctx context.Context, src, name, kind string) (to, waiting string, err error) {
	if kind == Auto {
		kind = ""
	}
	dir, err := s.dir()
	if err != nil {
		return "", "", err
	}
	name, err = cleanName(strings.TrimLeft(filepath.Base(name), ". "))
	if err != nil {
		name = "download"
	}
	name = unique(dir, name)
	full := filepath.Join(dir, name)
	if err := os.Rename(src, full); err != nil {
		return "", "", writeErr(err, dir)
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", "", err
	}
	rel := path.Join(Folder, name)
	// It's marked as being imported while it's looked at, so the Import
	// files page doesn't look at it too.
	s.mu.Lock()
	if s.items == nil {
		s.items = map[string]*Item{}
	}
	cur := &Item{Path: rel, Name: name, Dir: st.IsDir(), Status: Importing, stamp: st.ModTime()}
	s.items[rel] = cur
	s.mu.Unlock()

	it := *cur
	s.guess(ctx, &it, kind)
	defer func() {
		s.mu.Lock()
		*cur = it
		s.mu.Unlock()
	}()
	if !ready(it) {
		it.Status = Ready
		waiting = cmp.Or(it.Problem, "Box Office isn't sure what it is; check what it found")
		if it.Problem == "" && it.Match != nil {
			waiting = "Box Office isn't sure it's " + it.Match.Title + "; check it"
		}
		return "", waiting, nil
	}
	it.Status = Importing
	to, err = s.importOne(ctx, it)
	var left *downloader.LeftOverError
	switch {
	case err == nil:
		it.Status, it.To = Done, to
	case errors.As(err, &left) && to != "", it.Kind == Game && to != "":
		it.Status, it.To, it.Problem = Done, to, "some files didn't go in: "+err.Error()
	default:
		it.Status, it.Problem = Failed, err.Error()
		return "", "", err
	}
	if it.Kind == Game && s.Games != nil {
		if _, err := s.Games.Start(context.WithoutCancel(ctx), 0, ""); err != nil && !errors.Is(err, games.ErrBusy) {
			s.log().Warn("checking games after a download", "err", err)
		}
	}
	return to, "", nil
}

// ready says whether Box Office can file an item without asking: it knows
// what it is, for sure, and nothing's in the way.
func ready(it Item) bool {
	if it.Problem != "" || !it.Sure {
		return false
	}
	if it.Kind == Game {
		return it.System != ""
	}
	return it.Match != nil
}
