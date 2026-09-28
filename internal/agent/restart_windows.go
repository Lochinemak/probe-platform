//go:build windows

package agent

import "os"

// execSelf cannot replace the running image on Windows. The new binary is
// already in place, so exit and let the service control manager's recovery
// action (configured by `probe-agent service install`) start it. Run from a
// console instead of as a service, the process just ends and must be started
// again by hand.
func execSelf(string) error {
	os.Exit(0)
	return nil
}
