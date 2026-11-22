//go:build noquic

package probe

import (
	"context"
	"errors"
	"testing"

	"probe-platform/internal/protocol"
)

func TestHTTP3Stub(t *testing.T) {
	if _, err := HTTP(context.Background(), "https://example.com/", protocol.Params{HTTP3: true}, nil); !errors.Is(err, errNoHTTP3) {
		t.Fatalf("got %v", err)
	}
	for _, c := range DetectCapabilities() {
		if c == protocol.CapHTTP3 {
			t.Fatal("a build without QUIC announced http3")
		}
	}
}
