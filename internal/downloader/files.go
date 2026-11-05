package downloader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// The downloads folder's file browser: what's in the downloads folder, and
// the few things people can do to it by hand. Everything goes through an
// os.Root opened on the downloads folder, so no path, .. or symlink can
// reach outside it, and nothing here writes anywhere else, save moving a
// folder into the library through the Importer.

// ErrOutside means a path isn't in the downloads folder, or names
// something the file browser leaves alone.
var ErrOutside = errors.New("that isn't in the downloads folder")

// nzbDir is where queued NZBs are kept; the browser doesn't show it.
const nzbDir = ".nzb"

// Entry is one file or folder in the downloads folder.
type Entry struct {
	Name string `json:"name"`
	// Path is relative to the downloads folder, with forward slashes.
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Link     bool      `json:"link,omitempty"`
	Size     int64     `json:"size"`
	Files    int       `json:"files,omitempty"` // in a folder, all the way down
	Modified time.Time `json:"modified"`
	// Quarantined says it's in the quarantine folder: it may not be
	// downloaded, opened or moved into the library, only released or
	// deleted.
	Quarantined bool `json:"quarantined,omitempty"`
	// Busy says a download is still using it, so it can't be changed.
	Busy bool `json:"busy,omitempty"`
	// Download is the download it came from, when Box Office knows.
	Download *EntryDownload `json:"download,omitempty"`
	// Moving says it's being moved into the library now, and Moved what
	// happened the last time it was.
	Moving bool   `json:"moving,omitempty"`
	Moved  string `json:"moved,omitempty"`
	// Video says it's a video file, which can be moved into the library.
	Video bool `json:"video,omitempty"`
}

// EntryDownload is what the browser says about the download a folder
// came from.
type EntryDownload struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Kind    string `json:"kind,omitempty"`
	TitleID int64  `json:"titleId,omitempty"`
}

// Listing is one folder of the downloads folder.
type Listing struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	// Size is everything in the downloads folder, and Quarantine what's
	// in its quarantine folder.
	Size       int64 `json:"size"`
	Quarantine int64 `json:"quarantine"`
	// Moves are the folders picked to go into the library: those still
	// going, and how the others went.
	Moves []MoveNote `json:"moves"`
}

// MoveNote is how moving a folder into the library is going.
type MoveNote struct {
	Path    string `json:"path"`
	Running bool   `json:"running"`
	Message string `json:"message,omitempty"`
}

// cleanRel turns a path from the browser into one relative to the
// downloads folder, or says it isn't one. "" is the downloads folder.
func cleanRel(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) {
			return "", ErrOutside
		}
	}
	if p == nzbDir || strings.HasPrefix(p, nzbDir+"/") {
		return "", ErrOutside
	}
	return p, nil
}

func inQuarantine(rel string) bool {
	return rel == QuarantineDir || strings.HasPrefix(rel, QuarantineDir+"/")
}

func (e *Engine) root() (*os.Root, error) {
	r, err := os.OpenRoot(e.Dir)
	if err != nil {
		return nil, fmt.Errorf("Box Office can't open its downloads folder %s: %v", e.Dir, err)
	}
	return r, nil
}

// browseState is what the browser needs from the downloads: who uses which
// folder, and which download each folder came from.
type browseState struct {
	busy  map[string]bool
	owner map[string]store.Download
}

func (e *Engine) folderUse(ctx context.Context) (browseState, error) {
	st := browseState{busy: map[string]bool{}, owner: map[string]store.Download{}}
	queue, err := e.Store.DownloadQueue(ctx)
	if err != nil {
		return st, err
	}
	history, err := e.Store.DownloadHistory(ctx, 1000)
	if err != nil {
		return st, err
	}
	for _, d := range history {
		if d.Quarantined != "" {
			if rel, err := filepath.Rel(e.Dir, d.Quarantined); err == nil && !strings.HasPrefix(rel, "..") {
				st.owner[filepath.ToSlash(rel)] = d
			}
		}
		if d.Folder != "" {
			if _, ok := st.owner[d.Folder]; !ok {
				st.owner[d.Folder] = d
			}
		}
	}
	for _, d := range queue {
		if d.Folder != "" {
			st.busy[d.Folder] = true
			st.owner[d.Folder] = d
		}
	}
	e.mu.Lock()
	var importing []int64
	for id := range e.importing {
		importing = append(importing, id)
	}
	for p, j := range e.moves {
		if j.running {
			st.busy[top(p)] = true
		}
	}
	e.mu.Unlock()
	for _, id := range importing {
		if d, err := e.Store.Download(ctx, id); err == nil && d.Folder != "" {
			st.busy[d.Folder] = true
		}
	}
	return st, nil
}

