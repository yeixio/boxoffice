package downloader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yeixio/boxoffice/internal/flush"
	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp"
	"github.com/yeixio/boxoffice/internal/usenet/nzb"
	"github.com/yeixio/boxoffice/internal/usenet/unpack"
	"github.com/yeixio/boxoffice/internal/usenet/yenc"
)

type nzbFile struct {
	index  int
	file   nzb.File
	name   string // the file's name: from the subject, then from yEnc
	par2   bool
	blocks int // recovery blocks, for a par2 volume
	held   bool
}

type fetchJob struct {
	dir   string
	nzb   *nzb.NZB
	files []*nzbFile
}

func (j *fetchJob) held() []*nzbFile {
	var out []*nzbFile
	for _, f := range j.files {
		if f.held {
			out = append(out, f)
		}
	}
	return out
}

// fetch downloads every file of the release except par2 volumes, which wait
// until a check shows they're needed.
func (e *Engine) fetch(ctx context.Context, r *running, d *store.Download, srv *servers) (*fetchJob, error) {
	r.setStage("")
	raw, err := os.ReadFile(e.nzbPath(d.ID))
	if err != nil {
		return nil, errors.New("the NZB for this download is gone")
	}
	n, err := nzb.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	if d.Folder == "" {
		d.Folder = e.uniqueFolder(d.Name)
	}
	dir, ok := e.inside(d.Folder)
	if !ok {
		return nil, errors.New("the download's folder isn't in the downloads folder")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, local(err)
	}
	// A folder of that name another app made (NZBGet, say, as another
	// user) may not take Box Office's files. If nothing of this download
	// is in it yet, it gets a fresh folder of its own instead.
	if !writable(dir) && len(d.DoneFiles) == 0 {
		old := d.Folder
		d.Folder = e.uniqueFolder(d.Name)
		if dir, ok = e.inside(d.Folder); !ok {
			return nil, errors.New("the download's folder isn't in the downloads folder")
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, local(err)
		}
		e.Log.Info("download's folder isn't writable; using another", "name", d.Name, "id", d.ID, "was", old, "now", d.Folder)
		e.Store.SaveDownloadProgress(context.WithoutCancel(ctx), *d)
	}
	job := &fetchJob{dir: dir, nzb: n}
	hasIndex := false
	for i, f := range n.Files {
		nf := &nzbFile{index: i, file: f, name: f.Name()}
		nf.par2, nf.blocks = nzb.IsPar2(nf.name)
		if nf.par2 && nf.blocks == 0 {
			hasIndex = true
		}
		job.files = append(job.files, nf)
	}
	// Recovery volumes wait. With no index file, the smallest volume is
	// fetched instead, as it carries the same description of the set.
	vols := slices.DeleteFunc(slices.Clone(job.files), func(f *nzbFile) bool { return !f.par2 || f.blocks == 0 })
	slices.SortFunc(vols, func(a, b *nzbFile) int { return a.blocks - b.blocks })
	for i, f := range vols {
		f.held = hasIndex || i > 0
	}
	var want []*nzbFile
	var size int64
	for _, f := range job.files {
		if !f.held {
			want = append(want, f)
			size += f.file.Bytes()
		}
	}
	d.Size = size
	r.size.Store(size)
	e.Store.SaveDownloadProgress(context.WithoutCancel(ctx), *d)
	e.mu.Lock()
	again := e.again[d.ID]
	delete(e.again, d.ID)
	e.mu.Unlock()
	left := 0
	for _, f := range want {
		if !slices.Contains(d.DoneFiles, f.index) {
			left++
		}
	}
	e.Log.Info("download started", "name", d.Name, "id", d.ID, "files", len(job.files), "fetching", left, "already", len(want)-left, "size", size,
		"par2 index", hasIndex, "folder", d.Folder, "again", again)
	// Fetched before Box Office stopped (or the download was paused)
	// while it was waiting to be unpacked, or being checked, unpacked or
	// moved: its files are here, and unpacking may already have deleted
	// the archives, so nothing is fetched again. Only if the folder is
	// empty does it start over.
	resumed := !again && d.Fetched && len(listFiles(dir)) > 0
	if resumed {
		e.Log.Info("download was already fetched; carrying on from checking and unpacking", "name", d.Name, "id", d.ID)
	}
	if again || resumed {
		// Checking and unpacking again: the files are already here,
		// whatever they've been renamed or unpacked to.
		r.bytes.Store(d.DoneBytes)
		r.failed.Store(d.FailedBytes)
		return job, nil
	}
	start := time.Now()
	if err := e.download(ctx, r, d, srv, job, want); err != nil {
		return nil, err
	}
	took := time.Since(start)
	e.Log.Info("download fetched", "name", d.Name, "id", d.ID, "bytes", r.bytes.Load(), "missing", r.failed.Load(),
		"took", took.Round(time.Second).String(), "average", humanBytes(int64(float64(r.bytes.Load())/max(took.Seconds(), 1)))+"/s")
	return job, nil
}

