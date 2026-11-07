package problems

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/flush"
	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
)

// Roles of the files a fix touched, beyond the store's aside, moved and
// made.
const (
	roleRecord     = "record"      // recorded as a title's copy
	roleForgot     = "forgot"      // a record dropped
	roleRemovedDir = "removed_dir" // an empty folder removed
)

// ErrChanged means what a problem is about changed on disk since it was
// found, so the fix isn't carried out.
var ErrChanged = errors.New("changed since Box Office looked")

// fixer carries out one fix.
type fixer struct {
	st    *store.Store
	tools func() (Tools, error)
	p     Problem
	stamp string // the folder in .recycle/problem-files for this fix
	done  []store.ProblemFixFile
}

func (x *fixer) recycleDir() string {
	return filepath.Join(x.p.Root, ".recycle", RecycleSub, x.stamp)
}

// safe says path is a place in the root a fix may touch: inside it, and
// not in its recycle folder.
func (x *fixer) safe(path string) error {
	if !inside(x.p.Root, path) {
		return fmt.Errorf("%s isn't inside the library folder %s", path, x.p.Root)
	}
	if rel, _ := filepath.Rel(x.p.Root, path); strings.HasPrefix(rel, ".recycle") {
		return fmt.Errorf("%s is in the recycle folder", path)
	}
	return nil
}

// check makes sure the problem's files are still as they were found.
func (x *fixer) check() error {
	if !writable(x.p.Root) {
		return fmt.Errorf("the library folder %s is read-only for Box Office", x.p.Root)
	}
	if x.p.Title != nil {
		if err := x.st.CheckUnlocked(context.Background(), x.p.Title.Kind, x.p.Title.ID); err != nil {
			return errors.New("the title is on Do not touch")
		}
	}
	for _, f := range x.p.Files {
		if err := x.safe(f.Path); err != nil {
			return err
		}
		info, err := os.Lstat(f.Path)
		if x.p.Kind == Missing {
			if err == nil {
				return fmt.Errorf("%s is back: %w", filepath.Base(f.Path), ErrChanged)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%s is gone: %w", filepath.Base(f.Path), ErrChanged)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link, so Box Office leaves it alone", f.Path)
		}
		if f.Dir {
			if !info.IsDir() {
				return fmt.Errorf("%s isn't a folder any more: %w", filepath.Base(f.Path), ErrChanged)
			}
			if x.p.Kind == EmptyDir {
				if list, _ := os.ReadDir(f.Path); len(list) > 0 {
					return fmt.Errorf("%s isn't empty any more: %w", filepath.Base(f.Path), ErrChanged)
				}
			}
			continue
		}
		if info.Size() != f.Size {
			return fmt.Errorf("%s changed size: %w", filepath.Base(f.Path), ErrChanged)
		}
	}
	return nil
}

// Apply carries out a problem's fix and records it. keep, for keep_one,
// is the copy to keep ("" for the one the fix suggests).
func Apply(ctx context.Context, st *store.Store, tools func() (Tools, error), p Problem, keep string, progress Progress) (store.ProblemFix, error) {
	if p.Fix == nil {
		return store.ProblemFix{}, errors.New("there's nothing to do for it")
	}
	x := &fixer{st: st, tools: tools, p: p, stamp: time.Now().UTC().Format("2006-01-02-150405") + "-" + p.ID[:6]}
	if err := x.check(); err != nil {
		return store.ProblemFix{}, err
	}
	var err error
	switch p.Fix.Action {
	case ActSetAside:
		err = x.setAsideAll(ctx, p.Fix.Paths)
	case ActRemoveDirs:
		for _, d := range p.Fix.Paths {
			if err = os.Remove(d); err != nil {
				break
			}
			x.done = append(x.done, store.ProblemFixFile{Role: roleRemovedDir, Src: d, Dest: d})
		}
	case ActForget:
		err = x.forget(ctx, p.Fix.Paths)
	case ActRename:
		err = x.moveAll(ctx, p.Fix.Moves)
	case ActRecord:
		err = x.record(ctx)
	case ActKeepOne:
		err = x.keepOne(ctx, keep)
	case ActConvert, ActJoin:
		err = x.remux(ctx, progress)
	default:
		err = fmt.Errorf("no such fix %q", p.Fix.Action)
	}
	if err != nil {
		// Whatever was done before it failed is put back.
		if len(x.done) > 0 {
			undo(ctx, st, p.Root, x.done)
		}
		return store.ProblemFix{}, err
	}
	title := ""
	if p.Title != nil {
		title = FileName(p.Title.Name, p.Title.Year)
		if p.Detail != "" && strings.HasPrefix(p.Detail, "S") {
			title += " " + p.Detail
		}
	} else if len(p.Files) > 0 {
		title = Repair(filepath.Base(p.Files[0].Path))
	}
	fix, err := st.AddProblemFix(ctx, store.ProblemFix{Action: p.Fix.Action, Kind: p.Kind, Detail: p.Detail, Title: title, Root: p.Root, State: store.FixWaiting, Files: x.done})
	if err != nil {
		return fix, fmt.Errorf("done, but couldn't note it to put it back later: %w", err)
	}
	return fix, nil
}