// top is the folder in the downloads folder that rel is in: its first
// part, or its first two inside the quarantine folder.
func top(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if parts[0] == QuarantineDir && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// Browse lists one folder in the downloads folder.
func (e *Engine) Browse(ctx context.Context, p string) (Listing, error) {
	e.init()
	rel, err := cleanRel(p)
	if err != nil {
		return Listing{}, err
	}
	root, err := e.root()
	if err != nil {
		return Listing{}, err
	}
	defer root.Close()
	name := rel
	if name == "" {
		name = "."
	}
	info, err := root.Stat(name)
	if err != nil {
		return Listing{}, fmt.Errorf("%s isn't in the downloads folder any more", p)
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%s is a file, not a folder", p)
	}
	f, err := root.Open(name)
	if err != nil {
		return Listing{}, err
	}
	des, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return Listing{}, err
	}
	state, err := e.folderUse(ctx)
	if err != nil {
		return Listing{}, err
	}
	fsys := root.FS()
	out := Listing{Path: rel, Entries: []Entry{}}
	for _, de := range des {
		if rel == "" && de.Name() == nzbDir || strings.HasPrefix(de.Name(), ".boxoffice-") {
			continue
		}
		ent := Entry{Name: de.Name(), Path: path.Join(rel, de.Name())}
		ent.Quarantined = inQuarantine(ent.Path) && ent.Path != QuarantineDir
		if i, err := de.Info(); err == nil {
			ent.Modified = i.ModTime().UTC()
			ent.Size = i.Size()
		}
		switch {
		case de.Type()&fs.ModeSymlink != 0:
			ent.Link, ent.Size = true, 0
		case de.IsDir():
			ent.Dir = true
			ent.Size, ent.Files = treeSize(fsys, ent.Path)
		default:
			ent.Video = isVideo(ent.Name) && !ent.Quarantined
		}
		t := top(ent.Path)
		ent.Busy = state.busy[t]
		if d, ok := state.owner[t]; ok && t == ent.Path {
			ent.Download = &EntryDownload{ID: d.ID, Name: d.Name, Status: d.Status, Kind: d.Kind, TitleID: d.TitleID}
		}
		e.mu.Lock()
		if j, ok := e.moves[ent.Path]; ok {
			ent.Moving, ent.Moved = j.running, j.message
		}
		e.mu.Unlock()
		out.Entries = append(out.Entries, ent)
	}
	slices.SortFunc(out.Entries, func(a, b Entry) int {
		// The quarantine folder first, then folders, then files.
		if (a.Path == QuarantineDir) != (b.Path == QuarantineDir) {
			if a.Path == QuarantineDir {
				return -1
			}
			return 1
		}
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	e.mu.Lock()
	out.Moves = []MoveNote{}
	for p, j := range e.moves {
		out.Moves = append(out.Moves, MoveNote{Path: p, Running: j.running, Message: j.message})
	}
	e.mu.Unlock()
	slices.SortFunc(out.Moves, func(a, b MoveNote) int { return strings.Compare(a.Path, b.Path) })
	out.Size, _ = treeSize(fsys, ".")
	out.Quarantine, _ = treeSize(fsys, QuarantineDir)
	return out, nil
}

// treeSize adds up the files in a folder, without following links.
func treeSize(fsys fs.FS, dir string) (int64, int) {
	var size int64
	var n int
	fs.WalkDir(fsys, dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if dir == "." && p == nzbDir && de.IsDir() {
			return fs.SkipDir
		}
		if de.Type().IsRegular() {
			if i, err := de.Info(); err == nil {
				size += i.Size()
				n++
			}
		}
		return nil
	})
	return size, n
}

// changeable checks that rel can be changed by hand: it's something in the
// downloads folder (not the folder itself or the quarantine folder), the
// downloads folder is set up right, and no download is using it.
func (e *Engine) changeable(ctx context.Context, rel string) (browseState, error) {
	if rel == "" || rel == QuarantineDir {
		return browseState{}, errors.New("that folder is Box Office's own and stays")
	}
	if err := e.CheckFolder(ctx); err != nil {
		return browseState{}, err
	}
	state, err := e.folderUse(ctx)
	if err != nil {
		return state, err
	}
	if state.busy[top(rel)] {
		return state, errors.New("a download is still using it; wait for it to finish, or remove the download on the Downloads page")
	}
	return state, nil
}

// DeletePath deletes a file or folder in the downloads folder, everything
// in it included. Links are deleted, never what they point to.
func (e *Engine) DeletePath(ctx context.Context, p string) error {
	e.init()
	rel, err := cleanRel(p)
	if err != nil {
		return err
	}
	state, err := e.changeable(ctx, rel)
	if err != nil {
		return err
	}
	root, err := e.root()
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(rel); err != nil {
		return fmt.Errorf("%s isn't in the downloads folder any more", p)
	}
	// Network shares can refuse a delete for a moment, so it gets a few
	// tries.
	for i := range removeTries {
		if i > 0 {
			time.Sleep(removeWait << (i - 1))
		}
		if err = root.RemoveAll(rel); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("couldn't delete all of it: %v", err)
	}
	e.mu.Lock()
	delete(e.moves, rel)
	e.mu.Unlock()
	// A download whose quarantined files are gone says so; it stays
	// marked as quarantined, so it isn't offered to download again.
	if d, ok := state.owner[rel]; ok && inQuarantine(rel) {
		e.Store.SetDownloadMessage(ctx, d.ID, strings.TrimSuffix(d.Message, ".")+". Its files were deleted from quarantine.")
	}
	e.Log.Info("deleted from the downloads folder", "path", rel)
	return nil
}

// Release moves a folder or file out of the quarantine folder, back into
// the downloads folder, and says its new path. Its files still can't be
// run (quarantine took that away), and moving it into the library still
// only takes its videos and subtitles.
func (e *Engine) Release(ctx context.Context, p string) (string, error) {
	e.init()
	rel, err := cleanRel(p)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 2 || parts[0] != QuarantineDir {
		return "", errors.New("only what's directly in the quarantine folder can be released")
	}
	state, err := e.changeable(ctx, rel)
	if err != nil {
		return "", err
	}
	root, err := e.root()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if _, err := root.Lstat(rel); err != nil {
		return "", fmt.Errorf("%s isn't in quarantine any more", p)
	}
	name := parts[1]
	to := name
	for i := 2; ; i++ {
		if _, err := root.Lstat(to); errors.Is(err, fs.ErrNotExist) && to != nzbDir && to != QuarantineDir {
			break
		}
		to = fmt.Sprintf("%s (%d)", name, i)
	}
	if err := root.Rename(rel, to); err != nil {
		return "", fmt.Errorf("couldn't move it out of quarantine: %v", err)
	}
	if d, ok := state.owner[rel]; ok {
		e.Store.SetDownloadQuarantined(ctx, d.ID, "")
		d.Folder = to
		e.Store.SaveDownloadProgress(ctx, d)
	}
	e.Log.Warn("released from quarantine by hand", "from", rel, "to", to)
	return to, nil
}

// OpenFile opens a file in the downloads folder to download it. Files in
// quarantine can't be; release them first.
func (e *Engine) OpenFile(p string) (*os.File, os.FileInfo, error) {
	rel, err := cleanRel(p)
	if err != nil {
		return nil, nil, err
	}
	if inQuarantine(rel) {
		return nil, nil, errors.New("files in quarantine can't be downloaded, as they may be harmful; release it first if you're sure it's safe")
	}
	if rel == "" {
		return nil, nil, ErrOutside
	}
	root, err := e.root()
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, nil, fmt.Errorf("%s isn't in the downloads folder any more", p)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("only files can be downloaded")
	}
	return f, info, nil
}

