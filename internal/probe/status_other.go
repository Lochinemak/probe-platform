//go:build !linux && !darwin && !windows

package probe

import "probe-platform/internal/protocol"

// platformStatus has nothing to add on this system: the portable fields
// Status fills in are all there is.
func platformStatus(*protocol.StatusResult) {}
