package monitor

import (
	"context"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Interval is how often the monitor takes a reading, and HistoryLength how
// many it keeps: an hour's worth.
const (
	Interval      = 5 * time.Second
	HistoryLength = 720
)

// Traffic counts what the server sends and the requests it answers.
type Traffic struct {
	bytesOut     atomic.Int64
	requests     atomic.Int64
	serverErrors atomic.Int64
}

// Sent counts bytes sent.
func (t *Traffic) Sent(n int) { t.bytesOut.Add(int64(n)) }

// Answered counts a request with its status.
func (t *Traffic) Answered(status int) {
	t.requests.Add(1)
	if status >= 500 {
		t.serverErrors.Add(1)
	}
}

// Point is the server at one moment, for the dashboard's charts.
type Point struct {
	Time int64 `json:"t"` // milliseconds
	// CPU is how much of the machine's processors (or the container's
	// share of them) Box Office used, 0 to 100; HostCPU the whole machine's.
	CPU     float64  `json:"cpu"`
	HostCPU *float64 `json:"hostCpu,omitempty"`
	Memory  uint64   `json:"mem"`
	// BytesOut is bytes sent per second; Requests requests per second.
	BytesOut float64 `json:"out"`
	Requests float64 `json:"req"`
	// ServerErrors are requests that failed on the server since the last
	// point, and Problems warnings and errors logged.
	ServerErrors int64 `json:"serverErrors"`
	Problems     int64 `json:"problems"`
	// Downloads is how many downloads were running, and DownloadRate the
	// bytes per second they fetched together.
	Downloads    int     `json:"downloads"`
	DownloadRate float64 `json:"downloadRate"`
}

// System is the machine and the server process now.
type System struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
	Hostname  string `json:"hostname,omitempty"`
	PID       int    `json:"pid"`
	Started   int64  `json:"started"` // milliseconds
	// Scope says whether Box Office's CPU and memory include the
	// programs it runs (ScopeGroup) or not (ScopeProcess).
	Scope    string    `json:"scope"`
	Cores    int       `json:"cores"`
	CPULimit float64   `json:"cpuLimit,omitempty"` // a container's, in cores
	CPU      float64   `json:"cpu"`
	HostCPU  *float64  `json:"hostCpu,omitempty"`
	Load     []float64 `json:"load,omitempty"`
	// Memory: the machine's total and available (0 when unknown), what
	// Box Office uses, and a container's limit.
	MemTotal     uint64 `json:"memTotal,omitempty"`
	MemAvailable uint64 `json:"memAvailable,omitempty"`
	Memory       uint64 `json:"memory"`
	MemLimit     uint64 `json:"memLimit,omitempty"`
	GoHeap       uint64 `json:"goHeap"`
	Goroutines   int    `json:"goroutines"`
	// Since the server started.
	BytesOut     int64   `json:"bytesOut"`
	Requests     int64   `json:"requests"`
	ServerErrors int64   `json:"serverErrors"`
	Warnings     int64   `json:"warnings"`
	Errors       int64   `json:"errors"`
	History      []Point `json:"history"`
}

// Monitor takes a reading every Interval while it runs.
type Monitor struct {
	Logs    *Logs
	Traffic Traffic
	// Downloads says how many downloads are running and how fast they
	// fetch together, in bytes per second; nil counts none.
	Downloads func() (active int, rate float64)
	// Previous is what the run before this one logged, and its crash; nil
	// when there's no journal.
	Previous *PreviousRun

	started                   time.Time
	mu                        sync.Mutex
	last                      reading
	lastAt                    time.Time
	lastOut, lastReq, lastErr int64
	lastProblems              int64
	now                       Point
	history                   []Point
}

// NewMonitor makes a monitor that keeps logs.
func NewMonitor(logs *Logs) *Monitor {
	return &Monitor{Logs: logs, started: time.Now()}
}

// Run takes readings until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	m.sample()
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample()
		}
	}
}

func (m *Monitor) problems() int64 {
	if m.Logs == nil {
		return 0
	}
	w, e := m.Logs.Counts()
	return w + e
}

func (m *Monitor) sample() {
	r := read()
	at := time.Now()
	out, req, errs := m.Traffic.bytesOut.Load(), m.Traffic.requests.Load(), m.Traffic.serverErrors.Load()
	problems := m.problems()
	var active int
	var rate float64
	if m.Downloads != nil {
		active, rate = m.Downloads()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := Point{Time: at.UnixMilli(), Memory: r.procMem, Downloads: active, DownloadRate: rate}
	first := m.lastAt.IsZero()
	if !first {
		dt := at.Sub(m.lastAt).Seconds()
		if dt > 0 {
			if r.procOK && m.last.procOK && r.procCPU >= m.last.procCPU {
				p.CPU = clampPercent((r.procCPU - m.last.procCPU) / dt / cores(r) * 100)
			}
			p.BytesOut = float64(out-m.lastOut) / dt
			p.Requests = float64(req-m.lastReq) / dt
		}
		if r.hostOK && m.last.hostOK && r.hostTotal > m.last.hostTotal {
			v := clampPercent((r.hostBusy - m.last.hostBusy) / (r.hostTotal - m.last.hostTotal) * 100)
			p.HostCPU = &v
		}
		p.ServerErrors = errs - m.lastErr
		p.Problems = problems - m.lastProblems
	}
	m.last, m.lastAt = r, at
	m.lastOut, m.lastReq, m.lastErr, m.lastProblems = out, req, errs, problems
	m.now = p
	if first {
		return // no rates yet
	}
	m.history = append(m.history, p)
	if len(m.history) > HistoryLength {
		m.history = append(m.history[:0], m.history[len(m.history)-HistoryLength:]...)
	}
}

// cores is how many processors Box Office may use.
func cores(r reading) float64 {
	n := float64(runtime.NumCPU())
	if r.cpuLimit > 0 && r.cpuLimit < n {
		return r.cpuLimit
	}
	return n
}

func clampPercent(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return float64(int(v*10+0.5)) / 10
}

// System describes the machine and the server now.
func (m *Monitor) System() System {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	host, _ := os.Hostname()
	s := System{
		OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Hostname: host, PID: os.Getpid(),
		Started: m.started.UnixMilli(), Cores: runtime.NumCPU(), GoHeap: ms.HeapAlloc, Goroutines: runtime.NumGoroutine(),
		BytesOut: m.Traffic.bytesOut.Load(), Requests: m.Traffic.requests.Load(), ServerErrors: m.Traffic.serverErrors.Load(),
	}
	if m.Logs != nil {
		s.Warnings, s.Errors = m.Logs.Counts()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.last
	if m.lastAt.IsZero() {
		r = read()
	}
	s.Scope, s.CPULimit, s.Load = r.scope, r.cpuLimit, r.load
	s.MemTotal, s.MemAvailable, s.Memory, s.MemLimit = r.memTotal, r.memAvailable, r.procMem, r.memLimit
	s.CPU, s.HostCPU = m.now.CPU, m.now.HostCPU
	s.History = append([]Point{}, m.history...)
	return s
}

// DiskUsage is the space on the disk path is on.
func DiskUsage(path string) (Disk, bool) { return diskUsage(path) }

func sortProblems(ps []Problem) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].seq > ps[j].seq })
}
