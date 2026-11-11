package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/quality"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nzb"
)

// Engine runs the download queue.
type Engine struct {
	Store *store.Store
	Dir   string // the downloads folder; nothing outside it is written, save imports
	// NZBDir keeps downloads' NZBs, off the downloads folder so a release
	// can be grabbed while that's away (a network share that dropped).
	// Empty keeps them in the downloads folder, as before.
	NZBDir string
	Log    *slog.Logger
	// Importer moves a finished download into the library. Nil leaves
	// finished downloads where they are.
	Importer Importer
	// Libraries lists the library folders, so the downloads folder is never
	// one of them or inside one.
	Libraries func(ctx context.Context) ([]string, error)
	// AudioLanguages lists the languages a video's audio tracks are
	// tagged with (ISO 639-1), for a copy reported in the wrong language;
	// ffprobe when nil.
	AudioLanguages func(ctx context.Context, path string) ([]string, error)
	// Failed hears of a download that failed, to block the release and
	// look for another.
	Failed func(ctx context.Context, d store.Download, why string)
	// Notify hears what happened to a download (store.EventGrabbed and the
	// rest), to tell people about it; why is what went wrong, if anything.
	Notify func(ctx context.Context, d store.Download, event, why string)
	// Imported hears of a download, or a file picked in the downloads
	// folder, that went into the library: on its own or by hand.
	Imported func(ctx context.Context, d store.Download)

	meter meter
	limit limiter
	data  dataCounter
	// waiting says why nothing is downloading though downloads are
	// queued, when it's the data cap or the news servers' accounts.
	waiting atomic.Value // string
	wake    chan struct{}
	mu      sync.Mutex
	current *running           // the download being fetched, if any
	working map[int64]*running // every download in progress, fetching or after
	srv     *servers
	post    chan struct{} // one download is checked and unpacked at a time
	// again holds downloads to check and unpack again without fetching.
	again map[int64]bool
	// shareTries counts, by download, the tries since the downloads
	// folder last stopped answering mid-download.
	shareTries map[int64]int
	// importing holds downloads being moved into the library by hand.
	importing map[int64]bool
	// moves holds folders picked in the file browser to move into the
	// library, by their path in the downloads folder.
	moves map[string]*folderMove
	// folderOK is whether the downloads folder answered when last checked,
	// and check the check under way, if any: one on a share that hung can
	// take minutes, so it isn't started twice, and whoever asks meanwhile
	// waits for its answer.
	folderOK atomic.Bool
	checkMu  sync.Mutex
	check    *folderCheck
	// disk holds the reads of the downloads folder under way for pages, by
	// key, and diskStuck how many of them someone gave up waiting on.
	diskMu    sync.Mutex
	disk      map[string]*diskCall
	diskStuck int
	once      sync.Once
}

// Importer moves the files of a finished download into the library and
// says where they went.
type Importer interface {
	Import(ctx context.Context, d store.Download, dir string, files []string) (string, error)
}

type running struct {
	id     int64
	cancel context.CancelCauseFunc
	done   chan struct{}
	bytes  atomic.Int64 // done so far, as posted (encoded)
	failed atomic.Int64
	size   atomic.Int64
	stage  atomic.Value // string: what it's doing, for people
	// How far the current step (checking, repairing, unpacking) has got.
	stepDone, stepTotal atomic.Int64
}

// step records how far the current step has got.
func (r *running) step(done, total int64) {
	r.stepDone.Store(done)
	r.stepTotal.Store(total)
}

var (
	errPaused  = errors.New("paused")
	errRemoved = errors.New("removed")
)

func (e *Engine) init() {
	e.once.Do(func() {
		e.wake = make(chan struct{}, 1)
		e.working = map[int64]*running{}
		e.again = map[int64]bool{}
		e.shareTries = map[int64]int{}
		e.post = make(chan struct{}, 1)
	})
}

