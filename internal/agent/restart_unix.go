//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// execSelf replaces the current process with exe, keeping the PID (systemd
// keeps tracking it) and the ambient capabilities granted by the unit.
func execSelf(exe string) error {
	args := append([]string{exe}, os.Args[1:]...)
	return syscall.Exec(exe, args, os.Environ())
}
