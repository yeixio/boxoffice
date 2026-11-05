package monitor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func filetime(ft windows.Filetime) float64 {
	return float64(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) / 1e7 // 100ns units
}

func read() reading {
	r := reading{scope: ScopeProcess}
	var idle, kernel, user windows.Filetime
	if ok, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		// Kernel time includes idle time.
		r.hostTotal = filetime(kernel) + filetime(user)
		r.hostBusy = r.hostTotal - filetime(idle)
		r.hostOK = true
	}
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); ok != 0 {
		r.memTotal, r.memAvailable = ms.TotalPhys, ms.AvailPhys
	}
	self := windows.CurrentProcess()
	var created, exited, kt, ut windows.Filetime
	if windows.GetProcessTimes(self, &created, &exited, &kt, &ut) == nil {
		r.procCPU, r.procOK = filetime(kt)+filetime(ut), true
	}
	var pmc processMemoryCounters
	pmc.Cb = uint32(unsafe.Sizeof(pmc))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(self), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb)); ok != 0 {
		r.procMem = uint64(pmc.WorkingSetSize)
	}
	return r
}

func diskUsage(path string) (Disk, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Disk{}, false
	}
	var avail, total, free uint64
	if windows.GetDiskFreeSpaceEx(p, &avail, &total, &free) != nil {
		return Disk{}, false
	}
	return Disk{Total: total, Free: avail}, true
}
