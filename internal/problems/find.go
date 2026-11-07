// Package problems looks through the movie and TV library folders for
// files that need a hand, and fixes the ones an admin picks:
//
//   - files that aren't any title's on the list, loose in a library folder
//     or in a folder of their own, and copies Box Office hasn't recorded
//     inside a title's folder;
//   - more than one copy of a movie or an episode;
//   - recorded files that are gone from disk;
//   - disc images (.iso, .img), to remux into an MKV;
//   - movies split into parts (CD1, CD2…), to join into one file;
//   - names and folders that don't say what the title is, to rename the
//     way Box Office names what it downloads;
//   - leftovers such as samples, shortcuts, programs and partial downloads,
//     and empty folders.
//
// Finding only reads the disk. A fix never deletes or overwrites anything:
// it moves the originals to <root>/.recycle/problem-files/, where they wait
// until the admin deletes them for good or puts them back, and a new file
// (from a remux or a join) is written next to them and checked before the
// originals are moved. Every fix is recorded, so it can be put back.
package problems

import (
	"cmp"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/yeixio/boxoffice/internal/store"
	"golang.org/x/text/unicode/norm"
)

// Kinds of problems.
const (
	NotListed = "not_listed" // a folder or file in a library folder that isn't a title on the list
	Untracked = "untracked"  // a video in a title's folder Box Office hasn't recorded, that could be its copy
	Duplicate = "duplicate"  // more than one copy of a movie or an episode
	Missing   = "missing"    // a recorded file that's gone
	DiscImage = "disc_image" // an .iso or .img
	Parts     = "parts"      // a movie in parts, CD1, CD2…
	Naming    = "naming"     // a name or folder that doesn't say what it is
	Leftover  = "leftover"   // a sample, a program, a shortcut, a partial download
	Unknown   = "unknown"    // a video in a show's folder that isn't named for an episode
	EmptyDir  = "empty"      // an empty folder
)

// Fix actions.
const (
	ActRename     = "rename"      // move or rename files, or a title's folder (Moves)
	ActSetAside   = "set_aside"   // move Paths to the recycle folder
	ActKeepOne    = "keep_one"    // keep one copy (Keep), set the others aside
	ActRecord     = "record"      // record a file as the title's copy, renamed per Moves
	ActForget     = "forget"      // drop the record of a file that's gone
	ActConvert    = "convert"     // remux a disc image into Output
	ActJoin       = "join"        // join parts into Output
	ActRemoveDirs = "remove_dirs" // remove empty folders
)

// Problem is one thing in a library folder that needs a hand.
type Problem struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Root  string    `json:"root"`
	Title *TitleRef `json:"title,omitempty"`
	// Detail says more, such as an episode's S01E02 or why a name is off.
	Detail string `json:"detail,omitempty"`
	// Copies says parts each run as long as the movie, so they're likely
	// copies of it: the fix can keep one (Fix.Keep) instead of joining.
	Copies bool   `json:"copies,omitempty"`
	Files  []File `json:"files"`
	Fix    *Fix   `json:"fix,omitempty"`
}

// TitleRef is the title a problem is about.
type TitleRef struct {
	Kind   string `json:"kind"` // movie or series
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Year   int    `json:"year,omitempty"`
	Locked bool   `json:"locked,omitempty"`
}

// File is a file or folder a problem is about.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Dir is set for a folder; Size then counts everything in it, and
	// Count how many files that is.
	Dir   bool `json:"dir,omitempty"`
	Count int  `json:"count,omitempty"`
	// Name is the file's name as it was meant to be, when it's garbled on
	// disk ("WALL·E (2008)" for "WALLÂ·E (2008)").
	Name string `json:"name,omitempty"`
	// Recorded says Box Office has it as the title's copy (or an edition).
	Recorded bool `json:"recorded,omitempty"`
	// Episode is a show's file's S01E02.
	Episode string `json:"episode,omitempty"`
	// Media is what's in a video, for copies and parts, when FFmpeg is
	// there; Edition the cut its name or title says it is.
	Media   *Media `json:"media,omitempty"`
	Edition string `json:"edition,omitempty"`

	mtime   int64
	episode *store.Episode
	record  *store.File
}

// Move is a file a fix renames or moves.
type Move struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Folder is set when it's a title's folder.
	Folder bool `json:"folder,omitempty"`
}

// Fix is what fixing a problem does, for the admin to check first.
type Fix struct {
	Action string   `json:"action"`
	Moves  []Move   `json:"moves,omitempty"`
	Paths  []string `json:"paths,omitempty"` // what's set aside, or forgotten
	Keep   string   `json:"keep,omitempty"`  // the copy keep_one keeps unless told otherwise
	Output string   `json:"output,omitempty"`
	// Blocked says why it can't be done now; the problem is still listed.
	Blocked string `json:"blocked,omitempty"`
}