// aside moves a file or folder into this fix's recycle folder, keeping
// where it was inside the root.
func (x *fixer) aside(ctx context.Context, path string) error {
	if err := x.safe(path); err != nil {
		return err
	}
	rel, err := filepath.Rel(x.p.Root, path)
	if err != nil {
		return err
	}
	dest := filepath.Join(x.recycleDir(), rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := moveFile(path, dest, info.IsDir()); err != nil {
		return err
	}
	ff := store.ProblemFixFile{Role: store.FixAside, Src: path, Dest: dest, Size: info.Size()}
	if !info.IsDir() {
		if rec, err := x.st.FileByPath(ctx, path); err == nil {
			ff.Record = &rec
			if err := x.st.ForgetFileAt(ctx, path); err != nil {
				return err
			}
		}
		// Subtitles named like a video go with it.
		if isVideo(path) {
			for _, m := range withSubtitles(path, path)[1:] {
				if err := x.aside(ctx, m.From); err != nil {
					return err
				}
			}
		}
	} else {
		_, ff.Size = dirSize(dest)
	}
	x.done = append(x.done, ff)
	return nil
}

func (x *fixer) setAsideAll(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue // a subtitle that went along with its video
		}
		if err := x.aside(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (x *fixer) forget(ctx context.Context, paths []string) error {
	for _, p := range paths {
		rec, err := x.st.FileByPath(ctx, p)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := x.st.ForgetFileAt(ctx, p); err != nil {
			return err
		}
		x.done = append(x.done, store.ProblemFixFile{Role: roleForgot, Src: p, Dest: p, Size: rec.Size, Record: &rec})
	}
	return nil
}

// moveAll renames files, or a title's folder, and their records.
func (x *fixer) moveAll(ctx context.Context, moves []Move) error {
	for _, m := range moves {
		if err := x.safe(m.From); err != nil {
			return err
		}
		if err := x.safe(m.To); err != nil {
			return err
		}
		if _, err := os.Lstat(m.From); errors.Is(err, fs.ErrNotExist) && m.From != moves[0].From {
			continue // a subtitle gone since
		}
		// Only a change of case may land where something is.
		if _, err := os.Lstat(m.To); err == nil && !strings.EqualFold(m.From, m.To) {
			return fmt.Errorf("%s is there already", m.To)
		}
		if err := os.MkdirAll(filepath.Dir(m.To), 0o755); err != nil {
			return err
		}
		if err := os.Rename(m.From, m.To); err != nil {
			return err
		}
		ff := store.ProblemFixFile{Role: store.FixMoved, Src: m.From, Dest: m.To}
		if m.Folder {
			if x.p.Title == nil {
				return errors.New("a folder move needs its title")
			}
			if err := x.st.MoveTitleFolder(ctx, x.p.Title.Kind, x.p.Title.ID, m.From, m.To); err != nil {
				os.Rename(m.To, m.From)
				return err
			}
			ff.Record = &store.File{Kind: "folder:" + x.p.Title.Kind, OwnerID: x.p.Title.ID}
		} else {
			info, _ := os.Stat(m.To)
			if info != nil {
				ff.Size = info.Size()
			}
			if err := x.st.MoveFileRecord(ctx, m.From, m.To, ff.Size); err != nil {
				return err
			}
		}
		x.done = append(x.done, ff)
		// A season folder or the like left empty by the move goes.
		if dir := filepath.Dir(m.From); !m.Folder && dir != x.p.Root && inside(x.p.Root, dir) {
			if list, err := os.ReadDir(dir); err == nil && len(list) == 0 && !slices.ContainsFunc(moves, func(o Move) bool { return filepath.Dir(o.To) == dir }) {
				if os.Remove(dir) == nil {
					x.done = append(x.done, store.ProblemFixFile{Role: roleRemovedDir, Src: dir, Dest: dir})
				}
			}
		}
	}
	return nil
}

// owner is the movie or episode a file is the copy of.
func (x *fixer) owner(path string) (kind string, id int64, err error) {
	if x.p.Title == nil {
		return "", 0, errors.New("it isn't any title's")
	}
	if x.p.Title.Kind == "movie" {
		return "movie", x.p.Title.ID, nil
	}
	for _, f := range x.p.Files {
		if f.Path != path {
			continue
		}
		if f.episode != nil {
			return "episode", f.episode.ID, nil
		}
		// A problem from a saved look has only the episode's S01E02.
		if season, numbers := episodeNumbers(f.Episode); len(numbers) > 0 {
			eps, err := x.st.Episodes(context.Background(), x.p.Title.ID)
			if err != nil {
				return "", 0, err
			}
			for _, e := range eps {
				if e.Season == season && e.Number == numbers[0] {
					return "episode", e.ID, nil
				}
			}
		}
	}
	return "", 0, errors.New("it isn't named for an episode")
}

// recordAs records path as its title's copy, noting the record it
// replaces, if any.
func (x *fixer) recordAs(ctx context.Context, kind string, id int64, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var prev *store.File
	if kind == "movie" {
		if m, err := x.st.Movie(ctx, id); err == nil {
			prev = m.File
		}
	} else if e, err := x.st.Episode(ctx, id); err == nil {
		prev = e.File
	}
	if prev != nil && prev.Path != path {
		x.done = append(x.done, store.ProblemFixFile{Role: roleForgot, Src: prev.Path, Dest: prev.Path, Size: prev.Size, Record: prev})
	}
	if prev != nil && prev.Path == path {
		return nil
	}
	if _, err := x.st.AddFile(ctx, store.File{Kind: kind, OwnerID: id, Path: path, Size: info.Size(), Quality: quality.Parse(filepath.Base(path)).ID}); err != nil {
		return err
	}
	x.done = append(x.done, store.ProblemFixFile{Role: roleRecord, Src: path, Dest: path, Size: info.Size()})
	return nil
}

func (x *fixer) record(ctx context.Context) error {
	path := x.p.Files[0].Path
	kind, id, err := x.owner(path)
	if err != nil {
		return err
	}
	if len(x.p.Fix.Moves) > 0 {
		if err := x.moveAll(ctx, x.p.Fix.Moves); err != nil {
			return err
		}
		path = x.p.Fix.Moves[0].To
	}
	return x.recordAs(ctx, kind, id, path)
}

func (x *fixer) keepOne(ctx context.Context, keep string) error {
	if keep == "" {
		keep = x.p.Fix.Keep
	}
	if !slices.ContainsFunc(x.p.Files, func(f File) bool { return f.Path == keep }) {
		return errors.New("the copy to keep isn't one of them")
	}
	kind, id, err := x.owner(keep)
	if err != nil {
		return err
	}
	for _, f := range x.p.Files {
		if f.Path != keep {
			if err := x.aside(ctx, f.Path); err != nil {
				return err
			}
		}
	}
	return x.recordAs(ctx, kind, id, keep)
}

func (x *fixer) remux(ctx context.Context, progress Progress) error {
	tools, err := x.tools()
	if err != nil {
		return err
	}
	out := x.p.Fix.Output
	if err := x.safe(out); err != nil {
		return err
	}
	if x.p.Fix.Action == ActConvert {
		err = tools.Convert(ctx, x.p.Fix.Paths[0], out, progress)
	} else {
		err = tools.Join(ctx, x.p.Fix.Paths, out, progress)
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	x.done = append(x.done, store.ProblemFixFile{Role: store.FixMade, Src: out, Dest: out, Size: info.Size()})
	// Only once the new file is whole and checked do the originals go.
	if err := x.setAsideAll(ctx, x.p.Fix.Paths); err != nil {
		return err
	}
	if x.p.Title != nil && x.p.Title.Kind == "movie" {
		return x.recordAs(ctx, "movie", x.p.Title.ID, out)
	}
	return nil
}

// PutBack undoes a fix: the originals go back where they were, with their
// records, renames are turned back, and a file the fix made is set aside
// in the recycle folder.
func PutBack(ctx context.Context, st *store.Store, id int64) error {
	fix, err := st.ProblemFix(ctx, id)
	if err != nil {
		return err
	}
	if fix.State != store.FixWaiting {
		return errors.New("it's been decided already")
	}
	// Nothing is put back over something that's there now.
	for _, f := range fix.Files {
		if f.Role == store.FixAside {
			if _, err := os.Lstat(f.Src); err == nil {
				return fmt.Errorf("there's something at %s now, so %s can't go back there", f.Src, filepath.Base(f.Src))
			}
			if _, err := os.Lstat(f.Dest); err != nil {
				return fmt.Errorf("%s isn't in the recycle folder any more", filepath.Base(f.Src))
			}
		}
	}
	if err := undo(ctx, st, fix.Root, fix.Files); err != nil {
		return err
	}
	cleanRecycle(fix)
	return st.SetProblemFixState(ctx, id, store.FixUndone)
}

// undo turns back what a fix did, last first.
func undo(ctx context.Context, st *store.Store, root string, files []store.ProblemFixFile) error {
	var errs []error
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		var err error
		switch f.Role {
		case store.FixMade:
			if _, serr := os.Lstat(f.Dest); serr == nil {
				_, err = arrapi.RecycleTo(root, f.Dest)
			}
			if err == nil {
				err = st.ForgetFileAt(ctx, f.Dest)
			}
		case roleRecord:
			err = st.ForgetFileAt(ctx, f.Dest)
		case roleForgot:
			if f.Record != nil {
				err = st.RestoreFile(ctx, *f.Record)
			}
		case roleRemovedDir:
			err = os.MkdirAll(f.Src, 0o755)
		case store.FixMoved:
			if err = os.MkdirAll(filepath.Dir(f.Src), 0o755); err == nil {
				err = os.Rename(f.Dest, f.Src)
			}
			if err == nil {
				if f.Record != nil && strings.HasPrefix(f.Record.Kind, "folder:") {
					err = st.MoveTitleFolder(ctx, strings.TrimPrefix(f.Record.Kind, "folder:"), f.Record.OwnerID, f.Dest, f.Src)
				} else {
					err = st.MoveFileRecord(ctx, f.Dest, f.Src, f.Size)
				}
			}
		case store.FixAside:
			if err = os.MkdirAll(filepath.Dir(f.Src), 0o755); err == nil {
				info, _ := os.Lstat(f.Dest)
				err = moveFile(f.Dest, f.Src, info != nil && info.IsDir())
			}
			if err == nil && f.Record != nil {
				err = st.RestoreFile(ctx, *f.Record)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f.Src), err))
		}
	}
	return errors.Join(errs...)
}

