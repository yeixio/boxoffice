// Package flush keeps big writes from piling up in memory.
//
// Written data sits in the page cache, as dirty pages, until the kernel
// writes it out, and in a container those pages count against its memory
// limit. A network share (SMB, NFS) writes out slowly, so a download or
// unpack saving to one can fill the limit with them and be killed for it,
// though Box Office itself uses little. Flushing every so often, and
// dropping what was flushed from the cache, keeps them bounded.
package flush

import (
	"io"
	"os"
	"sync/atomic"
)

// Every is how much is written to a file before it's flushed.
const Every = 32 << 20

// File writes what's been written to f out to disk, and drops it from the
// page cache. Errors are left for the writes and the close to report.
func File(f *os.File) {
	f.Sync()
	dropCache(f)
}

// Counter flushes a file each time Every more bytes were written to it, by
// any number of writers at once.
type Counter struct{ n atomic.Int64 }

// Wrote counts n bytes written to f, flushing it if they add up to Every.
func (c *Counter) Wrote(f *os.File, n int) {
	if c.n.Add(int64(n)) >= Every {
		c.n.Store(0)
		File(f)
	}
}

// Writer writes to a file, flushing it every Every bytes.
type Writer struct {
	F *os.File
	c Counter
}

func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.F.Write(p)
	w.c.Wrote(w.F, n)
	return n, err
}

var _ io.Writer = (*Writer)(nil)
