package downloader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
	"github.com/yeixio/boxoffice/internal/usenet/nntp"
	"github.com/yeixio/boxoffice/internal/usenet/nzb"
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
		return nil, err
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
	if err := e.download(ctx, r, d, srv, job, want); err != nil {
		return nil, err
	}
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
	for _, f := range files {
		if slices.Contains(d.DoneFiles, f.index) {
			if _, err := os.Stat(filepath.Join(job.dir, f.name)); err == nil {
				done += f.file.Bytes()
				continue
			}
			d.DoneFiles = slices.DeleteFunc(d.DoneFiles, func(i int) bool { return i == f.index })
		}
		tmp := filepath.Join(job.dir, fmt.Sprintf(".boxoffice-%d.part", f.index))
		fh, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o640)
		if err != nil {
			return err
		}
		st := &fileState{nf: f, fh: fh, tmp: tmp}
		st.remaining.Store(int32(len(f.file.Segments)))
		states = append(states, st)
	}
	defer func() {
		for _, st := range states {
			if st.fh != nil {
				st.fh.Close()
				os.Remove(st.tmp)
			}
		}
	}()
	r.bytes.Store(d.DoneBytes)
	if d.DoneBytes == 0 {
		r.bytes.Store(done)
	}
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
		if st.yname != "" {
			name = st.yname
		}
		st.mu.Unlock()
		name = cleanName(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
		if name == "" || strings.HasPrefix(name, ".boxoffice-") {
			name = fmt.Sprintf("file-%d", st.nf.index)
		}
		if err := st.fh.Close(); err != nil {
			return err
		}
		st.fh = nil
		final := filepath.Join(job.dir, name)
		if err := os.Rename(st.tmp, final); err != nil {
			return err
		}
		mu.Lock()
		st.nf.name = name
		d.DoneFiles = append(d.DoneFiles, st.nf.index)
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
						cancel(fmt.Errorf("%s of this release is gone from your news servers, and it has no par2 files to repair it; try another release", humanBytes(lost)))
					} else {
						cancel(fmt.Errorf("too much of this release is gone from your news servers (%s missing) to repair; try another release", humanBytes(lost)))
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
	wg.Wait()
	mu.Lock()
	d.DoneBytes, d.FailedBytes = r.bytes.Load(), r.failed.Load()
	mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return nil
}

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
		if attempt >= 6 {
			return fmt.Errorf("the news servers stopped answering: %w", err)
		}
		select {
		case <-time.After(10 * time.Second):
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
		return fmt.Errorf("write to the downloads folder: %w", err)
	}
	return nil
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
