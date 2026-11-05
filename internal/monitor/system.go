package monitor

// reading is one look at the machine. CPU times only mean something as the
// difference between two readings.
type reading struct {
	// hostBusy and hostTotal are the whole machine's CPU time, busy and in
	// all, in any unit; hostOK says they were read.
	hostBusy, hostTotal float64
	hostOK              bool
	// procCPU is the CPU time, in seconds, Box Office has used: with the
	// programs it runs when scope is ScopeGroup.
	procCPU float64
	procOK  bool
	// memTotal and memAvailable are the machine's memory; 0 when unknown.
	memTotal, memAvailable uint64
	// procMem is the memory Box Office uses (with its child processes for ScopeGroup).
	procMem uint64
	// memLimit and cpuLimit are a container's limits; 0 for none.
	memLimit uint64
	cpuLimit float64
	load     []float64 // 1, 5 and 15 minute load averages, when the OS has them
	scope    string
}

const (
	// ScopeGroup means Box Office's numbers include the programs it runs.
	ScopeGroup = "group"
	// ScopeProcess means they're for the server process alone.
	ScopeProcess = "process"
)

// Disk is the space on the disk a folder is on.
type Disk struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}