// Wake has the engine look at the queue now.
func (e *Engine) Wake() {
	e.init()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// nzbPath is where a download's NZB is: in NZBDir, or for one saved
// before that was set, in the downloads folder.
func (e *Engine) nzbPath(id int64) string {
	name := fmt.Sprintf("%d.nzb", id)
	if e.NZBDir != "" {
		p := filepath.Join(e.NZBDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(e.Dir, nzbDir, name)
}

// newNZBPath is where a new download's NZB is saved.
func (e *Engine) newNZBPath(id int64) string {
	if e.NZBDir != "" {
		return filepath.Join(e.NZBDir, fmt.Sprintf("%d.nzb", id))
	}
	return filepath.Join(e.Dir, nzbDir, fmt.Sprintf("%d.nzb", id))
}

// Add queues a release from its NZB.
func (e *Engine) Add(ctx context.Context, d store.Download, nzbData []byte) (store.Download, error) {
	e.init()
	n, err := nzb.Parse(bytes.NewReader(nzbData))
	if err != nil {
		return d, err
	}
	if d.Name == "" {
		d.Name = n.Meta["title"]
	}
	d.Name = cleanName(d.Name)
	if d.Name == "" {
		d.Name = "download"
	}
	if d.Password == "" {
		d.Password = n.Meta["password"]
	}
	if d.Password == "" {
		d.Name, d.Password = passwordFromName(d.Name)
	}
	d.Size = n.Bytes()
	if d.Quality == 0 {
		d.Quality = quality.Parse(d.Name).ID
	}
	if err := os.MkdirAll(filepath.Dir(e.newNZBPath(0)), 0o750); err != nil {
		if folderAway(err) {
			return d, fmt.Errorf("the downloads folder %s isn't answering, so the release can't be saved to download; if it's a network share, check it's mounted and up", e.Dir)
		}
		return d, fmt.Errorf("make the folder for NZBs: %w", err)
	}
	// It waits, paused, until its NZB is saved.
	want := d.Status
	if want == "" {
		want = store.DownloadQueued
	}
	d.Status = store.DownloadPaused
	d, err = e.Store.AddDownload(ctx, d)
	if err != nil {
		return d, err
	}
	if err := os.WriteFile(e.newNZBPath(d.ID), nzbData, 0o640); err != nil {
		e.Store.DeleteDownload(ctx, d.ID)
		return d, err
	}
	if err := e.Store.SetDownloadStatus(ctx, d.ID, want, ""); err != nil {
		return d, err
	}
	d.Status = want
	e.Wake()
	e.notify(ctx, d, store.EventGrabbed, "")
	return d, nil
}

var rePassword = regexp.MustCompile(`^(.*?)\s*\{\{(.+)\}\}$`)

// passwordFromName takes a password given as "name{{password}}", as
// indexers and posters write it.
func passwordFromName(name string) (string, string) {
	if m := rePassword.FindStringSubmatch(name); m != nil {
		return m[1], m[2]
	}
	return name, ""
}

// cleanName makes a release name safe as a folder name.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r < 32, strings.ContainsRune(`/\:*?"<>|`, r):
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	s = strings.Trim(s, ". ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Pause stops a download where it is; Resume carries on.
func (e *Engine) Pause(ctx context.Context, id int64) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	switch d.Status {
	case store.DownloadQueued, store.DownloadDownloading:
	case store.DownloadDownloaded, store.DownloadVerifying, store.DownloadRepairing, store.DownloadUnpacking, store.DownloadImporting:
		return errors.New("it's all downloaded, so there's nothing left to pause")
	default:
		return fmt.Errorf("a download that's %s can't be paused", d.Status)
	}
	if err := e.Store.SetDownloadStatus(ctx, id, store.DownloadPaused, ""); err != nil {
		return err
	}
	e.stop(id, errPaused)
	return nil
}

func (e *Engine) Resume(ctx context.Context, id int64) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	if d.Status != store.DownloadPaused {
		return nil
	}
	if err := e.Store.SetDownloadStatus(ctx, id, store.DownloadQueued, ""); err != nil {
		return err
	}
	e.Wake()
	return nil
}

// Retry puts a finished download back in the queue. Files it fetched
// before are fetched again only if they're gone.
func (e *Engine) Retry(ctx context.Context, id int64) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	if !d.Finished() {
		return errors.New("only a finished download can be tried again")
	}
	if _, err := os.Stat(e.nzbPath(id)); err != nil {
		return errors.New("the NZB for this download is gone, so search for the release again")
	}
	e.init()
	e.mu.Lock()
	delete(e.again, id)
	e.mu.Unlock()
	if err := e.Store.ResetDownload(ctx, id); err != nil {
		return err
	}
	e.Wake()
	return nil
}

// CanReprocess says whether a failed download's files are still in its
// folder, so it can be checked, repaired and unpacked again without
// downloading it again.
func (e *Engine) CanReprocess(d store.Download) bool {
	// One that failed before all its files came, or for articles gone
	// from the news servers, has nothing whole to unpack: only
	// downloading it again can help.
	if d.Status != store.DownloadFailed || d.Quarantined != "" || !d.Fetched || d.Missing || len(d.DoneFiles) == 0 || d.Folder == "" {
		return false
	}
	if _, err := os.Stat(e.nzbPath(d.ID)); err != nil {
		return false
	}
	dir, ok := e.inside(d.Folder)
	return ok && hasFiles(dir)
}

// Reprocess runs a failed download's checking, repair and unpacking again
// on the files it already has, fetching nothing but recovery volumes a
// repair needs. (If Box Office restarts first, it carries on as a normal
// download, fetching only files that aren't there.)
func (e *Engine) Reprocess(ctx context.Context, id int64) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	if !e.CanReprocess(d) {
		return errors.New("its files aren't in the downloads folder any more, so it has to be downloaded again")
	}
	e.init()
	e.mu.Lock()
	e.again[id] = true
	e.mu.Unlock()
	if err := e.Store.RequeueDownload(ctx, id); err != nil {
		e.mu.Lock()
		delete(e.again, id)
		e.mu.Unlock()
		return err
	}
	e.Wake()
	return nil
}

