//go:build !windows

package agent

// restrictToOwner is a no-op: the file is created 0600.
func restrictToOwner(string) error { return nil }
