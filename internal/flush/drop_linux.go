package flush

import (
	"os"

	"golang.org/x/sys/unix"
)

func dropCache(f *os.File) { unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED) }