// CanImport says whether a finished download is waiting to go into the
// library: it's for a title, wasn't moved there, and its files are still
// in its folder.
func (e *Engine) CanImport(d store.Download) bool {
	if e.Importer == nil || d.Status != store.DownloadCompleted || d.Kind == "" || d.ImportedTo != "" || d.Folder == "" {
		return false
	}
	dir, ok := e.inside(d.Folder)
	return ok && hasFiles(dir)
}

// Import moves a finished download into the library now, doing nothing
// else: no downloading, checking or unpacking. It works whether or not
// moving downloads in by itself is turned on.
func (e *Engine) Import(ctx context.Context, id int64) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	if !e.CanImport(d) {
		return errors.New("there's nothing of it in the downloads folder to move into the library")
	}
	e.init()
	e.mu.Lock()
	if e.importing == nil {
		e.importing = map[int64]bool{}
	}
	if e.importing[id] {
		e.mu.Unlock()
		return errors.New("it's already being moved into the library")
	}
	e.importing[id] = true
	e.mu.Unlock()
	if err := e.Store.SetDownloadStatus(ctx, id, store.DownloadImporting, ""); err != nil {
		return err
	}
	go func() {
		bg := context.WithoutCancel(ctx)
		defer e.dropOrphan(bg, id)
		defer func() {
			e.mu.Lock()
			delete(e.importing, id)
			e.mu.Unlock()
		}()
		dir, _ := e.inside(d.Folder)
		e.Store.SetDownloadStatus(bg, id, store.DownloadCompleted, e.importInto(bg, &d, dir, ""))
	}()
	return nil
}

// importInto moves a download into the library and says what happened.
// Failing to doesn't fail the download, which is fine where it is.
func (e *Engine) importInto(ctx context.Context, d *store.Download, dir, lead string) string {
	to, err := e.Importer.Import(ctx, *d, dir, listFiles(dir))
	var ro *ReadOnlyError
	var left *LeftOverError
	switch {
	case errors.As(err, &left):
		// The episodes it could tell went in; the videos it couldn't stay
		// in the download's folder, which is kept for them.
		e.Log.Info("download moved into the library, some videos unmatched", "name", d.Name, "id", d.ID, "to", to, "left", len(left.Left))
		e.Store.SetDownloadImported(ctx, d.ID, to)
		d.ImportedTo = to
		e.imported(ctx, *d)
		return lead + "Moved into the library at " + to + ", but " + left.Error() + ". Use Files on the Downloads page to choose the episode for each."
	case errors.As(err, &ro):
		e.Log.Info("download waits to go into the library: its root folder is read-only", "name", d.Name, "id", d.ID, "root", ro.Root)
		return lead + fmt.Sprintf("Downloaded, and waiting to go into the library: %s is read-only for Box Office for now. Nothing was moved; it's in the downloads folder until you choose Move into library.", ro.Root)
	case err != nil:
		e.Log.Warn("couldn't move a download into the library", "name", d.Name, "id", d.ID, "err", err)
		return lead + "Downloaded, but it couldn't go into the library yet: " + err.Error() + ". It's in the downloads folder until you choose Move into library."
	}
	e.Log.Info("download moved into the library", "name", d.Name, "id", d.ID, "to", to)
	e.Store.SetDownloadImported(ctx, d.ID, to)
	d.ImportedTo = to
	e.removeFolder(*d)
	e.imported(ctx, *d)
	return lead + "Moved into the library at " + to
}

// Remove takes a download off the queue or out of the history; with
// deleteFiles, its folder in the downloads folder goes too.
func (e *Engine) Remove(ctx context.Context, id int64, deleteFiles bool) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
		return err
	}
	if err := e.locked(d); err != nil {
		return err
	}
	e.stop(id, errRemoved)
	if err := e.Store.DeleteDownload(ctx, id); err != nil {
		return err
	}
	os.Remove(e.nzbPath(id))
	if deleteFiles {
		e.removeFolder(d)
	}
	return nil
}

