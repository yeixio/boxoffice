// Package relocate moves titles to another root folder: a whole library,
// or titles picked on Movies or Shows, to a new drive, say. It works in the
// background, one title at a time, and never loses a file:
//
//   - A folder on the same disk is just renamed.
//   - Otherwise each file is copied to a hidden folder in the new root
//     folder, written out to disk, and read back to check it matches the
//     original byte for byte (SHA-256). The original is untouched.
//   - Only then does the copy take the folder's name, and the title (its
//     folder, its files' records, its subtitles) point at it, in one
//     database transaction.
//   - Only then are the originals removed: only the files copied, and only
//     if they haven't changed since.
//
// Each title's row in title_moves says how far it got, so a restart, a
// pause or a cancel carries on or backs out cleanly. While a title is being
// moved, downloads wait to go into its folder, and its files can't be
// deleted.
package relocate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/arrapi"
	"github.com/yeixio/boxoffice/internal/store"
)

// Settings.
const (
	// SettingPaused is "1" while moving is paused, and SettingPausedWhy
	// says why when Box Office paused it itself (the new disk filled up).
	SettingPaused    = "title_moves_paused"
	SettingPausedWhy = "title_moves_paused_why"
	// SettingSpeed is the speed limit, in MB/s; 0 or none is no limit.
	SettingSpeed = "title_moves_speed"
)

// JobKind is how a finished run of moves shows on Activity.
const JobKind = "move-titles"

// maxTries is how often a title whose files keep changing as it's copied
// is tried before it's given up on.
const maxTries = 3

// Why the title being moved stopped part-way.
var (
	errPaused    = errors.New("paused")
	errCancelled = errors.New("cancelled")
)

// Service moves titles. Run works through the moves; the rest is for the
// API.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// AfterMove, when set, is called once a title's move has ended either
	// way, so the downloads that waited for it can go into its folder.
	AfterMove func(ctx context.Context, kind string, id int64)

	speed atomic.Int64 // MB/s, 0 for no limit

	mu   sync.Mutex
	held map[key]bool
	// writing counts what's going into each title's folder right now
	// (Enter), so a move doesn't start under it.
	writing map[key]int
	current *store.TitleMove
	prog    *live
	stop    context.CancelCauseFunc
	wake    chan struct{}
	// moved and failed count the titles since the line was last empty,
	// for Activity.
	moved, failed int
	loaded        bool
}

type key struct {
	kind string
	id   int64
}

func (s *Service) init(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
		s.held = map[key]bool{}
	}
	if !s.loaded && s.Store != nil {
		if v, err := s.Store.Setting(ctx, SettingSpeed); err == nil {
			n, _ := strconv.ParseInt(v, 10, 64)
			s.speed.Store(max(n, 0))
			s.loaded = true
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// Moving says whether a title is being moved right now, so nothing else
// should write to its folder or delete from it.
func (s *Service) Moving(kind string, id int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[key{kind, id}]
}

// Enter is for something about to write into a title's folder, such as a
// download going in. It says no while the title is being moved; otherwise
// no move starts on the title until leave is called. Checking and
// counting under one lock means a download can't slip in between a move's
// checks and its copy, or a move start under a download half moved in.
func (s *Service) Enter(kind string, id int64) (leave func(), ok bool) {
	if s == nil {
		return func() {}, true
	}
	k := key{kind, id}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[k] {
		return nil, false
	}
	if s.writing == nil {
		s.writing = map[key]int{}
	}
	s.writing[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.writing[k]--; s.writing[k] <= 0 {
				delete(s.writing, k)
			}
			s.mu.Unlock()
		})
	}, true
}

// hold keeps everything else out of a title's folder while it moves. It
// says no while something is going into the folder (Enter).
func (s *Service) hold(kind string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writing[key{kind, id}] > 0 {
		return false
	}
	if s.held == nil {
		s.held = map[key]bool{}
	}
	s.held[key{kind, id}] = true
	return true
}

