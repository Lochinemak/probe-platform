// Package buildinfo holds build-time metadata injected via -ldflags.
package buildinfo

// Version is set with: -ldflags "-X probe-platform/internal/buildinfo.Version=v1.2.3"
var Version = "dev"

// Variant is the CPU variant for GOARCH=arm builds ("v6", "v7"), set with
// -X probe-platform/internal/buildinfo.Variant=v7. Empty for other targets.
var Variant = ""
