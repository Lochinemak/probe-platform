//go:build !noquic

package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	"probe-platform/internal/protocol"
)

// h3Server serves HTTP/3 on a loopback UDP port with a self-signed
// certificate and returns the port. sni receives the server name of every
// handshake.
func h3Server(t *testing.T, sni chan<- string) int {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "h3.test"}, DNSNames: []string{"h3.test"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	conf := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			select {
			case sni <- h.ServerName:
			default:
			}
			return nil, nil
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proto", r.Proto)
		fmt.Fprint(w, "hi over quic")
	})
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/hello", http.StatusFound) })
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback UDP: %v", err)
	}
	srv := &http3.Server{Handler: mux, TLSConfig: http3.ConfigureTLSConfig(conf)}
	go func() { _ = srv.Serve(pc) }()
	t.Cleanup(func() { _ = srv.Close(); _ = pc.Close() })
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func TestHTTP3Loopback(t *testing.T) {
	sni := make(chan string, 8)
	port := h3Server(t, sni)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := fmt.Sprintf("https://127.0.0.1:%d", port)

	res, err := HTTP(ctx, base+"/hello", protocol.Params{HTTP3: true, InsecureTLS: true, ExpectKeyword: "over quic", TimeoutMs: 5000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Attempts[0]
	if !a.OK || !a.AssertOK || a.StatusCode != 200 || a.Proto != "HTTP/3.0" || a.IP != "127.0.0.1" || a.Headers["X-Proto"] != "HTTP/3.0" {
		t.Fatalf("attempt: %+v", a)
	}
	// One QUIC handshake stands in for TCP connect and TLS; a literal
	// address needs no lookup.
	if a.Timing.TLSMs <= 0 || a.Timing.ConnectMs != 0 || a.Timing.DNSMs != 0 || a.Timing.TotalMs < a.Timing.TLSMs {
		t.Fatalf("timing: %+v", a.Timing)
	}
	if a.TLSVersion != "TLS 1.3" || a.CertSubject != "h3.test" || a.CertNotAfter == nil {
		t.Fatalf("tls: %q %q %v", a.TLSVersion, a.CertSubject, a.CertNotAfter)
	}

	// A redirect is followed over HTTP/3 too.
	res, err = HTTP(ctx, base+"/redir", protocol.Params{HTTP3: true, InsecureTLS: true, FollowRedirects: true, TimeoutMs: 5000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Attempts[0]; !a.OK || len(a.Redirects) != 1 || !strings.HasSuffix(a.FinalURL, "/hello") || a.Proto != "HTTP/3.0" {
		t.Fatalf("redirect: %+v", a)
	}

	// The certificate is checked unless told otherwise.
	res, err = HTTP(ctx, base+"/hello", protocol.Params{HTTP3: true, TimeoutMs: 5000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Attempts[0]; a.OK || a.Error == "" {
		t.Fatalf("a self-signed certificate was accepted: %+v", a)
	}

	// connect_to: the URL's host is not looked up, and stays the server name.
	for len(sni) > 0 {
		<-sni
	}
	res, err = HTTP(ctx, fmt.Sprintf("https://h3.test:%d/hello", port), protocol.Params{HTTP3: true, InsecureTLS: true, ConnectTo: "127.0.0.1", TimeoutMs: 5000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Attempts[0]; !a.OK || a.IP != "127.0.0.1" || a.StatusCode != 200 {
		t.Fatalf("connect_to: %+v", a)
	}
	if got := <-sni; got != "h3.test" {
		t.Fatalf("server name %q", got)
	}
}

func TestHTTP3Refusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := HTTP(ctx, "http://127.0.0.1/", protocol.Params{HTTP3: true}, nil); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http over HTTP/3: %v", err)
	}
	// A target without a scheme means https.
	port := h3Server(t, make(chan string, 4))
	res, err := HTTP(ctx, fmt.Sprintf("127.0.0.1:%d/hello", port), protocol.Params{HTTP3: true, InsecureTLS: true, TimeoutMs: 5000}, nil)
	if err != nil || !strings.HasPrefix(res.URL, "https://") || !res.Attempts[0].OK {
		t.Fatalf("no scheme: %v %+v", err, res)
	}
	// Guests stay on the public internet over QUIC as well.
	if _, err := HTTP(ctx, fmt.Sprintf("https://127.0.0.1:%d/hello", port), protocol.Params{HTTP3: true, InsecureTLS: true, PublicOnly: true}, nil); err == nil {
		t.Fatal("a loopback target was accepted with public_only")
	}
	found := false
	for _, c := range DetectCapabilities() {
		found = found || c == protocol.CapHTTP3
	}
	if !found {
		t.Fatal("http3 capability not announced")
	}
}