// CancelFor stops and removes the downloads for titles that were taken
// off the list, deleting what they fetched: those queued, downloading,
// paused, waiting to unpack, or finished and waiting to go into the
// library. kinds are the downloads' kinds (movie, series, album, ebook,
// audiobook) and ids the titles'. Quarantined and failed downloads keep
// to their own rules.
//
// One being checked, unpacked or moved into the library can't be stopped
// in the middle; once that's done it's removed the same way, as its
// title is gone (see dropOrphan). removed counts those that went now,
// later those that go once their step is done.
func (e *Engine) CancelFor(ctx context.Context, kinds []string, ids []int64) (removed, later int, err error) {
	list, err := e.Store.UnfinishedFor(ctx, kinds, ids)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, d := range list {
		if e.locked(d) != nil {
			later++
			continue
		}
		if err := e.Remove(ctx, d.ID, true); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
			case e.lockedNow(ctx, d.ID):
				// It started a step meanwhile; it goes when that's done.
				later++
			default:
				errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
			}
			continue
		}
		removed++
		e.Log.Info("download cancelled: its title was removed", "name", d.Name, "id", d.ID, "kind", d.Kind, "title", d.TitleID)
	}
	return removed, later, errors.Join(errs...)
}

func (e *Engine) lockedNow(ctx context.Context, id int64) bool {
	d, err := e.Store.Download(ctx, id)
	return err == nil && e.locked(d) != nil
}

// dropOrphan removes a download whose title was taken off the list, with
// what it fetched, once nothing is working on it: its title went while it
// was being checked, unpacked or moved in, or before Box Office restarted.
// Quarantined, failed and imported downloads stay, as do those added by
// hand, which have no title.
func (e *Engine) dropOrphan(ctx context.Context, id int64) {
	d, err := e.Store.Download(ctx, id)
	if err != nil || !e.orphan(ctx, d) || e.locked(d) != nil {
		return
	}
	if err := e.Remove(ctx, id, true); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			e.Log.Warn("couldn't remove a download for a removed title", "name", d.Name, "id", id, "err", err)
		}
		return
	}
	e.Log.Info("download removed: its title was removed", "name", d.Name, "id", id, "kind", d.Kind, "title", d.TitleID)
}

// orphan says a download is for a title that isn't on the list any more,
// and hasn't gone into the library, failed or been quarantined.
func (e *Engine) orphan(ctx context.Context, d store.Download) bool {
	if d.Kind == "" || d.TitleID == 0 || d.Quarantined != "" || d.Status == store.DownloadFailed || d.ImportedTo != "" {
		return false
	}
	ok, err := e.Store.TitleExists(ctx, d.Kind, d.TitleID)
	return err == nil && !ok
}

// locked says why a download can't be removed now: while it's being
// checked, repaired, unpacked or moved into the library, its files are
// in use, so it's left to finish first.
func (e *Engine) locked(d store.Download) error {
	e.init()
	e.mu.Lock()
	importing := e.importing[d.ID]
	e.mu.Unlock()
	switch {
	case importing || d.Status == store.DownloadImporting:
		return errors.New("it's being moved into the library; remove it once that's done")
	case d.Status == store.DownloadVerifying, d.Status == store.DownloadRepairing, d.Status == store.DownloadUnpacking:
		return errors.New("it's being checked and unpacked; remove it once that's done")
	}
	return nil
}

// removeFolder deletes a download's folder, if it's one of the downloads
// folder's own.
func (e *Engine) removeFolder(d store.Download) {
	if d.Folder == "" {
		return
	}
	p, ok := e.inside(d.Folder)
	if !ok {
		e.Log.Warn("not deleting a folder outside the downloads folder", "folder", d.Folder)
		return
	}
	// Network shares can refuse a delete for a moment, so it gets a few
	// tries; what's still there after that stays for the user to clear.
	var err error
	for i := range removeTries {
		if i > 0 {
			time.Sleep(removeWait << (i - 1))
		}
		if err = os.RemoveAll(p); err == nil {
			return
		}
	}
	e.Log.Warn("couldn't delete a download's files", "folder", p, "err", err)
}

// inside resolves a folder name to its path in the downloads folder, and
// says whether it really is in there.
func (e *Engine) inside(folder string) (string, bool) {
	p := filepath.Join(e.Dir, folder)
	rel, err := filepath.Rel(e.Dir, p)
	return p, err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.ContainsRune(rel, filepath.Separator)
}

