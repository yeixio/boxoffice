package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/flush"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/screen"
)

// LibraryImporter moves finished downloads into the titles' folders.
type LibraryImporter struct {
	Store *store.Store
	// Tags reads what a video says about itself (its title, synopsis and
	// episode tags); ffprobe when nil.
	Tags func(ctx context.Context, path string) (VideoTags, error)
}

// ModeEpisode marks an import of one video as the episode in its
// EpisodeIDs, chosen by hand.
const ModeEpisode = "episode"

// LeftOverError means some of a download's videos went into the library
// and others couldn't be matched to an episode: those stay in the
// downloads folder, so nothing is lost.
type LeftOverError struct {
	To   string
	Left []string
}

func (e *LeftOverError) Error() string {
	n := len(e.Left)
	if n == 1 {
		return fmt.Sprintf("1 video couldn't be matched to an episode, so it's still in the downloads folder (%s)", e.Left[0])
	}
	return fmt.Sprintf("%d videos couldn't be matched to an episode, so they're still in the downloads folder", n)
}

var videoExts = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".wmv", ".mov", ".mpg", ".mpeg", ".webm"}
var subtitleExts = []string{".srt", ".ass", ".ssa", ".sub", ".idx", ".vtt"}

var (
	reSample  = regexp.MustCompile(`(?i)(^|[\W_])sample([\W_]|$)`)
	reEpisode = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})(?:[ ._-]?E(\d{1,3}))?\b|\b(\d{1,2})x(\d{2,3})\b`)
)

func isVideo(name string) bool {
	return slices.Contains(videoExts, strings.ToLower(filepath.Ext(name)))
}

// videos are the download's video files, biggest first, without samples.
func videos(dir string, files []string) []string {
	type sized struct {
		name string
		size int64
	}
	var list []sized
	for _, f := range files {
		if !isVideo(f) || reSample.MatchString(filepath.Base(f)) && len(files) > 1 {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		list = append(list, sized{f, st.Size()})
	}
	slices.SortFunc(list, func(a, b sized) int {
		if a.size > b.size {
			return -1
		}
		if a.size < b.size {
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	var out []string
	for _, s := range list {
		out = append(out, s.name)
	}
	return out
}

// Import moves the download's video (and its subtitles) into the title's
// folder.
func (li *LibraryImporter) Import(ctx context.Context, d store.Download, dir string, files []string) (string, error) {
	// A title on Do not touch never gets a file moved in.
	if d.Kind == "movie" || d.Kind == "series" || store.IsBookKind(d.Kind) {
		if err := li.Store.CheckUnlocked(ctx, d.Kind, d.TitleID); err != nil {
			return "", err
		}
	}
	if store.IsBookKind(d.Kind) {
		return li.importBook(ctx, d, dir, files)
	}
	if d.Kind == store.AlbumKind {
		return li.importAlbum(ctx, d, dir, files)
	}
	vids := videos(dir, files)
	if len(vids) == 0 {
		return "", errors.New("there's no video file in it")
	}
	q := d.Quality
	if q == 0 {
		q = quality.Parse(d.Name).ID
	}
	switch d.Kind {
	case "movie":
		m, err := li.Store.Movie(ctx, d.TitleID)
		if err != nil {
			return "", fmt.Errorf("the movie it was for is gone: %w", err)
		}
		if err := checkLibrary(m.RootFolder, m.Path); err != nil {
			return "", err
		}
		tw, err := li.Store.TitleWant(ctx, "movie", m.ID)
		if err != nil {
			return "", err
		}
		edition := d.Edition
		if edition == "" {
			edition = quality.Edition(filepath.Base(vids[0]))
		}
		// A copy is named for its edition when one was asked for, so it
		// and any other edition kept next to it can be told apart (Matinee,
		// Plex and Jellyfin read the {edition-…} tag).
		base := fileName(m.Title, m.Year)
		if tag := quality.EditionTag(edition); tag != "" && (d.Mode != "" || tw.Edition != "") {
			base += " " + tag
		}
		dest := filepath.Join(m.Path, base+strings.ToLower(filepath.Ext(vids[0])))
		if d.Mode == store.ModeAlongside {
			// Another edition goes in next to the movie's copy, leaving it
			// be; one already there by that name is set aside.
			if m.File != nil && m.File.Path == dest {
				return "", fmt.Errorf("the movie's copy is the %s edition already", edition)
			}
			if err := li.place(ctx, dir, vids[0], dest, m.RootFolder, m.Path, "edition", m.ID, q, nil); err != nil {
				return "", err
			}
			return dest, nil
		}
		if err := li.place(ctx, dir, vids[0], dest, m.RootFolder, m.Path, "movie", m.ID, q, m.File); err != nil {
			return "", err
		}
		if d.Mode == store.ModeReplace && edition != "" && !quality.SameEdition(tw.Edition, edition) {
			// From now on the movie is wanted in this edition, so upgrades
			// don't swap it back.
			tw.Edition, tw.Want, tw.Note = edition, "", ""
			if err := li.Store.SetTitleWant(ctx, tw); err != nil {
				return "", fmt.Errorf("moved in, but couldn't note the edition: %w", err)
			}
		}
		return dest, nil
	case "series":
		t, err := li.Store.Series(ctx, d.TitleID)
		if err != nil {
			return "", fmt.Errorf("the show it was for is gone: %w", err)
		}
		if err := checkLibrary(t.RootFolder, t.Path); err != nil {
			return "", err
		}
		eps, err := li.Store.Episodes(ctx, t.ID)
		if err != nil {
			return "", err
		}
		// Obfuscated posts can name the video anything. When no video says
		// which episode it is, the videos' own tags are read next (web
		// releases carry the store's synopsis), and failing that the
		// biggest one is taken as the episode the release is named for, or
		// the one it was grabbed for.
		named := slices.ContainsFunc(vids, func(v string) bool {
			_, n := episodeNumbers(filepath.Base(v))
			return len(n) > 0
		})
		type target struct {
			season  int
			numbers []int
		}
		targets := map[string]target{}
		for _, v := range vids {
			if s, n := episodeNumbers(filepath.Base(v)); len(n) > 0 {
				targets[v] = target{s, n}
			}
		}
		main := map[string]bool{}
		for _, m := range mainVideos(dir, vids, true) {
			main[m.name] = true
		}
		if d.Mode == ModeEpisode {
			i := -1
			if len(d.EpisodeIDs) == 1 {
				i = slices.IndexFunc(eps, func(e store.Episode) bool { return e.ID == d.EpisodeIDs[0] })
			}
			if i < 0 || len(main) != 1 {
				return "", errors.New("choose one video and an episode of the show it's for")
			}
			var v string
			for m := range main {
				v = m
			}
			targets = map[string]target{v: {eps[i].Season, []int{eps[i].Number}}}
		} else if !named {
			if s, n := releaseEpisode(d, eps); len(n) > 0 && len(main) == 1 {
				targets[vids[0]] = target{s, n}
			} else {
				for v, e := range li.matchTags(ctx, d, dir, vids, main, eps, time.Duration(t.Runtime)*time.Minute) {
					targets[v] = target{e.Season, []int{e.Number}}
				}
				if len(targets) == 0 && len(n) > 0 {
					targets[vids[0]] = target{s, n}
				}
			}
		}
		var placed, left []string
		for _, v := range vids {
			tg, ok := targets[v]
			if !ok {
				if !named && main[v] {
					left = append(left, v)
				}
				continue
			}
			season, numbers := tg.season, tg.numbers
			if len(numbers) == 0 {
				continue
			}
			i := slices.IndexFunc(eps, func(e store.Episode) bool { return e.Season == season && e.Number == numbers[0] })
			if i < 0 {
				continue
			}
			// A download for some episodes (a season pack grabbed for the
			// ones wanted, say) leaves the others' copies alone.
			if len(d.EpisodeIDs) > 0 && !slices.ContainsFunc(eps, func(e store.Episode) bool {
				return e.Season == season && slices.Contains(numbers, e.Number) && slices.Contains(d.EpisodeIDs, e.ID)
			}) {
				continue
			}
			ep := eps[i]
			code := fmt.Sprintf("S%02dE%02d", season, numbers[0])
			for _, n := range numbers[1:] {
				code += fmt.Sprintf("E%02d", n)
			}
			name := fmt.Sprintf("%s - %s", fileName(t.Title, t.Year), code)
			if ep.Title != "" {
				name += " - " + cleanFileName(ep.Title)
			}
			dest := filepath.Join(t.Path, fmt.Sprintf("Season %02d", season), name+strings.ToLower(filepath.Ext(v)))
			if err := li.place(ctx, dir, v, dest, t.RootFolder, t.Path, "episode", ep.ID, q, ep.File); err != nil {
				return "", err
			}
			placed = append(placed, dest)
		}
		if len(placed) == 0 {
			return "", errors.New("none of its video files, nor the release itself, are named for an episode of the show (like S01E02), and the videos' own tags don't say which episodes they are")
		}
		to := placed[0]
		if len(placed) > 1 {
			to = filepath.Dir(placed[0])
		}
		if len(left) > 0 {
			return to, &LeftOverError{To: to, Left: left}
		}
		return to, nil
	}
	return "", errors.New("it isn't for a movie, a show, a book or an album")
}

// ReadOnlyError means the library folder doesn't take writes, as when
// it's mounted read-only while Box Office runs next to other apps.
type ReadOnlyError struct{ Root string }

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("the library folder %s is read-only for Box Office", e.Root)
}

// checkLibrary makes sure the title's folder is inside its root folder,
// and that the root folder takes writes: a library mounted read-only
// stops the import here, before anything is touched.
func checkLibrary(root, path string) error {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if rel, err := filepath.Rel(root, path); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("the title's folder %s isn't inside its library folder %s", path, root)
	}
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("Box Office can't see the library folder %s", root)
	}
	if !writable(root) {
		return &ReadOnlyError{Root: root}
	}
	return nil
}

// place moves one file into the library, records it, and moves the copy it
// replaces to the root folder's .recycle folder. Nothing in the library is
// deleted or written over: a file already where the new one goes (an
// upgrade of a copy with the same name, say) is set aside first, and put
// back if the new one can't be moved in.
func (li *LibraryImporter) place(ctx context.Context, dir, rel, dest, root, titlePath, kind string, owner int64, q int, old *store.File) error {
	src := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	aside, err := arrapi.RecycleTo(root, dest)
	if err != nil {
		return fmt.Errorf("couldn't set aside the copy already at %s: %w", dest, err)
	}
	if err := move(src, dest); err != nil {
		// The library copy is whole and in place; only the original in
		// the downloads folder is left over, and the download folder's
		// cleanup takes another go at it.
		var left *sourceLeftError
		if !errors.As(err, &left) {
			if aside != "" {
				if _, serr := os.Lstat(dest); errors.Is(serr, fs.ErrNotExist) {
					rename(aside, dest)
				}
			}
			return err
		}
	}
	// Subtitles named like the video go along, setting aside any already
	// there.
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if entries, err := os.ReadDir(filepath.Dir(src)); err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if slices.Contains(subtitleExts, ext) && strings.HasPrefix(e.Name(), base) {
				extra := strings.TrimSuffix(strings.TrimPrefix(e.Name(), base), filepath.Ext(e.Name()))
				to := strings.TrimSuffix(dest, filepath.Ext(dest)) + extra + ext
				if _, err := arrapi.RecycleTo(root, to); err != nil {
					continue
				}
				move(filepath.Join(filepath.Dir(src), e.Name()), to)
			}
		}
	}
	st, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if kind == "edition" {
		_, err = li.Store.AddEditionFile(ctx, owner, dest, st.Size(), q)
		return err
	}
	if _, err := li.Store.AddFile(ctx, store.File{Kind: kind, OwnerID: owner, Path: dest, Size: st.Size(), Quality: q}); err != nil {
		return err
	}
	// A better or replacement copy asked for is in.
	if err := li.Store.ClearWant(ctx, kind, owner); err != nil {
		return err
	}
	// The copy this one replaces is set aside, if it was a different file
	// in the same title's folder.
	if old != nil && old.Path != dest {
		if rel, err := filepath.Rel(filepath.Clean(titlePath), old.Path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			if err := arrapi.Recycle(root, old.Path); err != nil {
				return fmt.Errorf("moved in, but couldn't set the old copy aside: %w", err)
			}
		}
	}
	return nil
}

// move renames a file, or copies it when it's going to another disk.
func move(src, dest string) error {
	err := rename(src, dest)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	// A network share can refuse to delete a file for a moment after it
	// was read (SMB reports it as an input/output error), so this takes a
	// few tries. The library copy is already whole by now, so failing
	// here doesn't undo the move.
	var rerr error
	for i := range removeTries {
		if i > 0 {
			time.Sleep(removeWait << (i - 1))
		}
		if rerr = remove(src); rerr == nil || errors.Is(rerr, fs.ErrNotExist) {
			return nil
		}
	}
	return &sourceLeftError{src, rerr}
}

// rename and remove are os.Rename and os.Remove, swapped out in tests.
var (
	rename      = os.Rename
	remove      = os.Remove
	removeTries = 5
	removeWait  = 250 * time.Millisecond
)

// sourceLeftError means a file went into the library, verified whole, but
// the original couldn't be removed from the downloads folder.
type sourceLeftError struct {
	src string
	err error
}

func (e *sourceLeftError) Error() string {
	return fmt.Sprintf("moved into the library, but couldn't remove %s: %v", e.src, e.err)
}

func (e *sourceLeftError) Unwrap() error { return e.err }

// copyFile copies src to dest through a temporary file, and only puts it
// at dest once it's checked whole. src is closed before it returns, so it
// can be removed.
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".boxoffice-part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(&flush.Writer{F: out}, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	// The copy must be whole before the original goes.
	if info, serr := in.Stat(); err == nil && (serr != nil || info.Size() != n) {
		err = fmt.Errorf("the copy of %s came out %d bytes, not %d", filepath.Base(src), n, info.Size())
	}
	if err == nil {
		if info, serr := os.Stat(tmp); serr != nil || info.Size() != n {
			err = fmt.Errorf("the copy of %s didn't write out whole", filepath.Base(src))
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// matchTags reads the unnamed main videos' tags and matches them to the
// show's episodes, in the season the release is named for when it's a
// season pack.
func (li *LibraryImporter) matchTags(ctx context.Context, d store.Download, dir string, vids []string, main map[string]bool, eps []store.Episode, runtime time.Duration) map[string]store.Episode {
	read := li.Tags
	if read == nil {
		read = ffprobeTags
	}
	tags := map[string]VideoTags{}
	for _, v := range vids {
		if !main[v] {
			continue
		}
		p := filepath.Join(dir, v)
		if t, err := read(ctx, p); err == nil && (t.Title != "" || t.Description != "" || t.Episode > 0) {
			if t.Duration == 0 {
				t.Duration, _ = screen.Duration(p)
			}
			tags[v] = t
		}
	}
	if len(tags) == 0 {
		return nil
	}
	if len(d.EpisodeIDs) > 0 {
		// Grabbed for some episodes: matched among that season's, so the
		// others in the pack are told apart too and left be.
		var seasons []int
		for _, e := range eps {
			if slices.Contains(d.EpisodeIDs, e.ID) && !slices.Contains(seasons, e.Season) {
				seasons = append(seasons, e.Season)
			}
		}
		if len(seasons) == 1 && releaseSeason(d.Name) == 0 {
			return matchPack(tags, eps, seasons[0], runtime)
		}
	}
	return matchPack(tags, eps, releaseSeason(d.Name), runtime)
}

// releaseEpisode is the episode a download is for, from its release name,
// or else from the one episode it was grabbed for.
func releaseEpisode(d store.Download, eps []store.Episode) (int, []int) {
	if s, n := episodeNumbers(d.Name); len(n) > 0 {
		return s, n
	}
	if len(d.EpisodeIDs) == 1 {
		if i := slices.IndexFunc(eps, func(e store.Episode) bool { return e.ID == d.EpisodeIDs[0] }); i >= 0 {
			return eps[i].Season, []int{eps[i].Number}
		}
	}
	return 0, nil
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

func fileName(title string, year int) string {
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
