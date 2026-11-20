package relocate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
)

// Pick is what someone asked to move: some titles, or every title in a
// root folder (the whole library on a drive), to another root folder of
// the same kind.
type Pick struct {
	Kind string  `json:"kind"` // movie or series
	IDs  []int64 `json:"ids,omitempty"`
	// From is a root folder: every title in it.
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}

// Title is one title in a plan.
type Title struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Root and From are its root folder and folder now; To is its folder
	// after the move, named the same as now.
	Root  string `json:"root"`
	From  string `json:"from"`
	To    string `json:"to"`
	Files int    `json:"files"`
	Size  int64  `json:"size"`
	// Problem says why it can't be moved.
	Problem string `json:"problem,omitempty"`
	// Notes are things worth knowing, such as a second copy kept in
	// another folder that stays there.
	Notes []string `json:"notes,omitempty"`

	sameDisk bool
}

// Plan is what moving a Pick would do.
type Plan struct {
	Kind string `json:"kind"`
	To   string `json:"to"`
	// Titles are the titles that would move (up to MaxListed of them),
	// and Count, Files and Size add up all of them.
	Titles []Title `json:"titles"`
	Count  int     `json:"count"`
	Files  int     `json:"files"`
	Size   int64   `json:"size"`
	// Skipped are the picked titles that can't be moved, each with why.
	Skipped []Title `json:"skipped"`
	// FreeSpace is free on the new folder's disk; Need is how much the
	// move takes there (titles already on that disk take nothing).
	FreeSpace int64 `json:"freeSpace"`
	Need      int64 `json:"need"`
	// Problem says why nothing can be moved.
	Problem string `json:"problem,omitempty"`

	all []Title
}

// MaxListed is how many titles a plan lists by name.
const MaxListed = 500

// Reserve is space left free on the new disk: a move never fills it.
const Reserve = 1 << 30

// Matches says the plan moves what the person was shown.
func (p Plan) Matches(count int, size int64) bool { return p.Count == count && p.Size == size }

// ErrBadPick is a pick that makes no sense, such as an unknown folder.
type ErrBadPick struct{ Why string }

func (e ErrBadPick) Error() string { return e.Why }

// Plan works out what moving p would do. Nothing is changed.
func (s *Service) Plan(ctx context.Context, p Pick) (Plan, error) {
	if p.Kind != "movie" && p.Kind != "series" {
		return Plan{}, ErrBadPick{"only movies and shows can be moved for now"}
	}
	roots, err := s.Store.RootFolders(ctx, p.Kind)
	if err != nil {
		return Plan{}, err
	}
	to := filepath.Clean(p.To)
	known := func(path string) bool {
		for _, r := range roots {
			if filepath.Clean(r.Path) == filepath.Clean(path) {
				return true
			}
		}
		return false
	}
	if p.To == "" || !known(to) {
		return Plan{}, ErrBadPick{"pick one of the root folders to move them to"}
	}
	if p.From != "" && !known(p.From) {
		return Plan{}, ErrBadPick{"that isn't one of the root folders"}
	}
	if p.From == "" && len(p.IDs) == 0 {
		return Plan{}, ErrBadPick{"pick at least one title"}
	}
	plan := Plan{Kind: p.Kind, To: to, Titles: []Title{}, Skipped: []Title{}}
	plan.Problem = checkTarget(to)
	ids := p.IDs
	if p.From != "" {
		if ids, err = s.titlesIn(ctx, p.Kind, filepath.Clean(p.From)); err != nil {
			return plan, err
		}
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return plan, ctx.Err()
		}
		t, err := s.check(ctx, p.Kind, id, to, false)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return plan, err
		}
		if t.Problem != "" {
			plan.Skipped = append(plan.Skipped, t)
			continue
		}
		plan.all = append(plan.all, t)
		plan.Count++
		plan.Files += t.Files
		plan.Size += t.Size
		if !t.sameDisk {
			plan.Need += t.Size
		}
		if len(plan.Titles) < MaxListed {
			plan.Titles = append(plan.Titles, t)
		}
	}
	plan.FreeSpace = arrapi.FreeSpace(to)
	if plan.Problem == "" && plan.Need > 0 && plan.FreeSpace > 0 && plan.Need > plan.FreeSpace-Reserve {
		plan.Problem = fmt.Sprintf("there isn't room for them: they take %s, and %s has %s free", bytes(plan.Need), to, bytes(plan.FreeSpace))
	}
	if plan.Problem == "" && plan.Count == 0 {
		plan.Problem = "none of them can be moved"
	}
	return plan, nil
}

// checkTarget says why nothing can be moved to the root folder to, or "".
func checkTarget(to string) string {
	st, err := os.Stat(to)
	if err != nil || !st.IsDir() {
		return "Box Office can't see " + to + " right now"
	}
	if !writable(to) {
		return to + " is read-only for Box Office, so nothing can be moved there"
	}
	return ""
}

