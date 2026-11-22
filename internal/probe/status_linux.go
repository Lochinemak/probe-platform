//go:build linux

package probe

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"

	"probe-platform/internal/protocol"
)

func platformStatus(res *protocol.StatusResult) {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		for _, f := range firstN(strings.Fields(string(b)), 3) {
			if v, err := strconv.ParseFloat(f, 64); err == nil {
				res.Load = append(res.Load, v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				res.UptimeSec = int64(v)
			}
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		var free, buffers, cached uint64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, rest, ok := strings.Cut(sc.Text(), ":")
			fields := strings.Fields(rest)
			if !ok || len(fields) == 0 {
				continue
			}
			kb, _ := strconv.ParseUint(fields[0], 10, 64)
			switch k {
			case "MemTotal":
				res.MemTotal = kb << 10
			case "MemAvailable":
				res.MemAvailable = kb << 10
			case "MemFree":
				free = kb << 10
			case "Buffers":
				buffers = kb << 10
			case "Cached":
				cached = kb << 10
			}
		}
		f.Close()
		if res.MemAvailable == 0 { // kernels before 3.14 (old routers) have no MemAvailable
			res.MemAvailable = free + buffers + cached
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		res.Kernel = strings.TrimSpace(string(b))
	}
	res.Platform = osRelease()
	var st syscall.Statfs_t
	if syscall.Statfs(res.DiskPath, &st) == nil {
		res.DiskTotal = st.Blocks * uint64(st.Bsize)
		res.DiskFree = st.Bavail * uint64(st.Bsize)
	}
}

// osRelease is the distribution's name for itself: PRETTY_NAME from
// /etc/os-release, which OpenWrt, Debian, the Synology and QNAP systems all
// provide.
func osRelease() string {
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	return ""
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
