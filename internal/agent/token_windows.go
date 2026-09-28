//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

// restrictToOwner limits path to SYSTEM and Administrators, like the
// installer does for probe-agent.env: C:\ProgramData lets every local user
// read what is created under it.
func restrictToOwner(path string) error {
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls %s: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
