package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