func (s *Service) release(ctx context.Context, kind string, id int64) {
	s.mu.Lock()
	delete(s.held, key{kind, id})
	s.mu.Unlock()
	if s.AfterMove != nil {
		s.AfterMove(context.WithoutCancel(ctx), kind, id)
	}
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run works through the moves until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.init(ctx)
	for {
		if !s.runOnce(ctx) {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			case <-time.After(time.Minute):
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// RunAll works through every move that can be made now, for tests.
func (s *Service) RunAll(ctx context.Context) {
	s.init(ctx)
	for s.runOnce(ctx) {
	}
}

// runOnce moves one title, and says whether it did anything.
func (s *Service) runOnce(ctx context.Context) bool {
	if paused, _ := s.Store.Setting(ctx, SettingPaused); paused == "1" {
		return false
	}
	list, err := s.Store.UnfinishedTitleMoves(ctx)
	if err != nil {
		s.log().Warn("couldn't read the titles waiting to be moved", "err", err)
		return false
	}
	if len(list) == 0 {
		s.summarize(ctx)
		return false
	}
	for _, m := range list {
		if ctx.Err() != nil {
			return false
		}
		if s.work(ctx, m) {
			return true
		}
	}
	return false // each is waiting for something
}

// summarize notes on Activity what the moves since the line was last
// empty came to.
func (s *Service) summarize(ctx context.Context) {
	s.mu.Lock()
	moved, failed := s.moved, s.failed
	s.moved, s.failed = 0, 0
	s.mu.Unlock()
	if moved+failed == 0 {
		return
	}
	msg := fmt.Sprintf("Moved %d %s to another folder.", moved, plural(moved, "title", "titles"))
	status := "done"
	if failed > 0 {
		msg += fmt.Sprintf(" %d couldn't be moved; see Settings → Library → Move titles.", failed)
		if moved == 0 {
			status = "failed"
		}
	}
	s.Store.RecordJob(ctx, JobKind, "Move titles", status, msg)
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

// work takes a move as far as it can go now, and says whether it did:
// false means it's waiting for something, such as a download going into
// its folder.
func (s *Service) work(ctx context.Context, m store.TitleMove) bool {
	if m.State != store.MoveWaiting {
		if !s.hold(m.Kind, m.TitleID) {
			return false
		}
		defer s.release(ctx, m.Kind, m.TitleID)
		s.resume(ctx, m)
		return true
	}
	t, err := s.check(ctx, m.Kind, m.TitleID, filepath.Clean(m.ToRoot), true)
	if errors.Is(err, store.ErrNotFound) {
		s.end(ctx, &m, store.MoveSkipped, "it's no longer on the list")
		return true
	}
	if err != nil {
		s.log().Warn("couldn't check a title to move", "title", m.Title, "err", err)
		return false
	}
	if t.Problem != "" {
		s.end(ctx, &m, store.MoveSkipped, t.Problem)
		return true
	}
	if why := checkTarget(filepath.Clean(m.ToRoot)); why != "" {
		s.pause(ctx, why)
		return false
	}
	if !t.sameDisk && t.Size > 0 {
		if free := arrapi.FreeSpace(m.ToRoot); free > 0 && t.Size > free-Reserve {
			s.pause(ctx, fmt.Sprintf("%s has %s free, and %s needs %s", m.ToRoot, bytes(free), t.Title, bytes(t.Size)))
			return false
		}
	}
	// Held first, so a download can't start going into its folder between
	// the check and the copy.
	why, err := waitingForDownload, error(nil)
	if s.hold(m.Kind, m.TitleID) {
		if why, err = s.busy(ctx, m); err != nil || why != "" {
			s.release(ctx, m.Kind, m.TitleID)
		}
	}
	if err != nil || why != "" {
		if why != "" && why != m.Note {
			m.Note = why
			s.Store.SaveTitleMove(ctx, m)
		}
		return false
	}
	defer s.release(ctx, m.Kind, m.TitleID)
	m.FromRoot, m.FromPath, m.ToPath = t.Root, t.From, t.To
	m.Files, m.Size, m.Note = t.Files, t.Size, ""
	now := time.Now().UTC()
	m.StartedAt = &now
	m.State = store.MoveCopying
	if err := s.Store.SaveTitleMove(ctx, m); err != nil {
		s.log().Warn("couldn't start moving a title", "title", m.Title, "err", err)
		return false
	}
	s.log().Info("moving a title", "title", m.Title, "from", m.FromPath, "to", m.ToPath, "files", m.Files, "size", m.Size)
	s.move(ctx, m)
	return true
}

const waitingForDownload = "waiting for a download to go into its folder first"

// busy says what the title's waiting for before it can be moved, or "".
func (s *Service) busy(ctx context.Context, m store.TitleMove) (string, error) {
	list, err := s.Store.UnfinishedFor(ctx, []string{m.Kind}, []int64{m.TitleID})
	if err != nil {
		return "", err
	}
	for _, d := range list {
		switch d.Status {
		case store.DownloadVerifying, store.DownloadRepairing, store.DownloadUnpacking, store.DownloadImporting:
			return waitingForDownload, nil
		}
	}
	return "", nil
}

// pause stops moving until someone carries on, saying why.
func (s *Service) pause(ctx context.Context, why string) {
	s.log().Warn("moving titles paused", "why", why)
	s.Store.SetSetting(ctx, SettingPausedWhy, why)
	s.Store.SetSetting(ctx, SettingPaused, "1")
}

// move moves a title whose row says it's copying, from the start.
func (s *Service) move(ctx context.Context, m store.TitleMove) {
	if _, err := os.Lstat(m.FromPath); errors.Is(err, fs.ErrNotExist) {
		// Nothing on disk: only where it goes changes.
		s.switchOver(ctx, m, "")
		return
	}
	// Renaming onto an empty folder would replace it, so one that turned
	// up since the check stops it.
	if _, err := os.Lstat(m.ToPath); !errors.Is(err, fs.ErrNotExist) {
		s.end(ctx, &m, store.MoveFailed, "a folder called "+filepath.Base(m.ToPath)+" turned up in "+m.ToRoot+"; nothing was moved")
		return
	}
	if err := renameFolder(m.FromPath, m.ToPath); err == nil {
		s.switchOver(ctx, m, "")
		return
	} else if !errors.Is(err, syscall.EXDEV) {
		s.log().Debug("couldn't rename a title's folder; copying it", "title", m.Title, "err", err)
	}
	tmp := tempFolder(m)
	if err := os.RemoveAll(tmp); err != nil {
		s.end(ctx, &m, store.MoveFailed, "couldn't clear away an earlier try's copy: "+err.Error())
		return
	}
	run, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	p := newLive(m.Size)
	s.mu.Lock()
	s.current, s.prog, s.stop = &m, p, stop
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.current, s.prog, s.stop = nil, nil, nil
		s.mu.Unlock()
	}()
	list, err := s.copyTree(run, m.FromPath, tmp, p)
	if err != nil {
		// The original is untouched; only the copy goes.
		if rerr := os.RemoveAll(tmp); rerr != nil {
			s.log().Warn("couldn't clear away a part-made copy", "folder", tmp, "err", rerr)
		}
		switch cause := context.Cause(run); {
		case errors.Is(cause, errPaused) || (ctx.Err() != nil && !errors.Is(cause, errCancelled)):
			// Paused, or Box Office is stopping: it starts over later.
			m.State, m.StartedAt = store.MoveWaiting, nil
			s.Store.SaveTitleMove(context.WithoutCancel(ctx), m)
		case errors.Is(cause, errCancelled):
			s.end(ctx, &m, store.MoveCancelled, "")
		case errors.Is(err, errChanged):
			m.Tries++
			if m.Tries >= maxTries {
				s.end(ctx, &m, store.MoveFailed, "its files kept changing while it was copied, so it was left where it was")
				return
			}
			m.State, m.StartedAt, m.Note = store.MoveWaiting, nil, "its files changed while it was copied; it'll be tried again"
			s.Store.SaveTitleMove(ctx, m)
		default:
			s.end(ctx, &m, store.MoveFailed, "nothing was moved: "+err.Error())
		}
		return
	}
	b, _ := json.Marshal(list)
	m.Manifest, m.State = string(b), store.MoveCopied
	if err := s.Store.SaveTitleMove(context.WithoutCancel(ctx), m); err != nil {
		os.RemoveAll(tmp)
		s.end(ctx, &m, store.MoveFailed, "nothing was moved: "+err.Error())
		return
	}
	p.phase("removing", 0)
	s.finishCopied(context.WithoutCancel(ctx), m)
}

// tempFolder is where a title's copy is made, hidden in the new root
// folder, until it's whole and checked.
func tempFolder(m store.TitleMove) string {
	return filepath.Join(m.ToRoot, fmt.Sprintf(".boxoffice-move-%d", m.ID))
}

// resume carries on with a move a restart cut off.
func (s *Service) resume(ctx context.Context, m store.TitleMove) {
	s.log().Info("carrying on moving a title", "title", m.Title, "state", m.State)
	switch m.State {
	case store.MoveCopying:
		_, ferr := os.Lstat(m.FromPath)
		_, terr := os.Lstat(m.ToPath)
		switch {
		case errors.Is(ferr, fs.ErrNotExist) && terr == nil:
			// It was renamed; the title doesn't know yet.
			s.switchOver(ctx, m, "")
		case errors.Is(ferr, fs.ErrNotExist) && errors.Is(terr, fs.ErrNotExist):
			// Nothing on disk.
			s.switchOver(ctx, m, "")
		case terr == nil:
			s.end(ctx, &m, store.MoveFailed, "both "+m.FromPath+" and "+m.ToPath+" are there; nothing was removed, so check them and remove one by hand")
		default:
			// Copying was cut off: start over.
			os.RemoveAll(tempFolder(m))
			m.State, m.StartedAt = store.MoveWaiting, nil
			s.Store.SaveTitleMove(ctx, m)
		}
	case store.MoveCopied:
		s.finishCopied(ctx, m)
	case store.MoveSwitched:
		s.removeOriginal(ctx, m)
	}
}

// finishCopied gives a checked copy the folder's name and switches the
// title over to it.
func (s *Service) finishCopied(ctx context.Context, m store.TitleMove) {
	tmp := tempFolder(m)
	_, terr := os.Lstat(tmp)
	_, derr := os.Lstat(m.ToPath)
	switch {
	case terr == nil && errors.Is(derr, fs.ErrNotExist):
		if err := os.Rename(tmp, m.ToPath); err != nil {
			s.end(ctx, &m, store.MoveFailed, "the copy couldn't take the folder's name: "+err.Error()+"; the original is where it was")
			os.RemoveAll(tmp)
			return
		}
	case errors.Is(terr, fs.ErrNotExist) && derr == nil:
		// Renamed already.
	case errors.Is(terr, fs.ErrNotExist):
		// The copy's gone: start over.
		m.State, m.StartedAt, m.Manifest = store.MoveWaiting, nil, ""
		s.Store.SaveTitleMove(ctx, m)
		return
	default:
		s.end(ctx, &m, store.MoveFailed, "a folder called "+filepath.Base(m.ToPath)+" turned up in "+m.ToRoot+"; the original is where it was, and the copy is in "+tmp)
		return
	}
	s.switchOver(ctx, m, m.Manifest)
}

// switchOver points the title at its new folder, then removes the
// original. If the title changed meanwhile (it was removed, or its folder
// changed), the copy goes again, unless the original's gone too.
func (s *Service) switchOver(ctx context.Context, m store.TitleMove, manifest string) {
	ctx = context.WithoutCancel(ctx)
	err := s.Store.SwitchTitleFolder(ctx, m.ID, m.Kind, m.TitleID, m.FromPath, m.ToRoot, m.ToPath)
	if errors.Is(err, store.ErrNotFound) {
		s.backOut(ctx, m, manifest, "it was removed from the list, or its folder changed, while it was being moved")
		return
	}
	if err != nil {
		s.backOut(ctx, m, manifest, "Box Office couldn't save its new folder: "+err.Error())
		return
	}
	m.State = store.MoveSwitched
	s.removeOriginal(ctx, m)
}

// backOut undoes a move whose title couldn't be switched over: the
// original's still there, so the copy (or the renamed folder) goes back.
func (s *Service) backOut(ctx context.Context, m store.TitleMove, manifest, why string) {
	var list []entry
	if manifest == "" {
		// Renamed, or nothing on disk: rename it back.
		if _, err := os.Lstat(m.ToPath); err == nil {
			if _, ferr := os.Lstat(m.FromPath); errors.Is(ferr, fs.ErrNotExist) {
				if err := os.Rename(m.ToPath, m.FromPath); err != nil {
					s.end(ctx, &m, store.MoveFailed, why+"; its folder is now "+m.ToPath+" and couldn't be put back: "+err.Error())
					return
				}
			}
		}
		s.end(ctx, &m, store.MoveFailed, why+"; nothing was moved")
		return
	}
	json.Unmarshal([]byte(manifest), &list)
	if originalWhole(m.FromPath, list) {
		if err := os.RemoveAll(m.ToPath); err != nil {
			s.end(ctx, &m, store.MoveFailed, why+"; the original is where it was, and the copy in "+m.ToPath+" couldn't be removed: "+err.Error())
			return
		}
		s.end(ctx, &m, store.MoveFailed, why+"; nothing was moved")
		return
	}
	s.end(ctx, &m, store.MoveFailed, why+"; the original isn't all there any more, so the checked copy was kept in "+m.ToPath)
}

// originalWhole says every file copied is still in the original folder,
// unchanged.
func originalWhole(from string, list []entry) bool {
	for _, e := range list {
		info, err := os.Lstat(filepath.Join(from, e.Rel))
		if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size || info.ModTime().UnixNano() != e.Mod {
			return false
		}
	}
	return true
}

// removeOriginal removes the files copied from the old folder, once the
// title points at the copy: only those, and only if they haven't changed.
// Anything else is left, and said.
func (s *Service) removeOriginal(ctx context.Context, m store.TitleMove) {
	if m.Manifest == "" {
		// Renamed, or nothing on disk: there's no original left.
		s.end(ctx, &m, store.MoveDone, "")
		return
	}
	var list []entry
	if err := json.Unmarshal([]byte(m.Manifest), &list); err != nil {
		s.end(ctx, &m, store.MoveDone, "moved, but Box Office couldn't read which files to remove from "+m.FromPath+", so it's still there")
		return
	}
	left := 0
	var lastErr error
	for _, e := range list {
		path := filepath.Join(m.FromPath, e.Rel)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size || info.ModTime().UnixNano() != e.Mod {
			left++
			continue
		}
		// The copy must be there, whole, before the original goes.
		if c, err := os.Stat(filepath.Join(m.ToPath, e.Rel)); err != nil || c.Size() != e.Size {
			left++
			continue
		}
		if err := retry(func() error { return os.Remove(path) }); err != nil {
			left++
			lastErr = err
		}
	}
	removeEmpty(m.FromPath)
	if _, err := os.Lstat(m.FromPath); err == nil {
		// Whatever's left is something the copy doesn't have.
		if n, _, err := count(m.FromPath); err == nil && n > left {
			left = n
		}
	}
	note := ""
	if left > 0 {
		note = fmt.Sprintf("moved; %d %s left in %s", left, plural(left, "file was", "files were"), m.FromPath)
		if lastErr != nil {
			note += " (" + lastErr.Error() + ")"
		} else {
			note += " because they changed or weren't copied; check them before removing them"
		}
	}
	s.end(ctx, &m, store.MoveDone, note)
}

// removeEmpty removes the empty folders in dir, and dir if it's empty.
func removeEmpty(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // only empty ones go
	}
}

// renameFolder renames a title's folder to its new place: os.Rename,
// swapped out in tests to act as if the new folder were on another disk.
var renameFolder = os.Rename

// Tries and wait for removing: a network share can refuse for a moment
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

// end finishes a move as state, with a note.
func (s *Service) end(ctx context.Context, m *store.TitleMove, state, note string) {
	ctx = context.WithoutCancel(ctx)
	now := time.Now().UTC()
	m.State, m.Note, m.FinishedAt = state, note, &now
	if err := s.Store.SaveTitleMove(ctx, *m); err != nil {
		s.log().Warn("couldn't save how a move ended", "title", m.Title, "err", err)
	}
	s.mu.Lock()
	switch state {
	case store.MoveDone:
		s.moved++
	case store.MoveFailed:
		s.failed++
	}
	s.mu.Unlock()
	switch state {
	case store.MoveDone:
		s.log().Info("title moved", "title", m.Title, "to", m.ToPath, "note", note)
	case store.MoveCancelled:
		s.log().Info("title move cancelled", "title", m.Title)
	default:
		s.log().Warn("title not moved", "title", m.Title, "state", state, "why", note)
	}
}