func (s *Service) titlesIn(ctx context.Context, kind, root string) ([]int64, error) {
	var ids []int64
	if kind == "movie" {
		list, err := s.Store.Movies(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range list {
			if filepath.Clean(m.RootFolder) == root {
				ids = append(ids, m.ID)
			}
		}
		return ids, nil
	}
	list, err := s.Store.SeriesAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if filepath.Clean(t.RootFolder) == root {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

// check plans moving one title to the root folder to, from what's on
// disk now. running is set when it's the move itself checking, just
// before it starts.
func (s *Service) check(ctx context.Context, kind string, id int64, to string, running bool) (Title, error) {
	var t Title
	var secondPath string
	switch kind {
	case "movie":
		m, err := s.Store.Movie(ctx, id)
		if err != nil {
			return t, err
		}
		t = Title{ID: m.ID, Title: m.Title, Root: m.RootFolder, From: m.Path}
		if m.Second != nil {
			secondPath = m.Second.Path
		}
	case "series":
		x, err := s.Store.Series(ctx, id)
		if err != nil {
			return t, err
		}
		t = Title{ID: x.ID, Title: x.Title, Root: x.RootFolder, From: x.Path}
	default:
		return t, store.ErrNotFound
	}
	t.Root, t.From = filepath.Clean(t.Root), filepath.Clean(t.From)
	t.To = filepath.Join(to, filepath.Base(t.From))
	t.Problem = s.problem(ctx, kind, &t, to, running)
	if t.Problem != "" {
		return t, nil
	}
	if secondPath != "" && !inside(t.From, secondPath) {
		t.Notes = append(t.Notes, "its second copy stays where it is, in "+filepath.Dir(secondPath))
	}
	links, err := s.Store.FolderLinks(ctx)
	if err != nil {
		return t, err
	}
	for _, l := range links {
		if l.Kind == kind && l.TitleID == id && !inside(t.From, l.Path) {
			t.Notes = append(t.Notes, "the linked folder "+l.Path+" stays where it is")
		}
	}
	return t, nil
}

// problem says why t can't be moved to the root folder to, or "", and
// counts its files.
func (s *Service) problem(ctx context.Context, kind string, t *Title, to string, running bool) string {
	if t.Root == to {
		return "it's in " + to + " already"
	}
	if !running {
		if moving, err := s.Store.TitleMoving(ctx, kind, t.ID); err != nil {
			return "Box Office couldn't check it: " + err.Error()
		} else if moving {
			return "it's waiting to be moved already"
		}
	}
	if locked, err := s.Store.TitleLocked(ctx, kind, t.ID); err != nil {
		return "Box Office couldn't check it: " + err.Error()
	} else if locked {
		return "it's set to Do not touch, so Box Office leaves its files where they are"
	}
	if t.Root == "" || !filepath.IsAbs(t.Root) || !inside(t.Root, t.From) {
		return fmt.Sprintf("its folder %s isn't inside its root folder %s", t.From, t.Root)
	}
	if rel, _ := filepath.Rel(t.Root, t.From); strings.SplitN(rel, string(filepath.Separator), 2)[0] == ".recycle" {
		return "its folder is in the recycle folder"
	}
	if st, err := os.Stat(t.Root); err != nil || !st.IsDir() {
		return "Box Office can't see its root folder " + t.Root + " right now"
	}
	roots, err := s.Store.RootFolders(ctx, "")
	if err != nil {
		return "Box Office couldn't check it: " + err.Error()
	}
	for _, r := range roots {
		if p := filepath.Clean(r.Path); p == t.From || inside(t.From, p) {
			return t.From + " is a root folder itself"
		}
	}
	if _, err := os.Lstat(t.To); err == nil {
		return "there's a folder called " + filepath.Base(t.To) + " in " + to + " already"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "Box Office can't check " + t.To + ": " + err.Error()
	}
	fi, err := os.Lstat(t.From)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing on disk yet: only where it goes changes.
		t.Notes = append(t.Notes, "nothing of it is on disk yet, so only where it goes changes")
		t.sameDisk = true
		return ""
	}
	if err != nil {
		return "Box Office can't read its folder: " + err.Error()
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return t.From + " isn't a plain folder, so Box Office leaves it alone"
	}
	if other, err := s.Store.OthersFilesIn(ctx, t.From, kind, t.ID); err != nil {
		return "Box Office couldn't check it: " + err.Error()
	} else if other != "" {
		return other + " is in the same folder"
	}
	if fix, err := s.Store.FixWaitingIn(ctx, t.From); err != nil {
		return "Box Office couldn't check it: " + err.Error()
	} else if fix {
		return "a Problem files fix in its folder is waiting to be kept or put back; decide on it first"
	}
	if !writable(t.Root) {
		return t.Root + " is read-only for Box Office, so the original couldn't be removed after copying"
	}
	files, size, err := count(t.From)
	if err != nil {
		return err.Error()
	}
	t.Files, t.Size = files, size
	t.sameDisk = sameDisk(t.From, to)
	return ""
}

// count adds up the files in a title's folder. Links and other special
// files aren't moved, so one makes it say why.
func count(dir string) (int, int64, error) {
	var n int
	var size int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("Box Office can't read everything in its folder: %w", err)
		}
		switch {
		case d.IsDir():
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("Box Office can't read %s: %w", p, err)
			}
			n++
			size += info.Size()
		default:
			rel, _ := filepath.Rel(dir, p)
			return fmt.Errorf("%s in its folder is a link or special file, which Box Office doesn't move", rel)
		}
		return nil
	})
	return n, size, err
}

// inside says path is strictly inside dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// bytes writes a size the way the web UI does.
func bytes(n int64) string {
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
