package probe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"probe-platform/internal/buildinfo"
	"probe-platform/internal/protocol"
)

// Status reports the node's own state: what it runs on, how loaded it is and
// how much memory and disk it has left. It takes no target. The per-OS part
// is in status_<os>.go and fills in what that system can tell without cgo or
// external commands; fields it cannot fill stay empty.
func Status(_ context.Context) (*protocol.StatusResult, error) {
	res := &protocol.StatusResult{
		OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), AgentVersion: buildinfo.Version,
		CPUs: runtime.NumCPU(), Capabilities: DetectCapabilities(),
	}
	res.Hostname, _ = os.Hostname()
	res.Nameservers, _ = SystemNameservers()
	// Disk space is reported for where the agent lives: on a NAS that is the
	// volume that matters, and it is a path the agent may certainly look at.
	res.DiskPath = "/"
	if exe, err := os.Executable(); err == nil {
		res.DiskPath = filepath.Dir(exe)
	}
	platformStatus(res)
	return res, nil
}