// Root is how a library folder looked.
type Root struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"` // movie or series
	Readable bool   `json:"readable"`
	Writable bool   `json:"writable"`
	Files    int    `json:"files"`
}

// Report is what a look through the library folders found.
type Report struct {
	Roots    []Root    `json:"roots"`
	Problems []Problem `json:"problems"`
}

// RecycleSub is the folder in a root's .recycle folder where fixes set
// originals aside. Box Office's emptying of recycle folders only touches
// its day folders, so nothing here goes until the admin says so.
const RecycleSub = "problem-files"

var (
	videoExts    = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".wmv", ".mov", ".mpg", ".mpeg", ".webm", ".vob", ".divx", ".flv", ".ogm"}
	discExts     = []string{".iso", ".img"}
	subtitleExts = []string{".srt", ".ass", ".ssa", ".sub", ".idx", ".vtt", ".sup"}
	// Files nobody wants in a library: programs and scripts, shortcuts,
	// and what's left of downloads and archives.
	leftoverExts = []string{".exe", ".scr", ".bat", ".cmd", ".com", ".pif", ".vbs", ".msi", ".lnk", ".url", ".website",
		".part", ".partial", ".!qb", ".!ut", ".crdownload", ".boxoffice-part", ".tmp",
		".rar", ".zip", ".7z", ".par2", ".sfv", ".nzb", ".torrent"}
	reRarPart = regexp.MustCompile(`(?i)\.r\d{2,3}$`)
	reSample  = regexp.MustCompile(`(?i)(^|[\W_])sample([\W_]|$)`)
	reEpisode = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})(?:[ ._-]?-?[ ._-]?E(\d{1,3}))?\b|\b(\d{1,2})x(\d{2,3})\b`)
	// A part of a movie split over discs: "CD1", "Part 2", "disc.1", "pt3".
	rePart   = regexp.MustCompile(`(?i)(?:^|[ ._-]+)[(\[]?(?:cd|dvd|disc|disk|part|pt)[ ._-]?(\d{1,2})[)\]]?$`)
	reSeason = regexp.MustCompile(`(?i)^(?:season|series|s)[ ._-]*(\d{1,3})$`)
	reTags   = regexp.MustCompile(`\s*[\[{][^\]}]*[\]}]`)
	// Folders of a title's extras, left alone.
	extraDirs = []string{"extras", "featurettes", "behind the scenes", "deleted scenes", "interviews", "scenes", "shorts", "trailers", "other", "specials", "bonus", "subs", "subtitles", "artwork"}
)

func hasExt(name string, exts []string) bool {
	return slices.Contains(exts, strings.ToLower(filepath.Ext(name)))
}

func isVideo(name string) bool { return hasExt(name, videoExts) }

func isLeftover(name string) bool {
	return hasExt(name, leftoverExts) || reRarPart.MatchString(name) || isVideo(name) && reSample.MatchString(stem(name))
}

func stem(name string) string {
	b := filepath.Base(name)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

// key turns a name into what's compared: letters and digits, lower case,
// with bracketed tags ({tmdb-1}, [imdbid-tt1], {edition-…}) dropped, and
// accents written the one way (see Repair).
func key(name string) string {
	name = norm.NFC.String(reTags.ReplaceAllString(name, ""))
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FileName is how Box Office names a title: "Title (Year)".
func FileName(title string, year int) string {
	n := cleanFileName(title)
	if year > 0 {
		n += fmt.Sprintf(" (%d)", year)
	}
	return n
}

func cleanFileName(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, s))
}

func episodeNumbers(name string) (int, []int) {
	m := reEpisode.FindStringSubmatch(name)
	if m == nil {
		return 0, nil
	}
	if m[1] != "" {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		nums := []int{e}
		if m[3] != "" {
			e2, _ := strconv.Atoi(m[3])
			for n := e + 1; n <= e2 && n-e < 20; n++ {
				nums = append(nums, n)
			}
		}
		return s, nums
	}
	s, _ := strconv.Atoi(m[4])
	e, _ := strconv.Atoi(m[5])
	return s, []int{e}
}

func episodeCode(season int, numbers []int) string {
	code := fmt.Sprintf("S%02dE%02d", season, numbers[0])
	for _, n := range numbers[1:] {
		code += fmt.Sprintf("E%02d", n)
	}
	return code
}

// Part says which part of a movie a file is (1 for "CD1"), and what its
// name is without it; 0 when it isn't named as a part.
func Part(name string) (rest string, part int) {
	s := stem(name)
	m := rePart.FindStringSubmatchIndex(s)
	if m == nil {
		return s, 0
	}
	n, _ := strconv.Atoi(s[m[2]:m[3]])
	return strings.TrimRight(s[:m[0]], " ._-"), n
}

func isExtraDir(name string) bool {
	return slices.Contains(extraDirs, strings.ToLower(strings.TrimSpace(name)))
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func problemID(kind string, paths ...string) string {
	sorted := slices.Clone(paths)
	slices.Sort(sorted)
	h := sha1.Sum([]byte(kind + "\x00" + strings.Join(sorted, "\x00")))
	return hex.EncodeToString(h[:8])
}

// entry is a file found on disk.
type entry struct {
	path  string
	size  int64
	mtime int64
}

// finder looks through one library folder.
type finder struct {
	root     string
	kind     string // movie or series
	writable bool
	titles   []*store.LibraryTitle
	byFolder map[string]*store.LibraryTitle
	recorded map[string]recorded
	// found are titles whose folder is missing and was found under another
	// spelling: their files aren't missing.
	found map[int64]bool
	out   []Problem
}

type recorded struct {
	title *store.LibraryTitle
	file  store.TitleFile
}

// Find looks through every movie and TV library folder.
func Find(ctx context.Context, st *store.Store, progress func(done, total int)) (Report, error) {
	roots, err := st.RootFolders(ctx, "")
	if err != nil {
		return Report{}, err
	}
	titles, err := st.LibraryTitles(ctx)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Roots: []Root{}, Problems: []Problem{}}
	var todo []store.RootFolder
	for _, r := range roots {
		if r.Kind == "movie" || r.Kind == "series" {
			todo = append(todo, r)
		}
	}
	for i, r := range todo {
		if progress != nil {
			progress(i, len(todo))
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		f := &finder{root: filepath.Clean(r.Path), kind: r.Kind, byFolder: map[string]*store.LibraryTitle{}, recorded: map[string]recorded{}, found: map[int64]bool{}}
		for i := range titles {
			t := &titles[i]
			if t.Kind != r.Kind || filepath.Clean(t.Root) != f.root {
				continue
			}
			f.titles = append(f.titles, t)
			f.byFolder[filepath.Clean(t.Path)] = t
			for _, tf := range t.Files {
				f.recorded[filepath.Clean(tf.Path)] = recorded{t, tf}
			}
		}
		root, problems := f.run(ctx)
		rep.Roots = append(rep.Roots, root)
		rep.Problems = append(rep.Problems, problems...)
	}
	if progress != nil {
		progress(len(todo), len(todo))
	}
	slices.SortStableFunc(rep.Problems, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)), strings.Compare(a.sortKey(), b.sortKey()))
	})
	return rep, nil
}

var order = []string{Duplicate, Parts, DiscImage, Untracked, Missing, Naming, Unknown, NotListed, Leftover, EmptyDir}

func kindOrder(k string) int { return slices.Index(order, k) }

func (p Problem) sortKey() string {
	if p.Title != nil {
		return strings.ToLower(p.Title.Name) + "\x00" + p.Detail
	}
	if len(p.Files) > 0 {
		return strings.ToLower(p.Files[0].Path)
	}
	return ""
}

func (f *finder) add(p Problem) {
	p.Root = f.root
	var paths []string
	for _, fl := range p.Files {
		paths = append(paths, fl.Path)
	}
	p.ID = problemID(p.Kind, paths...)
	for i := range p.Files {
		if b := filepath.Base(p.Files[i].Path); Garbled(b) {
			p.Files[i].Name = Repair(b)
		}
	}
	if p.Fix != nil && p.Fix.Blocked == "" {
		switch {
		case !f.writable:
			p.Fix.Blocked = "read_only"
		case p.Title != nil && p.Title.Locked:
			p.Fix.Blocked = "locked"
		}
	}
	if p.Files == nil {
		p.Files = []File{}
	}
	f.out = append(f.out, p)
}

func ref(t *store.LibraryTitle) *TitleRef {
	if t == nil {
		return nil
	}
	return &TitleRef{Kind: t.Kind, ID: t.ID, Name: t.Title, Year: t.Year, Locked: t.Locked}
}

func (f *finder) run(ctx context.Context) (Root, []Problem) {
	r := Root{Path: f.root, Kind: f.kind}
	entries, err := os.ReadDir(f.root)
	// An empty folder is usually a share that isn't mounted: nothing in it
	// is taken for missing.
	if err != nil || len(entries) == 0 {
		return r, nil
	}
	r.Readable = true
	f.writable = writable(f.root)
	r.Writable = f.writable
	seen := map[string]bool{}
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		name := e.Name()
		path := filepath.Join(f.root, name)
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			if t := f.byFolder[path]; t != nil {
				f.titleFolder(t, seen)
			} else if f.titleAbove(path) == nil {
				// A folder holding titles kept deeper down, such as a
				// collection folder, is looked at title by title below.
				if t := f.titleNamed(name); t != nil {
					f.foundFolder(t, path, seen)
				} else {
					f.notListed(path, true)
				}
			}
			continue
		}
		f.looseFile(path, e)
	}
	// Titles whose folder is deeper than the root's first level.
	for _, t := range f.titles {
		if p := filepath.Clean(t.Path); filepath.Dir(p) != f.root && inside(f.root, p) {
			if _, err := os.Stat(p); err == nil {
				f.titleFolder(t, seen)
			}
		}
	}
	// Recorded files that are gone.
	for path, rc := range f.recorded {
		if seen[path] || !inside(f.root, path) || f.found[rc.title.ID] {
			continue
		}
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		fl := File{Path: path, Size: rc.file.Size, Recorded: true}
		if rc.file.Kind == "episode" {
			fl.Episode = episodeCode(rc.file.Season, []int{rc.file.Number})
		}
		f.add(Problem{Kind: Missing, Title: ref(rc.title), Detail: fl.Episode, Files: []File{fl},
			Fix: &Fix{Action: ActForget, Paths: []string{path}}})
	}
	for _, p := range f.out {
		r.Files += len(p.Files)
	}
	return r, f.out
}

// titleAbove is the title whose folder is inside path, if any.
func (f *finder) titleAbove(path string) *store.LibraryTitle {
	for _, t := range f.titles {
		if inside(path, t.Path) {
			return t
		}
	}
	return nil
}

// titleNamed is the title on the list a folder in the root is named for
// once its name is repaired, when Box Office keeps that title's folder
// under another spelling: "WALLÂ·E (2008)" for "WALL·E (2008)", or "TÁR"
// with its accent written another way.
func (f *finder) titleNamed(name string) *store.LibraryTitle {
	k := key(Repair(name))
	if k == "" {
		return nil
	}
	for _, t := range f.titles {
		if p := filepath.Clean(t.Path); filepath.Dir(p) == f.root && key(Repair(filepath.Base(p))) == k {
			return t
		}
	}
	for _, t := range f.titles {
		if filepath.Dir(filepath.Clean(t.Path)) != f.root {
			continue
		}
		if t.Year > 0 && key(FileName(t.Title, t.Year)) == k || t.Kind == "series" && key(t.Title) == k {
			return t
		}
	}
	return nil
}

// foundFolder is a folder named for a title on the list that Box Office
// keeps at another path.
func (f *finder) foundFolder(t *store.LibraryTitle, path string, seen map[string]bool) {
	here, err := os.Stat(path)
	if err != nil {
		return
	}
	there, err := os.Stat(t.Path)
	switch {
	case err == nil && os.SameFile(here, there):
		// The same folder, by a name the share takes either way.
		f.titleFolder(t, seen)
		return
	case err == nil:
		// The title's folder is there too: this is a second one.
		n, size := dirSize(path)
		f.add(Problem{Kind: NotListed, Title: ref(t), Detail: "second_folder", Files: []File{{Path: path, Size: size, Dir: true, Count: n}},
			Fix: &Fix{Action: ActSetAside, Paths: []string{path}}})
		return
	}
	// The title's folder isn't there: this is it, named another way.
	// Renaming it to the name Box Office has puts it back where its
	// records say, so nothing else changes.
	f.found[t.ID] = true
	n, size := dirSize(path)
	f.add(Problem{Kind: Naming, Title: ref(t), Detail: "found_folder", Files: []File{{Path: path, Size: size, Dir: true, Count: n}},
		Fix: &Fix{Action: ActRename, Moves: []Move{{From: path, To: filepath.Clean(t.Path)}}}})
}

// walk lists the files under dir, skipping hidden folders and, with
// extras, the title's extras folders. Empty folders are listed too.
func walk(dir string, skipExtras bool) (files []entry, empty []string) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || skipExtras && isExtraDir(d.Name())) {
				return filepath.SkipDir
			}
			if path != dir {
				if list, err := os.ReadDir(path); err == nil && len(list) == 0 {
					empty = append(empty, path)
				}
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, entry{path, info.Size(), info.ModTime().Unix()})
		return nil
	})
	return files, empty
}

func dirSize(dir string) (n int, size int64) {
	files, _ := walk(dir, false)
	for _, e := range files {
		n++
		size += e.size
	}
	return n, size
}

// notListed is a folder or file in the root that isn't on the list.
func (f *finder) notListed(path string, dir bool) {
	fl := File{Path: path, Dir: dir}
	if dir {
		n, size := dirSize(path)
		fl.Size, fl.Count = size, n
		if n == 0 {
			f.add(Problem{Kind: EmptyDir, Files: []File{fl}, Fix: &Fix{Action: ActRemoveDirs, Paths: []string{path}}})
			return
		}
	} else if info, err := os.Stat(path); err == nil {
		fl.Size = info.Size()
	}
	f.add(Problem{Kind: NotListed, Files: []File{fl}, Fix: &Fix{Action: ActSetAside, Paths: []string{path}}})
}

// looseFile is a file lying in the root itself.
func (f *finder) looseFile(path string, e fs.DirEntry) {
	info, err := e.Info()
	if err != nil {
		return
	}
	fl := File{Path: path, Size: info.Size(), mtime: info.ModTime().Unix()}
	name := e.Name()
	switch {
	case isLeftover(name):
		f.add(Problem{Kind: Leftover, Files: []File{fl}, Fix: &Fix{Action: ActSetAside, Paths: []string{path}}})
		return
	case hasExt(name, discExts):
		f.add(Problem{Kind: DiscImage, Files: []File{fl}, Detail: "not_listed", Fix: &Fix{Action: ActSetAside, Paths: []string{path}}})
		return
	case !isVideo(name):
		return // artwork, .nfo and the like
	}
	if rc, ok := f.recorded[path]; ok {
		// A title's copy kept outside its folder.
		f.misplaced(rc, fl)
		return
	}
	// A movie loose in the root, named for a movie on the list.
	if f.kind == "movie" {
		if t := f.movieNamed(name); t != nil {
			if !hasCopy(t) {
				dest := filepath.Join(t.Path, FileName(t.Title, t.Year)+strings.ToLower(filepath.Ext(name)))
				f.add(Problem{Kind: Untracked, Title: ref(t), Files: []File{fl},
					Fix: &Fix{Action: ActRecord, Moves: withSubtitles(path, dest)}})
				return
			}
			files := append(copiesOf(t), fl)
			f.add(Problem{Kind: Duplicate, Title: ref(t), Files: files, Fix: &Fix{Action: ActKeepOne, Keep: files[0].Path}})
			return
		}
	}
	f.add(Problem{Kind: NotListed, Files: []File{fl}, Fix: &Fix{Action: ActSetAside, Paths: []string{path}}})
}

func hasCopy(t *store.LibraryTitle) bool {
	return slices.ContainsFunc(t.Files, func(f store.TitleFile) bool { return f.Kind == "movie" })
}

// copiesOf is a movie's recorded copy.
func copiesOf(t *store.LibraryTitle) []File {
	var out []File
	for _, tf := range t.Files {
		if tf.Kind == "movie" {
			rec := tf.File
			out = append(out, File{Path: tf.Path, Size: tf.Size, Recorded: true, record: &rec})
		}
	}
	return out
}

// movieNamed is the movie on the list a file's name is for.
func (f *finder) movieNamed(name string) *store.LibraryTitle {
	k := key(stem(name))
	var best *store.LibraryTitle
	for _, t := range f.titles {
		if t.Year == 0 {
			continue
		}
		if want := key(FileName(t.Title, t.Year)); strings.HasPrefix(k, want) && (best == nil || len(want) > len(key(FileName(best.Title, best.Year)))) {
			best = t
		}
	}
	return best
}

// withSubtitles moves a video and the subtitles named like it.
func withSubtitles(from, to string) []Move {
	moves := []Move{{From: from, To: to}}
	base := stem(from)
	entries, err := os.ReadDir(filepath.Dir(from))
	if err != nil {
		return moves
	}
	// Another video whose name starts with this one's keeps its own
	// subtitles: "Movie.2160p.srt" is "Movie.2160p.mkv"'s, not "Movie.mkv"'s.
	var longer []string
	for _, e := range entries {
		if s := stem(e.Name()); !e.IsDir() && isVideo(e.Name()) && len(s) > len(base) && strings.HasPrefix(s, base) {
			longer = append(longer, s)
		}
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !hasExt(n, subtitleExts) || !strings.HasPrefix(n, base) ||
			slices.ContainsFunc(longer, func(s string) bool { return strings.HasPrefix(n, s) }) {
			continue
		}
		extra := strings.TrimSuffix(strings.TrimPrefix(n, base), filepath.Ext(n))
		if extra != "" && !strings.HasPrefix(extra, ".") && !strings.HasPrefix(extra, " ") && !strings.HasPrefix(extra, "-") && !strings.HasPrefix(extra, "_") {
			continue
		}
		dest := strings.TrimSuffix(to, filepath.Ext(to)) + extra + strings.ToLower(filepath.Ext(n))
		moves = append(moves, Move{From: filepath.Join(filepath.Dir(from), n), To: dest})
	}
	return moves
}

// misplaced is a title's recorded copy kept somewhere other than its folder.
func (f *finder) misplaced(rc recorded, fl File) {
	fl.Recorded = true
	t := rc.title
	dest := f.expected(t, rc.file, fl.Path)
	if dest == "" {
		return
	}
	f.add(Problem{Kind: Naming, Title: ref(t), Detail: "outside_folder", Files: []File{fl}, Fix: &Fix{Action: ActRename, Moves: withSubtitles(fl.Path, dest)}})
}

// expected is where Box Office would keep a title's recorded file.
func (f *finder) expected(t *store.LibraryTitle, tf store.TitleFile, path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch tf.Kind {
	case "movie", "edition":
		base := FileName(t.Title, t.Year)
		if m := regexp.MustCompile(`(?i)\{edition-[^}]+\}`).FindString(stem(path)); m != "" {
			base += " " + m
		}
		return filepath.Join(t.Path, base+ext)
	case "episode":
		numbers := []int{tf.Number}
		if s, n := episodeNumbers(filepath.Base(path)); s == tf.Season && len(n) > 1 && n[0] == tf.Number {
			numbers = n
		}
		name := FileName(t.Title, t.Year) + " - " + episodeCode(tf.Season, numbers)
		if tf.EpisodeTitle != "" && len(numbers) == 1 {
			name += " - " + cleanFileName(tf.EpisodeTitle)
		}
		return filepath.Join(t.Path, seasonFolder(tf.Season), name+ext)
	}
	return ""
}

func seasonFolder(n int) string {
	if n == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %02d", n)
}

// nameOK says a recorded file's name and folder already say what it is,
// even if not exactly the way Box Office would name it: names with extra
// tags (quality, ids) after the title are fine.
func nameOK(t *store.LibraryTitle, tf store.TitleFile, path string) bool {
	if !inside(t.Path, path) {
		return false
	}
	switch tf.Kind {
	case "movie", "edition":
		return named(stem(path), t)
	case "episode":
		s, n := episodeNumbers(filepath.Base(path))
		if s != tf.Season || len(n) == 0 || n[0] != tf.Number {
			return false
		}
		// In a season folder for its season, right in the show's folder.
		rel, _ := filepath.Rel(t.Path, filepath.Dir(path))
		if strings.Contains(rel, string(filepath.Separator)) || rel == "." {
			return false
		}
		if m := reSeason.FindStringSubmatch(rel); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n == tf.Season
		}
		return tf.Season == 0 && strings.EqualFold(rel, "specials")
	}
	return true
}

// folderOK says a title's folder is named for it.
func folderOK(t *store.LibraryTitle) bool {
	k := key(filepath.Base(t.Path))
	if t.Kind == "series" && k == key(t.Title) {
		return true // shows are often kept without the year
	}
	return k == key(FileName(t.Title, t.Year)) && named(filepath.Base(t.Path), t) || t.Year == 0 && k == key(t.Title)
}

// named says a name starts with the movie's title and has its year in
// brackets, "Heat (1995)", as against a release name like
// "Heat.1995.1080p.BluRay". Tags after it (quality, ids) are fine.
func named(name string, t *store.LibraryTitle) bool {
	if t.Year == 0 {
		return strings.HasPrefix(key(name), key(t.Title))
	}
	return strings.HasPrefix(key(name), key(FileName(t.Title, t.Year))) && strings.Contains(name, fmt.Sprintf("(%d)", t.Year))
}

// titleFolder looks through a title's folder.
func (f *finder) titleFolder(t *store.LibraryTitle, seen map[string]bool) {
	if !folderOK(t) {
		dest := filepath.Join(filepath.Dir(t.Path), FileName(t.Title, t.Year))
		if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
			_, size := dirSize(t.Path)
			f.add(Problem{Kind: Naming, Title: ref(t), Detail: "folder", Files: []File{{Path: t.Path, Size: size, Dir: true}},
				Fix: &Fix{Action: ActRename, Moves: []Move{{From: t.Path, To: dest, Folder: true}}}})
		}
	}
	files, empty := walk(t.Path, true)
	for _, d := range empty {
		f.add(Problem{Kind: EmptyDir, Title: ref(t), Files: []File{{Path: d, Dir: true}}, Fix: &Fix{Action: ActRemoveDirs, Paths: []string{d}}})
	}
	var videos, discs []entry
	for _, e := range files {
		seen[e.path] = true
		name := filepath.Base(e.path)
		switch {
		case isLeftover(name):
			f.add(Problem{Kind: Leftover, Title: ref(t), Files: []File{{Path: e.path, Size: e.size}}, Fix: &Fix{Action: ActSetAside, Paths: []string{e.path}}})
		case hasExt(name, discExts):
			discs = append(discs, e)
		case isVideo(name):
			videos = append(videos, e)
		}
	}
	if t.Kind == "movie" {
		f.movieFolder(t, videos, discs)
	} else {
		f.showFolder(t, videos, discs)
	}
}

func (f *finder) movieFolder(t *store.LibraryTitle, videos, discs []entry) {
	// Parts first: a movie in parts is one copy.
	groups := map[string][]entry{}
	for _, v := range videos {
		if _, ok := f.recorded[v.path]; ok && f.recorded[v.path].file.Kind == "edition" {
			continue
		}
		rest, n := Part(v.path)
		if n == 0 {
			continue
		}
		g := filepath.Dir(v.path) + "\x00" + strings.ToLower(rest) + "\x00" + strings.ToLower(filepath.Ext(v.path))
		groups[g] = append(groups[g], v)
	}
	inParts := map[string]bool{}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		slices.SortFunc(g, func(a, b entry) int { _, x := Part(a.path); _, y := Part(b.path); return x - y })
		ok := true
		for i, e := range g {
			if _, n := Part(e.path); n != i+1 {
				ok = false
			}
		}
		// A title named with a part ("… Part 2") isn't split in parts.
		if _, n := Part(t.Title); n > 0 || !ok {
			continue
		}
		var fls []File
		var paths []string
		for _, e := range g {
			inParts[e.path] = true
			_, rec := f.recorded[e.path]
			fls = append(fls, File{Path: e.path, Size: e.size, Recorded: rec, mtime: e.mtime})
			paths = append(paths, e.path)
		}
		out := filepath.Join(t.Path, FileName(t.Title, t.Year)+".mkv")
		fix := &Fix{Action: ActJoin, Paths: paths, Output: out}
		if _, err := os.Lstat(out); err == nil {
			fix.Blocked = "output_exists"
		}
		f.add(Problem{Kind: Parts, Title: ref(t), Detail: strconv.Itoa(len(g)), Files: fls, Fix: fix})
	}
	// Disc images.
	for _, d := range discs {
		fl := File{Path: d.path, Size: d.size, mtime: d.mtime}
		out := filepath.Join(t.Path, FileName(t.Title, t.Year)+".mkv")
		fix := &Fix{Action: ActConvert, Paths: []string{d.path}, Output: out}
		if _, err := os.Lstat(out); err == nil {
			fix.Blocked = "output_exists"
		}
		f.add(Problem{Kind: DiscImage, Title: ref(t), Files: []File{fl}, Fix: fix})
	}
	// Copies.
	var copies []File
	for _, v := range videos {
		if inParts[v.path] {
			continue
		}
		fl := File{Path: v.path, Size: v.size, mtime: v.mtime}
		if rc, ok := f.recorded[v.path]; ok {
			fl.Recorded = true
			rec := rc.file.File
			fl.record = &rec
			if rc.file.Kind == "edition" {
				f.checkName(t, rc.file, fl)
				continue
			}
		}
		copies = append(copies, fl)
	}
	switch {
	case len(copies) == 1 && copies[0].Recorded:
		f.checkName(t, f.recorded[copies[0].Path].file, copies[0])
	case len(copies) == 1 && hasCopy(t):
		// The recorded copy is gone and another is here: the Missing
		// problem says so; this one can be recorded in its place.
		fallthrough
	case len(copies) == 1:
		dest := filepath.Join(t.Path, FileName(t.Title, t.Year)+strings.ToLower(filepath.Ext(copies[0].Path)))
		moves := []Move{}
		if !nameOK(t, store.TitleFile{File: store.File{Kind: "movie"}}, copies[0].Path) {
			moves = withSubtitles(copies[0].Path, dest)
		}
		f.add(Problem{Kind: Untracked, Title: ref(t), Files: copies, Fix: &Fix{Action: ActRecord, Paths: []string{copies[0].Path}, Moves: moves}})
	case len(copies) > 1:
		keep := copies[0].Path
		for _, c := range copies {
			if c.Recorded {
				keep = c.Path
			}
		}
		slices.SortStableFunc(copies, func(a, b File) int { return cmp.Compare(b.Size, a.Size) })
		f.add(Problem{Kind: Duplicate, Title: ref(t), Files: copies, Fix: &Fix{Action: ActKeepOne, Keep: keep}})
	}
}

// checkName lists a recorded file whose name or folder is off.
func (f *finder) checkName(t *store.LibraryTitle, tf store.TitleFile, fl File) {
	if nameOK(t, tf, fl.Path) {
		return
	}
	dest := f.expected(t, tf, fl.Path)
	if dest == "" || dest == fl.Path {
		return
	}
	fix := &Fix{Action: ActRename, Moves: withSubtitles(fl.Path, dest)}
	if _, err := os.Lstat(dest); err == nil && !strings.EqualFold(dest, fl.Path) {
		fix.Blocked = "output_exists"
	}
	if tf.Kind == "episode" {
		fl.Episode = episodeCode(tf.Season, []int{tf.Number})
	}
	f.add(Problem{Kind: Naming, Title: ref(t), Detail: fl.Episode, Files: []File{fl}, Fix: fix})
}

func (f *finder) showFolder(t *store.LibraryTitle, videos, discs []entry) {
	for _, d := range discs {
		// A show's disc holds several episodes; it can't be turned into
		// one of them, so it's only pointed out.
		f.add(Problem{Kind: DiscImage, Title: ref(t), Files: []File{{Path: d.path, Size: d.size}}, Detail: "show",
			Fix: &Fix{Action: ActSetAside, Paths: []string{d.path}}})
	}
	byEpisode := map[int64][]File{}
	var order []int64
	episodes := map[int64]*store.Episode{}
	for i := range t.Episodes {
		episodes[t.Episodes[i].ID] = &t.Episodes[i]
	}
	for _, v := range videos {
		fl := File{Path: v.path, Size: v.size, mtime: v.mtime}
		var ep *store.Episode
		if rc, ok := f.recorded[v.path]; ok {
			fl.Recorded = true
			rec := rc.file.File
			fl.record = &rec
			ep = episodes[rc.file.OwnerID]
		} else {
			season, numbers := episodeNumbers(filepath.Base(v.path))
			if len(numbers) > 0 {
				for i := range t.Episodes {
					if t.Episodes[i].Season == season && t.Episodes[i].Number == numbers[0] {
						ep = &t.Episodes[i]
					}
				}
			}
		}
		if ep == nil {
			detail := ""
			if s, n := episodeNumbers(filepath.Base(v.path)); len(n) > 0 {
				detail = episodeCode(s, n)
			}
			f.add(Problem{Kind: Unknown, Title: ref(t), Detail: detail, Files: []File{fl}, Fix: &Fix{Action: ActSetAside, Paths: []string{v.path}}})
			continue
		}
		fl.Episode = episodeCode(ep.Season, []int{ep.Number})
		fl.episode = ep
		if _, ok := byEpisode[ep.ID]; !ok {
			order = append(order, ep.ID)
		}
		byEpisode[ep.ID] = append(byEpisode[ep.ID], fl)
	}
	for _, id := range order {
		files := byEpisode[id]
		ep := episodes[id]
		code := episodeCode(ep.Season, []int{ep.Number})
		switch {
		case len(files) == 1 && files[0].Recorded:
			f.checkName(t, store.TitleFile{File: *files[0].record, Season: ep.Season, Number: ep.Number, EpisodeTitle: ep.Title}, files[0])
		case len(files) == 1:
			tf := store.TitleFile{File: store.File{Kind: "episode", OwnerID: ep.ID}, Season: ep.Season, Number: ep.Number, EpisodeTitle: ep.Title}
			var moves []Move
			if !nameOK(t, tf, files[0].Path) {
				moves = withSubtitles(files[0].Path, f.expected(t, tf, files[0].Path))
			}
			f.add(Problem{Kind: Untracked, Title: ref(t), Detail: code, Files: files, Fix: &Fix{Action: ActRecord, Paths: []string{files[0].Path}, Moves: moves}})
		default:
			keep := files[0].Path
			for _, c := range files {
				if c.Recorded {
					keep = c.Path
				}
			}
			f.add(Problem{Kind: Duplicate, Title: ref(t), Detail: code, Files: files, Fix: &Fix{Action: ActKeepOne, Keep: keep}})
		}
	}
}
