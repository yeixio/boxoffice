// Package library deletes files from the media library when someone asks
// it to: a whole movie's or show's folder when the title is removed, or
// the files of a season, an episode or a movie while the title stays on
// the list. Deleting frees the space for good; there's no recycle bin for
// it. So every deletion is planned first, shown to the person with what it
// takes (files, size), and only carried out as planned: Delete refuses if
// what's on disk has changed since.
//
// It also looks after the library's .recycle folders, where copies
// replaced by upgrades are set aside.
package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// RecycleDir is the folder in each library root where replaced copies go.
const RecycleDir = ".recycle"

// MaxListed is how many files a plan names; it counts them all.
const MaxListed = 200

// File is one file a plan deletes.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Plan is what a deletion takes off the disk.
type Plan struct {
	// Folder is set when the whole folder goes, with everything in it.
	Folder string `json:"folder,omitempty"`
	// Files names up to MaxListed of the files, biggest first.
	Files []File `json:"files"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
	// Problem says why nothing can be deleted; the rest of the plan still
	// says what's there.
	Problem string `json:"problem,omitempty"`

	root, titleFolder string
	paths             []string
}

// Matches says the plan takes what the person was shown.
func (p Plan) Matches(count int, size int64) bool { return p.Count == count && p.Size == size }

// inside says path is strictly inside dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// checkRoot says why files under root can't be deleted, or "".
func checkRoot(root string) string {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return "the title has no library folder Box Office knows"
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return "Box Office can't see the library folder " + root
	}
	if !writable(root) {
		return root + " is read-only for Box Office, so it can't delete anything there"
	}
	return ""
}

// FolderPlan plans deleting a title's whole folder. kind is movie or
// series, and id the title's, so other titles' files are never in it.
func FolderPlan(ctx context.Context, st *store.Store, kind string, id int64, root, folder string) (Plan, error) {
	p := Plan{Files: []File{}, root: root, titleFolder: folder}
	if !inside(root, folder) {
		p.Problem = fmt.Sprintf("its folder %s isn't inside its library folder %s", folder, root)
		return p, nil
	}
	if rel, _ := filepath.Rel(root, folder); strings.SplitN(rel, string(filepath.Separator), 2)[0] == RecycleDir {
		p.Problem = "its folder is in the recycle folder"
		return p, nil
	}
	fi, err := os.Lstat(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil // nothing on disk
	}
	if err != nil {
		p.Problem = "Box Office can't read its folder: " + err.Error()
		return p, nil
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		p.Problem = folder + " isn't a plain folder, so Box Office leaves it alone"
		return p, nil
	}
	roots, err := st.RootFolders(ctx, "")
	if err != nil {
		return p, err
	}
	for _, r := range roots {
		if filepath.Clean(r.Path) == filepath.Clean(folder) || inside(folder, r.Path) {
			p.Problem = folder + " is a library folder itself"
			return p, nil
		}
	}
	if other, err := st.OtherTitleIn(ctx, folder, kind, id); err != nil {
		return p, err
	} else if other != "" {
		p.Problem = fmt.Sprintf("%s is in the same folder, so deleting it would take that too", other)
		return p, nil
	}
	var files []File
	err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, File{path, info.Size()})
		return nil
	})
	if err != nil {
		p.Problem = "Box Office can't read everything in its folder: " + err.Error()
		return p, nil
	}
	p.Folder = folder
	p.add(files)
	if p.Problem == "" {
		p.Problem = checkRoot(root)
	}
	return p, nil
}

// FilesPlan plans deleting a title's files (a movie's, an episode's, a
// season's) while the title stays: each file goes with the files named
// after it, such as its subtitles, artwork and .nfo, but never another
// video. Files outside titleFolder are left alone.
func FilesPlan(root, titleFolder string, tracked []store.File) Plan {
	p := Plan{Files: []File{}, root: root, titleFolder: titleFolder}
	var files []File
	seen := map[string]bool{}
	for _, f := range tracked {
		seen[filepath.Clean(f.Path)] = true
	}
	for _, f := range tracked {
		path := filepath.Clean(f.Path)
		if !inside(root, path) || !inside(titleFolder, path) {
			p.Problem = fmt.Sprintf("%s isn't inside the title's folder %s", path, titleFolder)
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // already gone
		}
		if err != nil {
			p.Problem = "Box Office can't read " + path + ": " + err.Error()
			continue
		}
		if !info.Mode().IsRegular() {
			p.Problem = path + " isn't a plain file, so Box Office leaves it alone"
			continue
		}
		files = append(files, File{path, info.Size()})
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		entries, _ := os.ReadDir(filepath.Dir(path))
		for _, e := range entries {
			name := e.Name()
			other := filepath.Join(filepath.Dir(path), name)
			if !companion(stem, name) || seen[other] || !e.Type().IsRegular() {
				continue
			}
			if info, err := e.Info(); err == nil {
				seen[other] = true
				files = append(files, File{other, info.Size()})
			}
		}
	}
	p.add(files)
	if p.Problem == "" && p.Count > 0 {
		p.Problem = checkRoot(root)
	}
	return p
}

var videoExts = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".wmv", ".mov", ".mpg", ".mpeg", ".webm"}

// companion says name goes with a video called stem: "stem.en.srt",
// "stem.nfo", "stem-thumb.jpg". Another video never does.
func companion(stem, name string) bool {
	if !strings.HasPrefix(name, stem) || len(name) == len(stem) {
		return false
	}
	if slices.Contains(videoExts, strings.ToLower(filepath.Ext(name))) {
		return false
	}
	c := name[len(stem)]
	return c == '.' || c == '-'
}

func (p *Plan) add(files []File) {
	slices.SortFunc(files, func(a, b File) int {
		if a.Size != b.Size {
			if a.Size > b.Size {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	for _, f := range files {
		p.Count++
		p.Size += f.Size
		p.paths = append(p.paths, f.Path)
	}
	p.Files = files[:min(len(files), MaxListed)]
}

// Delete carries out the plan. A file that's already gone is fine.
func Delete(p Plan) error {
	if p.Problem != "" {
		return errors.New(p.Problem)
	}
	if p.Folder != "" {
		return retry(func() error { return os.RemoveAll(p.Folder) })
	}
	dirs := map[string]bool{}
	for _, path := range p.paths {
		if err := retry(func() error { return os.Remove(path) }); err != nil {
			return err
		}
		dirs[filepath.Dir(path)] = true
	}
	// A season folder left empty goes too; the title's folder stays.
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

// Tries and wait for deleting: a network share can refuse for a moment
// (SMB reports it as an input/output error).
var (
	removeTries = 4
	removeWait  = 500 * time.Millisecond
)

func retry(f func() error) error {
	var err error
	for i := range removeTries {
		if i > 0 {
			time.Sleep(removeWait << (i - 1))
		}
		if err = f(); err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if !errors.Is(err, syscall.EIO) && !errors.Is(err, syscall.EBUSY) {
			return err
		}
	}
	return err
}

// Bytes writes a size the way the web UI does.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
