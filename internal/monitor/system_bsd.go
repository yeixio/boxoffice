//go:build darwin || freebsd || netbsd || openbsd

package monitor

import (
	"encoding/binary"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func read() reading {
	r := reading{scope: ScopeProcess}
	var self, children syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &self) == nil && syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children) == nil {
		sec := func(tv syscall.Timeval) float64 { return float64(tv.Sec) + float64(tv.Usec)/1e6 }
		// Finished child processes count once they're done.
		r.procCPU = sec(self.Utime) + sec(self.Stime) + sec(children.Utime) + sec(children.Stime)
		r.procOK = true
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	r.procMem = ms.Sys
	for _, name := range []string{"hw.memsize", "hw.physmem"} {
		if v, err := unix.SysctlUint64(name); err == nil && v > 0 {
			r.memTotal = v
			break
		}
	}
	if b, err := unix.SysctlRaw("vm.loadavg"); err == nil && len(b) >= 24 {
		// struct loadavg { fixpt_t ldavg[3]; long fscale; }
		scale := float64(binary.LittleEndian.Uint64(b[16:24]))
		if scale > 0 {
			for i := 0; i < 3; i++ {
				r.load = append(r.load, float64(binary.LittleEndian.Uint32(b[i*4:]))/scale)
			}
		}
	}
	return r
}

func diskUsage(path string) (Disk, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}, false
	}
	return Disk{Total: st.Blocks * uint64(st.Bsize), Free: uint64(st.Bavail) * uint64(st.Bsize)}, true
}