// MoveToTop puts a queued download first.
func (e *Engine) MoveToTop(ctx context.Context, id int64) error {
	if err := e.Store.MoveDownloadToTop(ctx, id); err != nil {
		return err
	}
	e.Wake()
	return nil
}

// MoveToBottom puts a queued download last.
func (e *Engine) MoveToBottom(ctx context.Context, id int64) error {
	if err := e.Store.MoveDownloadToBottom(ctx, id); err != nil {
		return err
	}
	e.Wake()
	return nil
}

// ClearHistory forgets finished downloads (their files stay).
func (e *Engine) ClearHistory(ctx context.Context) error {
	list, err := e.Store.ClearDownloadHistory(ctx)
	for _, d := range list {
		os.Remove(e.nzbPath(d.ID))
	}
	return err
}

func (e *Engine) stop(id int64, why error) {
	e.mu.Lock()
	r := e.working[id]
	e.mu.Unlock()
	if r == nil {
		return
	}
	r.cancel(why)
	select {
	case <-r.done:
	case <-time.After(30 * time.Second):
	}
}

// Progress is a download's live numbers.
type Progress struct {
	DoneBytes   int64  `json:"doneBytes"`
	FailedBytes int64  `json:"failedBytes"`
	Size        int64  `json:"size"`
	Stage       string `json:"stage,omitempty"`
	StepDone    int64  `json:"stepDone,omitempty"`
	StepTotal   int64  `json:"stepTotal,omitempty"`
}

// saveData saves the bytes downloaded so far, for the data cap and block
// accounts.
func (e *Engine) saveData(ctx context.Context) {
	if err := e.data.flush(ctx, e.Store); err != nil {
		e.Log.Warn("couldn't save how much was downloaded", "err", err)
	}
}

// Waiting says why queued downloads aren't starting, when it's the data
// cap or the news servers' accounts; "" otherwise.
func (e *Engine) Waiting() string {
	v, _ := e.waiting.Load().(string)
	return v
}

// Live is what the engine is doing right now.
type Live struct {
	Speed    int64              `json:"speed"` // bytes a second
	Progress map[int64]Progress `json:"progress"`
	Servers  []ServerState      `json:"servers"`
}

func (e *Engine) Live() Live {
	e.init()
	l := Live{Speed: e.meter.rate(), Progress: map[int64]Progress{}}
	e.mu.Lock()
	for id, r := range e.working {
		stage, _ := r.stage.Load().(string)
		l.Progress[id] = Progress{DoneBytes: r.bytes.Load(), FailedBytes: r.failed.Load(), Size: r.size.Load(), Stage: stage,
			StepDone: r.stepDone.Load(), StepTotal: r.stepTotal.Load()}
	}
	srv := e.srv
	e.mu.Unlock()
	if srv != nil {
		l.Servers = srv.states()
	}
	return l
}

// folderWait is how long checking the downloads folder may take. A
// network share that dropped can hang for minutes.
var folderWait = 20 * time.Second

