//go:build windows

package agent

import "os"

// execSelf cannot replace a running process on Windows; exit and let the
// service manager restart the (already swapped) executable.
func execSelf(string) error {
	os.Exit(0)
	return nil
}
