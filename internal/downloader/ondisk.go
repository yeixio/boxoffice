package downloader

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// diskWait is how long a page waits on the downloads folder before it
// says the folder isn't answering. When a network share drops, reading
// it can hang for minutes before failing (a soft SMB mount gives up only
// after several), and a page mustn't hang with it.
var diskWait = 3 * time.Second

// stuckWait is how long a page waits instead while another read of the
// downloads folder is already stuck: the share is most likely away, so
// there's no point waiting the whole diskWait each time.
var stuckWait = 250 * time.Millisecond

// diskCall is a read of the downloads folder under way.
type diskCall struct {
	done  chan struct{}
	val   any
	err   error
	stuck bool // someone gave up waiting on it
}

// onDisk runs fn, which reads the downloads folder, and waits at most
// diskWait for it before saying the folder isn't answering. fn carries
// on in the background if it's slow, and asking again with the same key
// meanwhile waits on that call instead of starting another, so a hung
// share doesn't pile up stuck reads with every refresh of a page.
func onDisk[T any](e *Engine, ctx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	e.diskMu.Lock()
	if e.disk == nil {
		e.disk = map[string]*diskCall{}
	}
	c := e.disk[key]
	if c == nil {
		c = &diskCall{done: make(chan struct{})}
		e.disk[key] = c
		bg := context.WithoutCancel(ctx)
		go func() {
			v, err := fn(bg)
			e.diskMu.Lock()
			c.val, c.err = v, err
			delete(e.disk, key)
			if c.stuck {
				e.diskStuck--
			}
			e.diskMu.Unlock()
			close(c.done)
		}()
	}
	wait := diskWait
	if e.diskStuck > 0 {
		wait = min(wait, stuckWait)
	}
	e.diskMu.Unlock()

	var zero T
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-c.done:
		v, _ := c.val.(T)
		return v, c.err
	case <-t.C:
		e.diskMu.Lock()
		select {
		case <-c.done: // it finished just now
		default:
			if !c.stuck {
				c.stuck = true
				e.diskStuck++
			}
		}
		e.diskMu.Unlock()
		return zero, e.folderAwayError()
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// FolderProblem is CheckFolder for a page: it says what's wrong with the
// downloads folder, but waits only diskWait for a share that hung.
func (e *Engine) FolderProblem(ctx context.Context) error {
	_, err := onDisk(e, ctx, "check", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, e.CheckFolder(ctx)
	})
	return err
}

// Actions is what can be done by hand with a download in the history.
type Actions struct {
	Reprocess, Import bool
}

// HistoryActions says, for each download, whether it can be checked and
// unpacked again or moved into the library, which hangs on whether its
// files are still in the downloads folder. If the folder doesn't answer
// in time, neither can: err says why.
func (e *Engine) HistoryActions(ctx context.Context, list []store.Download) ([]Actions, error) {
	ids := make([]string, len(list))
	for i, d := range list {
		ids[i] = strconv.FormatInt(d.ID, 10)
	}
	list = slices.Clone(list)
	out, err := onDisk(e, ctx, "actions:"+strings.Join(ids, ","), func(context.Context) ([]Actions, error) {
		out := make([]Actions, len(list))
		for i, d := range list {
			out[i] = Actions{Reprocess: e.CanReprocess(d), Import: e.CanImport(d)}
		}
		return out, nil
	})
	if err != nil {
		return make([]Actions, len(list)), err
	}
	return out, nil
}

// hasFiles is whether dir holds a file, anywhere in it, other than Box
// Office's own: the same as len(listFiles(dir)) > 0, but it stops at the
// first, as on a network share walking a whole release takes a while.
func hasFiles(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || !de.Type().IsRegular() || strings.HasPrefix(de.Name(), ".boxoffice-") {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
}