func (e *Engine) uniqueFolder(name string) string {
	base := cleanName(name)
	if base == "" {
		base = "download"
	}
	for i := 0; ; i++ {
		f := base
		if i > 0 {
			f = fmt.Sprintf("%s.%d", base, i)
		}
		if _, err := os.Stat(filepath.Join(e.Dir, f)); errors.Is(err, os.ErrNotExist) {
			return f
		}
	}
}

type fileState struct {
	nf        *nzbFile
	fh        *os.File
	tmp       string
	remaining atomic.Int32
	mu        sync.Mutex
	yname     string
	written   flush.Counter
}

type segment struct {
	fs  *fileState
	seg nzb.Segment
}

// download fetches some of a job's files, carrying on from what an earlier
// try fetched.
func (e *Engine) download(ctx context.Context, r *running, d *store.Download, srv *servers, job *fetchJob, files []*nzbFile) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var states []*fileState
	var done int64
	// Each try writes to part files of its own. A part file an earlier try
	// left (Box Office stopped mid-download, say) can stay locked on a
	// network share for a while, and opening it again is refused; so old
	// ones are cleared away if they can be, and never reused.
	clearParts(job.dir)
	try := strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, f := range files {
		if slices.Contains(d.DoneFiles, f.index) {
			if name, ok := doneName(job.dir, d, f); ok {
				f.name = name
				done += f.file.Bytes()
				continue
			}
			d.DoneFiles = slices.DeleteFunc(d.DoneFiles, func(i int) bool { return i == f.index })
			delete(d.DoneNames, f.index)
		}
		tmp := filepath.Join(job.dir, fmt.Sprintf(".boxoffice-%d-%s.part", f.index, try))
		fh, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o640)
		if err != nil {
			return local(shareTrouble(fmt.Errorf("Box Office couldn't make a file in its download folder %s: %w", job.dir, err)))
		}
		st := &fileState{nf: f, fh: fh, tmp: tmp}
		st.remaining.Store(int32(len(f.file.Segments)))
		states = append(states, st)
	}
	cleanup := func() {
		for _, st := range states {
			if st.fh != nil {
				st.fh.Close()
				os.Remove(st.tmp)
			}
		}
	}
	// Carrying on: what's done is the files that are here. What the part
	// files of an earlier try held is fetched again, so it isn't counted.
	for _, f := range job.files {
		if slices.Contains(d.DoneFiles, f.index) && !slices.Contains(files, f) {
			done += f.file.Bytes()
		}
	}
	r.bytes.Store(done)
	// What can be lost and still repaired: the recovery volumes' share.
	var allowance int64
	for _, f := range job.held() {
		allowance += f.file.Bytes()
	}
	queue := make(chan segment)
	go func() {
		defer close(queue)
		for _, st := range states {
			for _, s := range st.nf.file.Segments {
				select {
				case queue <- segment{st, s}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	var mu sync.Mutex
	lastSave := time.Now()
	finishFile := func(st *fileState) error {
		st.mu.Lock()
		name := st.nf.name
		if st.yname != "" && !(hashedName(st.yname) && !hashedName(name) && sameExt(name, st.yname)) {
			name = st.yname
		}
		st.mu.Unlock()
		name = cleanName(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
		if name == "" || strings.HasPrefix(name, ".boxoffice-") {
			name = fmt.Sprintf("file-%d", st.nf.index)
		}
		err := closePart(st.fh)
		st.fh = nil
		// A try that was stopped (it stalled on the share, say) while its
		// last write hung must not finish a file late: the next try may
		// have saved it already, and be unpacking it, and renaming over
		// it then pulls it away mid-read ("read …part08.rar: no such
		// file or directory"). Its part file is just dropped.
		if err == nil && ctx.Err() != nil {
			os.Remove(st.tmp)
			return context.Cause(ctx)
		}
		if err != nil {
			os.Remove(st.tmp)
			return shareTrouble(err)
		}
		final := filepath.Join(job.dir, name)
		if err := os.Rename(st.tmp, final); err != nil {
			return shareTrouble(err)
		}
		// A release locked with a password, and none given, can never be
		// unpacked. Any one volume of the archive says so, so it's
		// stopped as soon as the first is here, not when all of it is.
		if d.Password == "" && !st.nf.par2 && unpack.Locked(final) {
			e.Log.Info("download is locked with a password, and none was given; discarding it", "name", d.Name, "id", d.ID, "file", name)
			return lockedError(false)
		}
		mu.Lock()
		st.nf.name = name
		d.DoneFiles = append(d.DoneFiles, st.nf.index)
		if d.DoneNames == nil {
			d.DoneNames = map[int]string{}
		}
		d.DoneNames[st.nf.index] = name
		d.DoneBytes = r.bytes.Load()
		d.FailedBytes = r.failed.Load()
		save := time.Since(lastSave) > 3*time.Second
		if save {
			lastSave = time.Now()
		}
		mu.Unlock()
		if save {
			e.Store.SaveDownloadProgress(context.WithoutCancel(ctx), *d)
		}
		return nil
	}
	workers := min(srv.connections(), 64)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 0, 1<<20)
			for s := range queue {
				if err := e.segment(ctx, r, srv, s, &buf); err != nil {
					cancel(err)
					return
				}
				if lost := r.failed.Load(); lost > allowance+allowance/10+64<<10 {
					if allowance == 0 {
						cancel(hopeless(fmt.Errorf("%s of this release is gone from your news servers, and it has no par2 files to repair it; try another release", humanBytes(lost))))
					} else {
						cancel(hopeless(fmt.Errorf("too much of this release is gone from your news servers (%s missing) to repair; try another release", humanBytes(lost))))
					}
					return
				}
				if s.fs.remaining.Add(-1) == 0 {
					if err := finishFile(s.fs); err != nil {
						cancel(err)
						return
					}
				}
			}
		}()
	}
	// A download that gets nowhere for stallWait is stuck: a share that
	// hung mid-write, most likely. It's stopped and tried again, and if
	// its writes still haven't come back a while after, they're left to
	// finish on their own, so the queue isn't held up by them.
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	stuck := e.watchStall(ctx, cancel, r, waited)
	select {
	case <-waited:
		cleanup()
	case <-stuck:
		e.Log.Warn("a download's writes to the downloads folder never came back; leaving them and going on", "name", d.Name, "id", d.ID)
		go func() { <-waited; cleanup() }()
	}
	mu.Lock()
	d.DoneBytes, d.FailedBytes = r.bytes.Load(), r.failed.Load()
	mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return nil
}

// stallWait is how long a download may get nowhere before it's taken for
// stuck, and stallGrace how long it's then given to stop.
var stallWait, stallGrace = 10 * time.Minute, time.Minute

// watchStall cancels a download that gets nowhere for stallWait, as the
// downloads folder stopped answering, and closes the channel it returns
// if it still hasn't stopped stallGrace after that (or after it was
// stopped for any other reason).
func (e *Engine) watchStall(ctx context.Context, cancel context.CancelCauseFunc, r *running, done <-chan struct{}) <-chan struct{} {
	stuck := make(chan struct{})
	// Read once, here: the watch can outlive a download stuck on a hung
	// share, and tests set these.
	wait, grace := stallWait, stallGrace
	go func() {
		tick := time.NewTicker(min(wait/4, 30*time.Second))
		defer tick.Stop()
		last, at := r.bytes.Load(), time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				// Stopped (paused, removed, or Box Office is stopping):
				// it gets as long to finish.
				select {
				case <-done:
				case <-time.After(grace):
					close(stuck)
				}
				return
			case <-tick.C:
			}
			if b := r.bytes.Load(); b != last {
				last, at = b, time.Now()
				continue
			}
			if time.Since(at) < wait {
				continue
			}
			cancel(shareError{fmt.Errorf("nothing was saved for %s: %w", wait.Round(time.Second), syscall.ETIMEDOUT)})
			select {
			case <-done:
			case <-time.After(grace):
				close(stuck)
			}
			return
		}
	}()
	return stuck
}