// CheckFolder says what's wrong with the downloads folder, if anything:
// it must answer and take writes, and mustn't be a library folder or
// inside one. It gives up after folderWait, as a share that dropped can
// hang.
func (e *Engine) CheckFolder(ctx context.Context) error {
	e.checkMu.Lock()
	c := e.check
	if c == nil {
		c = &folderCheck{done: make(chan struct{})}
		e.check = c
		go func() {
			c.err = e.checkFolder(context.WithoutCancel(ctx))
			e.checkMu.Lock()
			e.check = nil
			e.checkMu.Unlock()
			close(c.done)
		}()
	}
	e.checkMu.Unlock()
	select {
	case <-c.done:
		return c.err
	case <-time.After(folderWait):
		return e.folderAwayError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// folderCheck is a check of the downloads folder under way.
type folderCheck struct {
	done chan struct{}
	err  error
}

func (e *Engine) folderAwayError() error {
	return fmt.Errorf("the downloads folder %s isn't answering; if it's a network share, check it's mounted and up. Downloads wait, and carry on by themselves once it answers", e.Dir)
}

// folderAway says whether err is a folder that's there but can't be
// reached: a network share that dropped. Making it then says it exists
// (EEXIST) though reading it fails.
func folderAway(err error) bool {
	var se shareError
	return errors.Is(err, fs.ErrExist) || errors.As(shareTrouble(err), &se)
}

// mkdirAll makes a folder; tests swap it.
var mkdirAll = os.MkdirAll

func (e *Engine) checkFolder(ctx context.Context) error {
	if e.Libraries != nil {
		libs, err := e.Libraries(ctx)
		if err != nil {
			return err
		}
		dir, _ := filepath.Abs(e.Dir)
		for _, lib := range libs {
			lib = filepath.Clean(lib)
			if rel, err := filepath.Rel(lib, dir); err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
				return fmt.Errorf("the downloads folder %s is inside the library folder %s; give downloads a folder of their own", e.Dir, lib)
			}
			if rel, err := filepath.Rel(dir, lib); err == nil && !strings.HasPrefix(rel, "..") {
				return fmt.Errorf("the library folder %s is inside the downloads folder %s; give downloads a folder of their own", lib, e.Dir)
			}
		}
	}
	if err := mkdirAll(e.Dir, 0o750); err != nil {
		if folderAway(err) {
			return e.folderAwayError()
		}
		return fmt.Errorf("Box Office can't make its downloads folder %s: %v", e.Dir, err)
	}
	if !writable(e.Dir) {
		return fmt.Errorf("Box Office can't write in its downloads folder %s", e.Dir)
	}
	return nil
}

// Run works the queue until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.init()
	if err := e.Store.RequeueInterruptedDownloads(ctx); err != nil {
		e.Log.Warn("couldn't requeue interrupted downloads", "err", err)
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		e.saveData(context.WithoutCancel(ctx))
	}()
	for n := 0; ; n++ {
		if n%6 == 0 {
			e.saveData(ctx)
		}
		if n%720 == 0 { // hourly
			e.PurgeQuarantine(ctx)
			if n > 0 && e.folderOK.Load() {
				e.resumeFolderWaits(ctx)
			}
		}
		e.startNext(ctx, &wg)
		select {
		case <-ctx.Done():
			e.mu.Lock()
			for _, r := range e.working {
				r.cancel(context.Canceled)
			}
			e.mu.Unlock()
			return
		case <-e.wake:
		case <-tick.C:
		}
	}
}

