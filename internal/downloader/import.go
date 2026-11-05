package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// LibraryImporter moves finished downloads into the titles' folders.
type LibraryImporter struct {
	Store *store.Store
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
		name := fileName(m.Title, m.Year) + strings.ToLower(filepath.Ext(vids[0]))
		dest := filepath.Join(m.Path, name)
		if err := li.place(ctx, dir, vids[0], dest, m.RootFolder, m.Path, "movie", m.ID, q, m.File); err != nil {
			return "", err
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
		var placed []string
		for _, v := range vids {
			season, numbers := episodeNumbers(filepath.Base(v))
			if len(numbers) == 0 {
				continue
			}
			i := slices.IndexFunc(eps, func(e store.Episode) bool { return e.Season == season && e.Number == numbers[0] })
			if i < 0 {
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
			return "", errors.New("none of its video files are named for an episode of the show (like S01E02)")
		}
		if len(placed) == 1 {
			return placed[0], nil
		}
		return filepath.Dir(placed[0]), nil
	}
	return "", errors.New("it isn't for a movie or a show")
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
		return fmt.Errorf("the library folder %s is read-only for Box Office, so nothing was moved; the download is still in the downloads folder", root)
	}
	return nil
}

// place moves one file into the library, records it, and moves the copy it
// replaces to the root folder's .recycle folder.
func (li *LibraryImporter) place(ctx context.Context, dir, rel, dest, root, titlePath, kind string, owner int64, q int, old *store.File) error {
	src := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := move(src, dest); err != nil {
		return err
	}
	// Subtitles named like the video go along.
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if entries, err := os.ReadDir(filepath.Dir(src)); err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if slices.Contains(subtitleExts, ext) && strings.HasPrefix(e.Name(), base) {
				extra := strings.TrimSuffix(strings.TrimPrefix(e.Name(), base), filepath.Ext(e.Name()))
				move(filepath.Join(filepath.Dir(src), e.Name()), strings.TrimSuffix(dest, filepath.Ext(dest))+extra+ext)
			}
		}
	}
	st, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if _, err := li.Store.AddFile(ctx, store.File{Kind: kind, OwnerID: owner, Path: dest, Size: st.Size(), Quality: q}); err != nil {
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
	err := os.Rename(src, dest)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
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
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	return os.Remove(src)
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
