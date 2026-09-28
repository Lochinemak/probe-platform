//go:build windows

package probe

func prepareTCPProbeSocket(uintptr, bool, int) (uint16, error) {
	return 0, errUnsupported
}
