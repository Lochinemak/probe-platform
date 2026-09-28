// Package buildinfo holds build-time metadata injected via -ldflags.
package buildinfo

// Version is set with: -ldflags "-X probe-platform/internal/buildinfo.Version=v1.2.3"
var Version = "dev"
