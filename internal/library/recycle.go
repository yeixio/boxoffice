package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Box Office sets replaced copies aside in <root>/.recycle/<YYYY-MM-DD>/.
var dayFolder = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Recycle is what's in one library root's recycle folder.
type Recycle struct {
	Root   string `json:"root"`
	Folder string `json:"folder"`
	Count  int    `json:"count"`
	Size   int64  `json:"size"`
	// Oldest is the day the oldest copy in it was set aside.
	Oldest string `json:"oldest,omitempty"`
}

// RecycleUsage says what's in each root's recycle folder; roots without
// one are left out.
func RecycleUsage(roots []string) []Recycle {
	out := []Recycle{}
	for _, root := range roots {
		dir := filepath.Join(root, RecycleDir)
		days, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		r := Recycle{Root: root, Folder: dir}
		var names []string
		for _, d := range days {
			if d.IsDir() && dayFolder.MatchString(d.Name()) {
				names = append(names, d.Name())
				n, size := usage(filepath.Join(dir, d.Name()))
				r.Count += n
				r.Size += size
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			r.Oldest = names[0]
		}
		out = append(out, r)
	}
	return out
}

func usage(dir string) (n int, size int64) {
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n++
				size += info.Size()
			}
		}
		return nil
	})
	return n, size
}

// EmptyRecycle deletes, for good, the copies set aside in root's recycle
// folder before the given day (all of them when before is zero). Only the
// day folders Box Office makes are touched. It says how much it freed.
func EmptyRecycle(root string, before time.Time) (freed int64, err error) {
	dir := filepath.Join(root, RecycleDir)
	days, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cut := ""
	if !before.IsZero() {
		cut = before.UTC().Format(time.DateOnly)
	}
	for _, d := range days {
		if !d.IsDir() || !dayFolder.MatchString(d.Name()) || cut != "" && d.Name() >= cut {
			continue
		}
		path := filepath.Join(dir, d.Name())
		_, size := usage(path)
		if err := retry(func() error { return os.RemoveAll(path) }); err != nil {
			return freed, err
		}
		freed += size
	}
	return freed, nil
}

// RecyclePlan carries out a files plan by moving its files into the root's
// recycle folder, where they stay until it's emptied, so they can be put
// back. A file that's already gone is fine. A season folder left empty
// goes; the title's folder stays.
func RecyclePlan(p Plan) error {
	if p.Problem != "" {
		return errors.New(p.Problem)
	}
	if p.Folder != "" {
		return errors.New("a whole folder isn't moved to the recycle folder")
	}
	if len(p.paths) == 0 {
		return nil
	}
	dir := filepath.Join(p.root, RecycleDir, time.Now().UTC().Format(time.DateOnly))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dirs := map[string]bool{}
	for _, path := range p.paths {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		name := filepath.Base(path)
		dest := filepath.Join(dir, name)
		for i := 1; ; i++ {
			if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
				break
			}
			ext := filepath.Ext(name)
			dest = filepath.Join(dir, fmt.Sprintf("%s.%d%s", strings.TrimSuffix(name, ext), i, ext))
		}
		if err := retry(func() error { return os.Rename(path, dest) }); err != nil {
			return err
		}
		dirs[filepath.Dir(path)] = true
	}
	for d := range dirs {
		for inside(p.titleFolder, d) {
			if os.Remove(d) != nil {
				break
			}
			d = filepath.Dir(d)
		}
	}
	return nil
}