// folderMove is a hand-picked folder being moved into the library.
type folderMove struct {
	running bool
	message string
}

// ImportPath moves a folder (or one video file) in the downloads folder
// into a title's folder in the library, the same way a finished download
// is. A folder a download made, for the same title, goes as that download.
// It runs in the background; Browse says how it went.
func (e *Engine) ImportPath(ctx context.Context, p, kind string, titleID int64) error {
	e.init()
	rel, err := cleanRel(p)
	if err != nil {
		return err
	}
	if inQuarantine(rel) {
		return errors.New("what's in quarantine can't go into the library; release it first if you're sure it's safe")
	}
	if e.Importer == nil {
		return errors.New("moving into the library isn't set up")
	}
	if kind != "movie" && kind != "series" {
		return errors.New("choose the movie or show it's for")
	}
	state, err := e.changeable(ctx, rel)
	if err != nil {
		return err
	}
	// The download's own folder, for its own title, goes as the download,
	// so its history says where it went.
	if d, ok := state.owner[rel]; ok && d.Kind == kind && d.TitleID == titleID && e.CanImport(d) {
		return e.Import(ctx, d.ID)
	}
	root, err := e.root()
	if err != nil {
		return err
	}
	info, err := root.Lstat(rel)
	root.Close()
	if err != nil {
		return fmt.Errorf("%s isn't in the downloads folder any more", p)
	}
	dir, files := filepath.Join(e.Dir, filepath.FromSlash(rel)), []string(nil)
	switch {
	case info.IsDir():
		files = listFiles(dir)
	case info.Mode().IsRegular() && isVideo(info.Name()):
		dir, files = filepath.Dir(dir), []string{info.Name()}
	default:
		return errors.New("only a folder or a video file can go into the library")
	}
	d := store.Download{Name: strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())), Kind: kind, TitleID: titleID}
	if info.IsDir() {
		d.Name = info.Name()
	}
	if o, ok := state.owner[top(rel)]; ok && o.Kind == kind && o.TitleID == titleID {
		d.Quality, d.EpisodeIDs = o.Quality, o.EpisodeIDs
	}
	e.mu.Lock()
	if e.moves == nil {
		e.moves = map[string]*folderMove{}
	}
	if j := e.moves[rel]; j != nil && j.running {
		e.mu.Unlock()
		return errors.New("it's already being moved into the library")
	}
	e.moves[rel] = &folderMove{running: true}
	e.mu.Unlock()
	go func() {
		bg := context.WithoutCancel(ctx)
		to, err := e.Importer.Import(bg, d, dir, files)
		msg := "Moved into the library at " + to
		var ro *ReadOnlyError
		switch {
		case errors.As(err, &ro):
			msg = fmt.Sprintf("Couldn't move it into the library: %s is read-only for Box Office. Nothing was moved.", ro.Root)
		case err != nil:
			msg = "Couldn't move it into the library: " + err.Error()
		}
		if err != nil {
			e.Log.Warn("couldn't move a folder into the library", "path", rel, "err", err)
		} else {
			e.Log.Info("moved into the library by hand", "path", rel, "to", to)
			if info.IsDir() {
				// What's left (extras, samples, .nfo files) goes, as it
				// does after a download is moved in.
				if r, err := e.root(); err == nil {
					r.RemoveAll(rel)
					r.Close()
				}
			}
			if o, ok := state.owner[rel]; ok && o.Kind == kind && o.TitleID == titleID {
				e.Store.SetDownloadImported(bg, o.ID, to)
			}
		}
		e.mu.Lock()
		e.moves[rel] = &folderMove{message: msg}
		e.mu.Unlock()
	}()
	return nil
}