// serverAttempts is how many more times an article is asked for, serverPause
// apart, when no news server answers, before the download stops for it.
var serverAttempts, serverPause = 6, 10 * time.Second

// segment fetches, decodes and writes one article. A missing or damaged
// article isn't an error here: par2 may fix it. A news server that can't
// be reached is, after a minute of trying.
func (e *Engine) segment(ctx context.Context, r *running, srv *servers, s segment, buf *[]byte) error {
	var body []byte
	var err error
	for attempt := 0; ; attempt++ {
		body, err = srv.fetch(ctx, s.seg.MessageID, *buf)
		if err == nil || errors.Is(err, nntp.ErrNoArticle) || ctx.Err() != nil {
			break
		}
		if attempt >= serverAttempts {
			return serverError{fmt.Errorf("the news servers stopped answering: %w", err)}
		}
		e.Log.Debug("news server didn't answer; trying again", "article", s.seg.MessageID, "in", serverPause.String(), "attempt", attempt+1, "err", err)
		select {
		case <-time.After(serverPause):
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	r.bytes.Add(s.seg.Bytes)
	if err != nil {
		r.failed.Add(s.seg.Bytes)
		return nil
	}
	*buf = body[:0]
	e.meter.add(int64(len(body)))
	if err := e.limit.wait(ctx, int64(len(body))); err != nil {
		return context.Cause(ctx)
	}
	p, derr := yenc.Decode(body)
	if p == nil {
		r.failed.Add(s.seg.Bytes)
		return nil
	}
	if derr != nil {
		r.failed.Add(s.seg.Bytes)
	}
	if p.Name != "" {
		s.fs.mu.Lock()
		if s.fs.yname == "" {
			s.fs.yname = p.Name
		}
		s.fs.mu.Unlock()
	}
	if p.Size > 0 && p.End() > p.Size {
		return nil // a part that claims to go past the end of its file
	}
	if _, err := s.fs.fh.WriteAt(p.Data, p.Begin); err != nil {
		return shareTrouble(fmt.Errorf("write to the downloads folder: %w", err))
	}
	// Bounded, so written data waiting to go out to a slow share doesn't
	// fill the memory limit.
	s.fs.written.Wrote(s.fs.fh, len(p.Data))
	return nil
}

// localError is a download that failed for Box Office's own folders (it
// isn't allowed to write there), not for anything wrong with the release:
// it's paused, not failed, so the release isn't blocked.
type localError struct{ error }

func (l localError) Unwrap() error { return l.error }

// local marks err as a localError when it's a permissions problem.
func local(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return localError{err}
	}
	return err
}

// closePart closes a finished part file; tests swap it.
var closePart = (*os.File).Close

// shareError is a write to the downloads folder that failed because the
// folder stopped answering for a while: a network share (SMB, NFS)
// dropping out and coming back says "host is down" or "resource
// temporarily unavailable". The download is tried again, keeping the
// files it finished.
type shareError struct{ error }

func (s shareError) Unwrap() error { return s.error }

func (s shareError) Error() string {
	return "the downloads folder stopped answering while Box Office was saving to it (" + s.why() + "); if it's a network share, check it's up, then download it again"
}

// why is what the system said, in short.
func (s shareError) why() string {
	var errno syscall.Errno
	if errors.As(s.error, &errno) {
		return errno.Error()
	}
	return s.error.Error()
}

// shareTrouble marks err as a shareError when it's the downloads folder
// dropping out, not something that stays wrong (no room, no permission).
func shareTrouble(err error) error {
	for _, e := range []syscall.Errno{syscall.EHOSTDOWN, syscall.EHOSTUNREACH, syscall.EAGAIN, syscall.ESTALE, syscall.EIO,
		syscall.ECONNRESET, syscall.ECONNABORTED, syscall.ETIMEDOUT, syscall.ENETDOWN, syscall.ENETUNREACH, syscall.ENETRESET} {
		if errors.Is(err, e) {
			return shareError{err}
		}
	}
	return err
}

// reHashed is a name made of nothing but a long run of hex digits or
// letters and digits, as obfuscated posts name their files.
var reHashed = regexp.MustCompile(`^(?i:[0-9a-f]{16,}|[0-9a-z]{24,})$`)

// hashedName says whether a file's name, without its extension, is a
// hash: what an obfuscated post calls a file in place of its real name.
// The NZB's subject may still give the real name (earth.2.s01e09-med.r00
// for 6f2d940f….r00), and that's the one kept, as old-style RAR sets
// find each next volume by name.
func hashedName(name string) bool {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	return reHashed.MatchString(strings.TrimSuffix(base, filepath.Ext(base)))
}

// sameExt says whether two names end alike (.r00 and .R00), so a subject
// that names another file isn't taken for this one's.
func sameExt(a, b string) bool {
	ea := filepath.Ext(a)
	return ea != "" && strings.EqualFold(ea, filepath.Ext(b)) && !strings.ContainsAny(a, "/\\")
}

// serverError is a download that stopped because the news servers
// couldn't be reached for a while (their name didn't look up, they didn't
// answer, they turned down the sign-in): nothing to do with the release,
// so it's tried again later, not blocked.
type serverError struct{ error }

func (s serverError) Unwrap() error { return s.error }

// why is what went wrong, in short: the last part of the chain, as
// "lookup reader.example.com: i/o timeout".
func (s serverError) why() string {
	msg := s.error.Error()
	if i := strings.Index(msg, "dial tcp: "); i >= 0 {
		return msg[i+len("dial tcp: "):]
	}
	return strings.TrimPrefix(msg, "the news servers stopped answering: ")
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// doneName finds a file an earlier try fetched: under the name it was
// saved as, or, for one saved before those were kept, its name in the NZB.
func doneName(dir string, d *store.Download, f *nzbFile) (string, bool) {
	for _, n := range []string{d.DoneNames[f.index], f.name} {
		if n == "" || n != filepath.Base(n) {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.Mode().IsRegular() {
			return n, true
		}
	}
	return "", false
}

// removePart deletes a part file; tests swap it for an SMB share, where
// one still open can't be deleted.
var removePart = os.Remove

// clearParts deletes the part files earlier tries left in dir, as far as
// it can; one that can't go yet is left for the folder's cleanup.
func clearParts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		if n := de.Name(); strings.HasPrefix(n, ".boxoffice-") && strings.HasSuffix(n, ".part") && de.Type().IsRegular() {
			removePart(filepath.Join(dir, n))
		}
	}
}