// Reason fills in, for a fix from before the kind of problem it fixed was
// noted, the kind it most likely was, from what it did and to what; and
// repairs its title, if garbled.
func Reason(f *store.ProblemFix) {
	f.Title = Repair(f.Title)
	if f.Kind != "" {
		return
	}
	switch f.Action {
	case ActKeepOne:
		f.Kind = Duplicate
	case ActJoin:
		f.Kind = Parts
	case ActConvert:
		f.Kind = DiscImage
	case ActRename:
		f.Kind = Naming
	case ActRecord:
		f.Kind = Untracked
	case ActForget:
		f.Kind = Missing
	case ActRemoveDirs:
		f.Kind = EmptyDir
	case ActSetAside:
		f.Kind = NotListed
		var aside []store.ProblemFixFile
		for _, ff := range f.Files {
			if ff.Role == store.FixAside {
				aside = append(aside, ff)
			}
		}
		if len(aside) == 0 {
			return
		}
		src := aside[0].Src
		switch name := filepath.Base(src); {
		case isLeftover(name):
			// Subtitles named like a sample go along with it.
			f.Kind = Leftover
		case hasExt(name, discExts):
			f.Kind = DiscImage
		case filepath.Dir(src) != filepath.Clean(f.Root) && isVideo(name):
			// A video inside a folder, set aside alone: one in a show's
			// folder that wasn't an episode.
			f.Kind = Unknown
		}
	}
}

