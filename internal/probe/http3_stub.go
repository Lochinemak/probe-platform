//go:build noquic

package probe

import (
	"net/http"
	"time"

	"probe-platform/internal/protocol"
)

// HasHTTP3 is false in builds made with -tags noquic: QUIC adds a few
// megabytes, too much for the routers these builds are for.
const HasHTTP3 = false

func newH3RoundTripper(protocol.Params, time.Duration, *phaseClock, string) (http.RoundTripper, func(), error) {
	return nil, nil, errNoHTTP3
}
