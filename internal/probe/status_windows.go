//go:build windows

package probe

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"probe-platform/internal/protocol"
)

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func platformStatus(res *protocol.StatusResult) {
	v := windows.RtlGetVersion()
	res.Kernel = fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	res.Platform = "Windows " + res.Kernel
	res.UptimeSec = int64(windows.DurationSinceBoot().Seconds())

	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r != 0 {
		res.MemTotal, res.MemAvailable = m.totalPhys, m.availPhys
	}
	if path, err := windows.UTF16PtrFromString(res.DiskPath); err == nil {
		var avail, total, free uint64
		if windows.GetDiskFreeSpaceEx(path, &avail, &total, &free) == nil {
			res.DiskTotal, res.DiskFree = total, avail
		}
	}
}
