package monitor

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// clockTicks is USER_HZ, which Linux keeps at 100 on every architecture
// Box Office runs on.
const clockTicks = 100

func read() reading {
	var r reading
	r.hostBusy, r.hostTotal, r.hostOK = hostCPU()
	r.memTotal, r.memAvailable = meminfo()
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(f); i++ {
			v, _ := strconv.ParseFloat(f[i], 64)
			r.load = append(r.load, v)
		}
	}
	r.scope = ScopeGroup
	if dir, ok := ownCgroup(); ok {
		// In a container (or a systemd service) the cgroup holds the server
		// and every program it runs, and has the container's limits.
		if usec, ok := cgroupValue(filepath.Join(dir, "cpu.stat"), "usage_usec"); ok {
			r.procCPU, r.procOK = float64(usec)/1e6, true
		}
		if v, ok := readUint(filepath.Join(dir, "memory.current")); ok {
			r.procMem = v
			// The page cache counts toward memory.current, but the kernel
			// gives it back when it needs to; what's left is what Box Office
			// really holds, as container tools show it.
			if inactive, ok := cgroupValue(filepath.Join(dir, "memory.stat"), "inactive_file"); ok && inactive < v {
				r.procMem = v - inactive
			}
		}
		if v, ok := readUint(filepath.Join(dir, "memory.max")); ok {
			r.memLimit = v
		}
		if b, err := os.ReadFile(filepath.Join(dir, "cpu.max")); err == nil {
			f := strings.Fields(string(b))
			if len(f) == 2 && f[0] != "max" {
				quota, _ := strconv.ParseFloat(f[0], 64)
				period, _ := strconv.ParseFloat(f[1], 64)
				if period > 0 {
					r.cpuLimit = quota / period
				}
			}
		}
	}
	if !r.procOK || r.procMem == 0 {
		// The server and the programs it started.
		cpu, mem := processTree()
		if !r.procOK {
			r.procCPU, r.procOK = cpu, true
		}
		if r.procMem == 0 {
			r.procMem = mem
		}
	}
	if r.memLimit >= r.memTotal && r.memTotal > 0 {
		r.memLimit = 0 // "no limit" can also be written as a huge number
	}
	return r
}

func hostCPU() (busy, total float64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	// user nice system idle iowait irq softirq steal (guest time is
	// already counted in user)
	for i, s := range fields[1:] {
		if i >= 8 {
			break
		}
		v, _ := strconv.ParseFloat(s, 64)
		total += v
		if i != 3 && i != 4 {
			busy += v
		}
	}
	return busy, total, total > 0
}

func meminfo() (total, available uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	return total, available
}

// ownCgroup finds this process's cgroup (v2) when it's one that holds just
// Box Office: a container's, or a systemd service's. A login session's would
// count everything else running there too.
func ownCgroup() (string, bool) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		own := path == "/" || strings.HasSuffix(path, ".service") ||
			strings.Contains(path, "kubepods") || strings.Contains(path, "docker-") || strings.Contains(path, "libpod-")
		if !own {
			return "", false
		}
		dir := filepath.Join("/sys/fs/cgroup", path)
		if _, err := os.Stat(filepath.Join(dir, "cpu.stat")); err != nil {
			return "", false
		}
		return dir, true
	}
	return "", false
}

func readUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

// cgroupValue reads one "key value" line of a cgroup stat file.
func cgroupValue(path, key string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		k, v, ok := bytes.Cut(line, []byte(" "))
		if ok && string(k) == key {
			n, err := strconv.ParseUint(string(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// processTree adds up the CPU time and memory of this process and its
// children (the programs it runs).
func processTree() (cpu float64, mem uint64) {
	self := os.Getpid()
	page := uint64(os.Getpagesize())
	add := func(pid string, withChildren bool) (ppid int) {
		b, err := os.ReadFile("/proc/" + pid + "/stat")
		if err != nil {
			return 0
		}
		// The name, in parentheses, can have spaces; the fields after it
		// start with the state.
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			return 0
		}
		f := strings.Fields(string(b[i+1:]))
		if len(f) < 22 {
			return 0
		}
		ppid, _ = strconv.Atoi(f[1])
		utime, _ := strconv.ParseFloat(f[11], 64)
		stime, _ := strconv.ParseFloat(f[12], 64)
		cpu += (utime + stime) / clockTicks
		if withChildren {
			// Children that have finished.
			cutime, _ := strconv.ParseFloat(f[13], 64)
			cstime, _ := strconv.ParseFloat(f[14], 64)
			cpu += (cutime + cstime) / clockTicks
		}
		rss, _ := strconv.ParseUint(f[21], 10, 64)
		mem += rss * page
		return ppid
	}
	add("self", true)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return cpu, mem
	}
	for _, e := range entries {
		name := e.Name()
		if name[0] < '0' || name[0] > '9' || name == strconv.Itoa(self) {
			continue
		}
		// Cheap check first: only children of this process count.
		b, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue
		}
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b[i+1:]))
		if len(f) > 1 && f[1] == strconv.Itoa(self) {
			add(name, false)
		}
	}
	return cpu, mem
}

func diskUsage(path string) (Disk, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Disk{}, false
	}
	return Disk{Total: st.Blocks * uint64(st.Bsize), Free: st.Bavail * uint64(st.Bsize)}, true
}
