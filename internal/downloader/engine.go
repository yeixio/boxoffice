package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	Log   *slog.Logger
	// Importer moves a finished download into the library. Nil leaves
	// finished downloads where they are.
	Importer Importer
	// Libraries lists the library folders, so the downloads folder is never
	// one of them or inside one.
	Libraries func(ctx context.Context) ([]string, error)
	// Failed hears of a download that failed, to block the release and
	// look for another.
	Failed func(ctx context.Context, d store.Download, why string)
	// Notify hears what happened to a download (store.EventGrabbed and the
	// rest), to tell people about it; why is what went wrong, if anything.
	Notify func(ctx context.Context, d store.Download, event, why string)

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
	// importing holds downloads being moved into the library by hand.
	importing map[int64]bool
	// moves holds folders picked in the file browser to move into the
	// library, by their path in the downloads folder.
	moves map[string]*folderMove
	once  sync.Once
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

func (e *Engine) nzbPath(id int64) string {
	return filepath.Join(e.Dir, ".nzb", fmt.Sprintf("%d.nzb", id))
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
	if err := os.MkdirAll(filepath.Join(e.Dir, ".nzb"), 0o750); err != nil {
		return d, fmt.Errorf("make the downloads folder: %w", err)
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
	if err := os.WriteFile(e.nzbPath(d.ID), nzbData, 0o640); err != nil {
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
	if d.Status != store.DownloadQueued && d.Status != store.DownloadDownloading {
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
	if d.Status != store.DownloadFailed || d.Quarantined != "" || len(d.DoneFiles) == 0 || d.Folder == "" {
		return false
	}
	if _, err := os.Stat(e.nzbPath(d.ID)); err != nil {
		return false
	}
	dir, ok := e.inside(d.Folder)
	return ok && len(listFiles(dir)) > 0
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
	return ok && len(listFiles(dir)) > 0
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
	switch {
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
	return lead + "Moved into the library at " + to
}

// Remove takes a download off the queue or out of the history; with
// deleteFiles, its folder in the downloads folder goes too.
func (e *Engine) Remove(ctx context.Context, id int64, deleteFiles bool) error {
	d, err := e.Store.Download(ctx, id)
	if err != nil {
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

// CheckFolder says what's wrong with the downloads folder, if anything:
// it must take writes, and mustn't be a library folder or inside one.
func (e *Engine) CheckFolder(ctx context.Context) error {
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
	if err := os.MkdirAll(e.Dir, 0o750); err != nil {
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
	// The month's data cap: the download that's on goes on, and the next
	// waits for next month.
	if c, err := LoadCap(ctx, e.Store); err != nil {
		return
	} else if c.Level == CapFull {
		e.waiting.Store(c.Why())
		return
	}
	if err := e.CheckFolder(ctx); err != nil {
		e.Store.SetDownloadStatus(ctx, d.ID, store.DownloadPaused, err.Error())
		return
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
		// The next download starts while this one is checked and unpacked.
		e.mu.Lock()
		if e.current == r {
			e.current = nil
		}
		e.mu.Unlock()
		e.Wake()
		select {
		case e.post <- struct{}{}:
		case <-ctx.Done():
			err = ctx.Err()
		}
		if err == nil {
			err = e.postProcess(ctx, r, &d, job)
			<-e.post
		}
	}
	bg := context.WithoutCancel(ctx)
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
	default:
		e.Log.Warn("download failed", "name", d.Name, "err", err)
		e.Store.SetDownloadStatus(bg, d.ID, store.DownloadFailed, sentence(err.Error()))
		if d.Quarantined != "" {
			e.notify(bg, d, store.EventQuarantined, err.Error())
		} else {
			e.notify(bg, d, store.EventFailed, err.Error())
		}
		if e.Failed != nil {
			e.Failed(bg, d, sentence(err.Error()))
		}
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
