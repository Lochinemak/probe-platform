//go:build darwin

package probe

import (
	"encoding/binary"
	"time"

	"golang.org/x/sys/unix"

	"probe-platform/internal/protocol"
)

func platformStatus(res *protocol.StatusResult) {
	if v, err := unix.Sysctl("kern.osrelease"); err == nil {
		res.Kernel = v
	}
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		res.Platform = "macOS " + v
	}
	if v, err := unix.SysctlUint64("hw.memsize"); err == nil {
		res.MemTotal = v
	}
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }
	if b, err := unix.SysctlRaw("vm.loadavg"); err == nil && len(b) >= 24 {
		if scale := float64(binary.LittleEndian.Uint64(b[16:24])); scale > 0 {
			for i := 0; i < 3; i++ {
				res.Load = append(res.Load, float64(binary.LittleEndian.Uint32(b[i*4:]))/scale)
			}
		}
	}
	if tv, err := unix.SysctlTimeval("kern.boottime"); err == nil && tv.Sec > 0 {
		res.UptimeSec = time.Now().Unix() - tv.Sec
	}
	var st unix.Statfs_t
	if unix.Statfs(res.DiskPath, &st) == nil {
		res.DiskTotal = st.Blocks * uint64(st.Bsize)
		res.DiskFree = st.Bavail * uint64(st.Bsize)
	}
}