func (e *Engine) startNext(ctx context.Context, wg *sync.WaitGroup) {
	set, err := LoadSettings(ctx, e.Store)
	if err != nil {
		return
	}
	e.limit.set(set.SpeedLimit * 1000)
	e.mu.Lock()
	busy := e.current != nil
	e.mu.Unlock()
	e.waiting.Store("")
	if !set.Enabled || set.Paused || busy {
		if (!set.Enabled || set.Paused) && busy {
			e.mu.Lock()
			if r := e.current; r != nil {
				r.cancel(errPaused)
			}
			e.mu.Unlock()
		}
		return
	}
	queue, err := e.Store.DownloadQueue(ctx)
	if err != nil {
		return
	}
	i := slices.IndexFunc(queue, func(d store.Download) bool { return d.Status == store.DownloadQueued })
	if i < 0 {
		return
	}
	d := queue[i]
	// One whose title was removed isn't fetched; it goes, and the next
	// is looked at.
	if e.orphan(ctx, d) {
		e.dropOrphan(ctx, d.ID)
		e.Wake()
		return
	}
	// The month's data cap: the download that's on goes on, and the next
	// waits for next month.
	if c, err := LoadCap(ctx, e.Store); err != nil {
		return
	} else if c.Level == CapFull {
		e.waiting.Store(c.Why())
		return
	}
	// A folder that's away or wrong holds up the whole queue, not this
	// download: each would fail alike. They all carry on once it's right.
	if err := e.CheckFolder(ctx); err != nil {
		e.folderOK.Store(false)
		e.waiting.Store(sentence(err.Error()))
		return
	}
	if !e.folderOK.Swap(true) {
		e.resumeFolderWaits(ctx)
	}
	list, err := e.Store.UsenetServers(ctx)
	if err != nil {
		return
	}
	qs, err := quotas(ctx, e.Store, list)
	if err != nil {
		return
	}
	srv := newServers(list, e.Log, qs, &e.data)
	if len(srv.pools) == 0 {
		if why := srv.unusable(); why != "" {
			e.waiting.Store(why)
			return
		}
		e.Store.SetDownloadStatus(ctx, d.ID, store.DownloadPaused, "add a news server in Settings first, then resume")
		return
	}
	rctx, cancel := context.WithCancelCause(ctx)
	r := &running{id: d.ID, cancel: cancel, done: make(chan struct{})}
	r.size.Store(d.Size)
	r.bytes.Store(d.DoneBytes)
	e.mu.Lock()
	if e.srv != nil {
		e.srv.close()
	}
	e.srv = srv
	e.current = r
	e.working[d.ID] = r
	e.mu.Unlock()
	if err := e.Store.SetDownloadStatus(ctx, d.ID, store.DownloadDownloading, ""); err != nil {
		cancel(err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.process(rctx, r, d, srv)
	}()
}

// process downloads a release, then checks, repairs, unpacks and imports it.
func (e *Engine) process(ctx context.Context, r *running, d store.Download, srv *servers) {
	// Runs last, once it's no longer working: if its title was removed
	// meanwhile, it goes too (unless it's being removed already).
	defer func() {
		if !errors.Is(context.Cause(ctx), errRemoved) {
			e.dropOrphan(context.WithoutCancel(ctx), r.id)
		}
	}()
	defer close(r.done)
	finish := func() {
		e.mu.Lock()
		delete(e.working, r.id)
		if e.current == r {
			e.current = nil
		}
		e.mu.Unlock()
		e.Wake()
	}
	defer finish()
	job, err := e.fetch(ctx, r, &d, srv)
	if err == nil {
		d.Fetched = true
		e.Store.SetDownloadFetched(context.WithoutCancel(ctx), d.ID)
		// The next download starts while this one is checked and unpacked.
		e.mu.Lock()
		if e.current == r {
			e.current = nil
		}
		e.mu.Unlock()
		e.Wake()
		select {
		case e.post <- struct{}{}:
		default:
			// Another is being unpacked; this one waits its turn, and can
			// still be removed meanwhile.
			e.Store.SetDownloadStatus(context.WithoutCancel(ctx), d.ID, store.DownloadDownloaded, "Downloaded; it's unpacked once the one before it is")
			select {
			case e.post <- struct{}{}:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			err = e.postProcess(ctx, r, &d, job)
			<-e.post
		}
	}
	bg := context.WithoutCancel(ctx)
	if e.shareRetry(ctx, r, &d, err) {
		return
	}
	e.Store.SaveDownloadProgress(bg, d)
	switch cause := context.Cause(ctx); {
	case err == nil:
		if d.ImportedTo != "" {
			e.notify(bg, d, store.EventImported, "")
		} else {
			e.notify(bg, d, store.EventDownloaded, "")
		}
	case errors.Is(cause, errPaused):
		// Paused by hand, or everything was paused or turned off: back to
		// waiting, as it was.
		if cur, _ := e.Store.Download(bg, d.ID); cur.Status != store.DownloadPaused {
			e.Store.SetDownloadStatus(bg, d.ID, store.DownloadQueued, "")
		}
	case errors.Is(cause, errRemoved):
	case errors.Is(cause, context.Canceled):
		// Box Office is stopping; it carries on at the next start.
	case errors.As(err, new(localError)):
		// Box Office's own folder trouble, not the release's: it waits,
		// and nothing is blocked.
		e.Log.Warn("download paused: Box Office can't write its files", "name", d.Name, "id", d.ID, "err", err)
		e.Store.SetDownloadStatus(bg, d.ID, store.DownloadPaused, sentence(err.Error())+". Nothing is wrong with the release: give Box Office permission to write there, then resume it.")
	default:
		e.Log.Warn("download failed", "name", d.Name, "err", err)
		msg := sentence(err.Error())
		// Gone from the news servers, or locked with a password: nothing
		// to fix or tell anyone, so it's blocked, another looked for, and
		// it's out of sight.
		var h hopelessError
		if errors.As(err, &h) {
			d.Missing = true
			e.Store.SetDownloadMissing(bg, d.ID)
		}
		if e.dropHopeless(bg, d, err) {
			msg += ". Its files were deleted, as they can't be used"
		}
		e.Store.SetDownloadStatus(bg, d.ID, store.DownloadFailed, msg)
		switch {
		case d.Quarantined != "":
			e.notify(bg, d, store.EventQuarantined, err.Error())
		case !d.Missing:
			e.notify(bg, d, store.EventFailed, err.Error())
		}
		if e.Failed != nil {
			e.Failed(bg, d, sentence(err.Error()))
		}
	}
}

// shareWait is how long a download waits after the downloads folder
// stopped answering before it's tried again, doubling each time; after
// shareTries tries it fails.
var shareWait = 15 * time.Second

const shareTries = 3

// shareRetry puts a download that failed because the downloads folder
// stopped answering back in the queue after a wait, keeping the files it
// finished, and says whether it did. The queue waits with it, as nothing
// else could be saved meanwhile either.
func (e *Engine) shareRetry(ctx context.Context, r *running, d *store.Download, err error) bool {
	e.mu.Lock()
	tries := e.shareTries[d.ID]
	if err == nil {
		delete(e.shareTries, d.ID)
	}
	e.mu.Unlock()
	var se shareError
	if err == nil || ctx.Err() != nil || !errors.As(err, &se) {
		return false
	}
	bg := context.WithoutCancel(ctx)
	if tries >= shareTries {
		// The folder's trouble, not the release's: it waits, paused, and
		// the queue goes on. It's tried again within the hour.
		e.mu.Lock()
		delete(e.shareTries, d.ID)
		e.mu.Unlock()
		e.Log.Warn("the downloads folder kept dropping out; pausing the download for now", "name", d.Name, "id", d.ID, "err", err)
		d.DoneBytes = 0
		e.Store.SaveDownloadProgress(bg, *d)
		e.Store.SetDownloadStatus(bg, d.ID, store.DownloadPaused, "The downloads folder kept dropping out while Box Office was saving this ("+se.why()+"); if it's a network share, check it's up. "+shareWaitNote)
		return true
	}
	e.mu.Lock()
	e.shareTries[d.ID] = tries + 1
	e.mu.Unlock()
	wait := shareWait << tries
	e.Log.Warn("the downloads folder stopped answering; trying the download again", "name", d.Name, "id", d.ID, "in", wait.String(), "try", tries+1, "err", err)
	r.setStage(fmt.Sprintf("The downloads folder stopped answering; trying again in %s", wait.Round(time.Second)))
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return false
	}
	// What a part file had is gone, so progress counts finished files.
	d.DoneBytes = 0
	e.Store.SaveDownloadProgress(bg, *d)
	e.Store.SetDownloadStatus(bg, d.ID, store.DownloadQueued, "")
	return true
}

// shareWaitNote ends the message of a download paused because the
// downloads folder kept dropping out; resumeFolderWaits looks for it.
const shareWaitNote = "Nothing is wrong with the release: Box Office tries it again within the hour, or resume it now"

// folderWaits begin the messages of downloads paused for the downloads
// folder, not for anything of their own: older versions paused one after
// another when the folder was away.
var folderWaits = []string{"Box Office can't make its downloads folder ", "Box Office can't write in its downloads folder "}

// resumeFolderWaits puts back in the queue downloads paused because the
// downloads folder was away, now it answers.
func (e *Engine) resumeFolderWaits(ctx context.Context) {
	n, err := e.Store.ResumeDownloadsPaused(ctx, folderWaits, shareWaitNote)
	if err != nil {
		e.Log.Warn("couldn't resume downloads that waited for the downloads folder", "err", err)
		return
	}
	if n > 0 {
		e.Log.Info("the downloads folder answers again; resumed the downloads waiting for it", "downloads", n)
		e.Wake()
	}
}

// hopelessError marks a failure no retry of the same files can fix: too
// much is gone from the news servers, too much is damaged to repair, or
// it's locked with a password nobody gave.
// (A failed unpack isn't one: Unpack again can still work.)
type hopelessError struct{ error }

func (h hopelessError) Unwrap() error { return h.error }

func hopeless(err error) error { return hopelessError{err} }

// lockedError is the failure of a release locked with a password: with
// none, or a wrong one, nothing can open it, so it's hopeless. It's
// discarded like one gone from the news servers: blocked, its files
// deleted, another looked for, and nobody told.
func lockedError(gavePassword bool) error {
	if gavePassword {
		return hopeless(errors.New("it's locked with a password, and the one the release gave doesn't open it; try another release"))
	}
	return hopeless(errors.New("it's locked with a password, and the release didn't give one. Releases like this are usually fake; try another release"))
}

// dropHopeless deletes a failed download's files when they can't be used
// and the settings say so, and says whether it did. Quarantined files are
// left to the quarantine's own rules.
func (e *Engine) dropHopeless(ctx context.Context, d store.Download, err error) bool {
	var h hopelessError
	if !errors.As(err, &h) || d.Quarantined != "" || d.Folder == "" {
		return false
	}
	if set, serr := LoadSettings(ctx, e.Store); serr != nil || !set.DeleteFailed {
		return false
	}
	dir, ok := e.inside(d.Folder)
	if !ok {
		return false
	}
	if _, serr := os.Stat(dir); serr != nil {
		return false
	}
	e.removeFolder(d)
	_, serr := os.Stat(dir)
	if serr == nil {
		return false
	}
	e.Log.Info("deleted a failed download's files", "name", d.Name, "folder", d.Folder)
	return true
}

func (e *Engine) imported(ctx context.Context, d store.Download) {
	if e.Imported != nil {
		e.Imported(context.WithoutCancel(ctx), d)
	}
}

func (e *Engine) notify(ctx context.Context, d store.Download, event, why string) {
	if e.Notify != nil {
		e.Notify(context.WithoutCancel(ctx), d, event, why)
	}
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (r *running) setStage(s string) {
	r.stage.Store(s)
	r.step(0, 0)
}
