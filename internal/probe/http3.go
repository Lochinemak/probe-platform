//go:build !noquic

package probe

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"probe-platform/internal/protocol"
)

// HasHTTP3 says whether this build can speak HTTP/3. Builds made with
// -tags noquic (the small-flash MIPS targets) leave QUIC out.
const HasHTTP3 = true

// newH3RoundTripper is the HTTP/3 transport for one attempt. Every host of a
// redirect chain gets its own UDP socket and QUIC connection, resolved the
// same way the TCP path resolves (connect_to, resolver, the public-only
// policy), and the dial fills in the attempt's clock: DNS, then the QUIC
// handshake as the TLS phase. The returned function releases the sockets.
func newH3RoundTripper(p protocol.Params, timeout time.Duration, clock *phaseClock, origHost string) (http.RoundTripper, func(), error) {
	var mu sync.Mutex
	var closers []func()
	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: p.InsecureTLS, NextProtos: []string{http3.NextProtoH3}}, //nolint:gosec // operator-controlled toggle
		QUICConfig:      &quic.Config{HandshakeIdleTimeout: timeout, MaxIdleTimeout: timeout},
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			portN, err := strconv.Atoi(port)
			if err != nil {
				return nil, err
			}
			q := p
			if !strings.EqualFold(host, origHost) {
				q.ConnectTo = "" // a redirect to another host goes where that host is
			}
			clock.mu.Lock()
			clock.dnsStart = time.Now()
			clock.mu.Unlock()
			rctx, cancel := untraced(ctx)
			ip, err := ResolveTarget(rctx, host, q)
			cancel()
			clock.mu.Lock()
			if net.ParseIP(host) == nil || q.ConnectTo != "" {
				clock.dnsDone = time.Now()
			} else {
				clock.dnsStart = time.Time{} // a literal address: nothing was looked up
			}
			clock.mu.Unlock()
			if err != nil {
				return nil, err
			}
			network := "udp4"
			if ip.To4() == nil {
				network = "udp6"
			}
			sock, err := net.ListenUDP(network, nil)
			if err != nil {
				return nil, err
			}
			qt := &quic.Transport{Conn: sock}
			mu.Lock()
			closers = append(closers, func() { _ = qt.Close(); _ = sock.Close() })
			mu.Unlock()

			clock.mu.Lock()
			clock.tlsStart = time.Now()
			clock.ip = ip.String()
			clock.mu.Unlock()
			// Dial, not DialEarly: it returns once the handshake is done, so
			// the time it takes is the handshake.
			conn, err := qt.Dial(ctx, &net.UDPAddr{IP: ip, Port: portN}, tlsCfg, cfg)
			if err != nil {
				return nil, err
			}
			cs := conn.ConnectionState().TLS
			clock.mu.Lock()
			clock.tlsDone = time.Now()
			clock.tlsState = &cs
			clock.mu.Unlock()
			return conn, nil
		},
	}
	return tr, func() {
		_ = tr.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range closers {
			c()
		}
	}, nil
}