// DeleteForGood deletes the originals a fix set aside, and marks it done.
func DeleteForGood(ctx context.Context, st *store.Store, id int64) (freed int64, err error) {
	fix, err := st.ProblemFix(ctx, id)
	if err != nil {
		return 0, err
	}
	if fix.State != store.FixWaiting {
		return 0, errors.New("it's been decided already")
	}
	base := filepath.Join(fix.Root, ".recycle", RecycleSub) + string(filepath.Separator)
	for _, f := range fix.Files {
		if f.Role != store.FixAside {
			continue
		}
		// Only what's in the fixes' recycle folder is ever deleted.
		if !strings.HasPrefix(filepath.Clean(f.Dest), base) {
			return freed, fmt.Errorf("%s isn't in the recycle folder", f.Dest)
		}
		if err := os.RemoveAll(f.Dest); err != nil {
			return freed, err
		}
		freed += f.Size
	}
	cleanRecycle(fix)
	return freed, st.SetProblemFixState(ctx, id, store.FixDone)
}

// cleanRecycle removes the fix's recycle folders left empty.
func cleanRecycle(fix store.ProblemFix) {
	base := filepath.Join(fix.Root, ".recycle", RecycleSub)
	for _, f := range fix.Files {
		if f.Role != store.FixAside {
			continue
		}
		for dir := filepath.Dir(f.Dest); inside(base, dir); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	os.Remove(base)
}

// moveFile renames, or for a file on another disk, copies and checks it
// before removing the original.
func moveFile(src, dest string, dir bool) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s is there already", dest)
	}
	err := os.Rename(src, dest)
	if err == nil || !errors.Is(err, syscall.EXDEV) || dir {
		return err
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".boxoffice-part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
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
	if info, serr := in.Stat(); err == nil && (serr != nil || info.Size() != n) {
		err = fmt.Errorf("the copy of %s came out short", filepath.Base(src))
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
