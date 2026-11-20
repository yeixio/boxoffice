package relocate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/flush"
)

// entry is one file copied: where it is under the title's folder, its size
// and modification time when it was copied, and its SHA-256. Only files
// that still have that size and time are removed from the original.
type entry struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // Unix nanoseconds
	Sum  string `json:"sum"`
}

// afterCopy, in tests, runs once a title is copied and checked.
var afterCopy func()

// errChanged means the original changed while it was copied.
var errChanged = errors.New("files in its folder changed while it was being copied")

// copyTree copies the folder from to dest, a new folder, file by file,
// and then reads every copy back to check it matches the original byte for
// byte (by SHA-256). It says what it copied. Nothing in from is changed.
func (s *Service) copyTree(ctx context.Context, from, dest string, p *live) ([]entry, error) {
	if err := os.Mkdir(dest, 0o775); err != nil {
		return nil, err
	}
	var list []entry
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dest, rel)
		switch {
		case rel == ".":
			return nil
		case d.IsDir():
			return os.Mkdir(to, 0o775)
		case d.Type().IsRegular():
			e, err := s.copyFile(ctx, path, to, p)
			if err != nil {
				return err
			}
			e.Rel = rel
			list = append(list, e)
			return nil
		}
		return fmt.Errorf("%s is a link or special file, which Box Office doesn't move", rel)
	})
	if err != nil {
		return nil, err
	}
	// Read every copy back: the new disk must hold what the old one does.
	p.phase("checking", 0)
	for _, e := range list {
		sum, err := s.hashFile(ctx, filepath.Join(dest, e.Rel), p)
		if err != nil {
			return nil, err
		}
		if sum != e.Sum {
			return nil, fmt.Errorf("the copy of %s doesn't match the original", e.Rel)
		}
	}
	if afterCopy != nil {
		afterCopy()
	}
	// What was copied must still be all there is, unchanged.
	if !unchanged(from, list) {
		return nil, errChanged
	}
	return list, nil
}

// unchanged says the folder from holds exactly the files in list, with
// the same sizes and times.
func unchanged(from string, list []entry) bool {
	want := map[string]entry{}
	for _, e := range list {
		want[e.Rel] = e
	}
	n := 0
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		e, ok := want[rel]
		info, ierr := d.Info()
		if !ok || ierr != nil || !d.Type().IsRegular() || info.Size() != e.Size || info.ModTime().UnixNano() != e.Mod {
			return errChanged
		}
		n++
		return nil
	})
	return err == nil && n == len(list)
}

// copyFile copies one file, hashing it as it goes, and writes it out to
// disk before saying it's done.
func (s *Service) copyFile(ctx context.Context, src, dest string, p *live) (entry, error) {
	in, err := os.Open(src)
	if err != nil {
		return entry{}, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return entry{}, err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o664)
	if err != nil {
		return entry{}, err
	}
	p.file(filepath.Base(src))
	h := sha256.New()
	w := &flush.Writer{F: out}
	n, err := s.pump(ctx, io.MultiWriter(w, h), in, p, true)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != info.Size() {
		err = fmt.Errorf("the copy of %s came out %d bytes, not %d", filepath.Base(src), n, info.Size())
	}
	if err == nil {
		if st, serr := os.Stat(dest); serr != nil || st.Size() != n {
			err = fmt.Errorf("the copy of %s didn't write out whole", filepath.Base(src))
		}
	}
	if err != nil {
		return entry{}, err
	}
	// Keep its time, so players and scans see the same file as before.
	os.Chtimes(dest, info.ModTime(), info.ModTime())
	return entry{Size: n, Mod: info.ModTime().UnixNano(), Sum: hex.EncodeToString(h.Sum(nil))}, nil
}

// hashFile reads a file through and says its SHA-256.
func (s *Service) hashFile(ctx context.Context, path string, p *live) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	p.file(filepath.Base(path))
	h := sha256.New()
	if _, err := s.pump(ctx, h, f, p, false); err != nil {
		return "", err
	}
	flush.File(f) // what was read needn't stay in memory
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pump copies r to w a megabyte at a time, counting it in p and keeping
// to the speed limit while copying. It stops when ctx ends.
func (s *Service) pump(ctx context.Context, w io.Writer, r io.Reader, p *live, copying bool) (int64, error) {
	buf := make([]byte, 1<<20)
	var n int64
	start, sent := time.Now(), int64(0)
	limit := s.speed.Load()
	for {
		if err := context.Cause(ctx); err != nil {
			return n, err
		}
		k, rerr := r.Read(buf)
		if k > 0 {
			if _, err := w.Write(buf[:k]); err != nil {
				return n, err
			}
			n += int64(k)
			p.add(int64(k))
			if copying {
				if l := s.speed.Load(); l != limit {
					limit, start, sent = l, time.Now(), 0
				}
				sent += int64(k)
				if limit > 0 {
					due := time.Duration(float64(sent) / float64(limit*mb) * float64(time.Second))
					if wait := due - time.Since(start); wait > 0 {
						select {
						case <-ctx.Done():
							return n, context.Cause(ctx)
						case <-time.After(wait):
						}
					}
				}
			}
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

const mb = 1 << 20

// live is how the title being moved is getting on, for the page.
type live struct {
	mu     sync.Mutex
	stage  string // copying, checking or removing
	done   int64  // bytes of this stage
	size   int64
	name   string
	copied int64 // bytes copied, for the speed
	marks  []mark
}

type mark struct {
	at time.Time
	n  int64
}

// Progress is how the title being moved is getting on.
type Progress struct {
	Stage string `json:"stage"` // copying, checking or removing
	Done  int64  `json:"done"`  // bytes of this stage
	Size  int64  `json:"size"`
	File  string `json:"file,omitempty"`
	// Speed is bytes copied a second, over the last few seconds.
	Speed int64 `json:"speed"`
}

func newLive(size int64) *live { return &live{stage: "copying", size: size} }

func (p *live) add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	if p.stage != "copying" {
		return
	}
	p.copied += n
	now := time.Now()
	if len(p.marks) == 0 || now.Sub(p.marks[len(p.marks)-1].at) >= 500*time.Millisecond {
		p.marks = append(p.marks, mark{now, p.copied})
		if len(p.marks) > 20 {
			p.marks = p.marks[len(p.marks)-20:]
		}
	}
}

func (p *live) phase(stage string, done int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stage, p.done, p.marks = stage, done, nil
}

func (p *live) file(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name = name
}

func (p *live) progress() Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := Progress{Stage: p.stage, Done: p.done, Size: p.size, File: p.name}
	if len(p.marks) >= 2 {
		a, b := p.marks[0], p.marks[len(p.marks)-1]
		if d := b.at.Sub(a.at).Seconds(); d > 0 {
			out.Speed = int64(float64(b.n-a.n) / d)
		}
	}
	return out
}
