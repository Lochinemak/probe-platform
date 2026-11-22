package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"

	"probe-platform/internal/protocol"
)

// TLS performs a TLS handshake with target ("host", "host:port" or an https
// URL; port 443 by default) and reports the negotiated parameters and the
// certificate chain the server presented.
//
// The handshake itself does not verify the certificate, so an expired or
// self-signed chain is still returned in full; verification is then done
// separately and reported in Verified / VerifyError. That is the difference
// to the HTTP probe, which stops at a bad certificate.
//
// progress receives "resolved" and one "attempt" (protocol.TLSAttempt) per
// handshake.
func TLS(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.TLSResult, error) {
	count := clamp(def(p.Count, 1), 1, 10)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	timeout := time.Duration(clamp(def(p.TimeoutMs, 10000), 500, 30000)) * time.Millisecond

	host, port, err := SplitTarget(target, def(p.Port, 443))
	if err != nil {
		return nil, err
	}
	ip, err := ResolveTarget(ctx, host, p)
	if err != nil {
		return nil, err
	}
	progress.emit("resolved", ip.String())
	res := &protocol.TLSResult{Target: target, ServerName: host, IP: ip.String(), Port: port, CertOK: true}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // verified below, so that a bad chain can still be shown
		NextProtos:         []string{"h2", "http/1.1"},
	}
	if net.ParseIP(host) == nil {
		cfg.ServerName = host // no SNI for an address, as browsers do
	}

	var rtts []float64
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
		a, state := tlsOnce(ctx, addr, cfg, timeout, p)
		a.Seq = i
		res.Attempts = append(res.Attempts, a)
		if a.OK {
			rtts = append(rtts, a.HandshakeMs)
			fillTLSState(res, state, host, p)
		}
		progress.emit("attempt", a)
		if ctx.Err() != nil {
			break
		}
	}
	res.Stats = ComputeStats(len(res.Attempts), rtts)
	return res, nil
}

func tlsOnce(ctx context.Context, addr string, cfg *tls.Config, timeout time.Duration, p protocol.Params) (protocol.TLSAttempt, *tls.ConnectionState) {
	var a protocol.TLSAttempt
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := net.Dialer{Control: publicAddrControl(p.PublicOnly)}
	start := time.Now()
	raw, err := d.DialContext(actx, dialNetwork("tcp", p.IPVersion), addr)
	if err != nil {
		a.Error = ErrString(err)
		return a, nil
	}
	defer raw.Close()
	a.ConnectMs = ms(time.Since(start))
	conn := tls.Client(raw, cfg)
	hs := time.Now()
	if err := conn.HandshakeContext(actx); err != nil {
		a.Error = ErrString(err)
		return a, nil
	}
	a.HandshakeMs = ms(time.Since(hs))
	a.OK = true
	state := conn.ConnectionState()
	return a, &state
}

func fillTLSState(res *protocol.TLSResult, state *tls.ConnectionState, host string, p protocol.Params) {
	res.Version = tls.VersionName(state.Version)
	res.Cipher = tls.CipherSuiteName(state.CipherSuite)
	res.ALPN = state.NegotiatedProtocol
	res.OCSPStapled = len(state.OCSPResponse) > 0
	res.Chain = res.Chain[:0]
	for _, c := range state.PeerCertificates {
		res.Chain = append(res.Chain, tlsCert(c))
	}
	if len(state.PeerCertificates) == 0 {
		res.Verified, res.VerifyError = false, "server sent no certificate"
		return
	}
	leaf := state.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter})
	res.Verified = err == nil
	res.VerifyError = ""
	if err != nil {
		res.VerifyError = err.Error()
	}
	res.DaysLeft = int(math.Floor(time.Until(leaf.NotAfter).Hours() / 24))
	res.CertOK = p.ExpectCertDays <= 0 || res.DaysLeft >= p.ExpectCertDays
}

func tlsCert(c *x509.Certificate) protocol.TLSCert {
	out := protocol.TLSCert{
		Subject: c.Subject.String(), Issuer: c.Issuer.String(), NotBefore: c.NotBefore, NotAfter: c.NotAfter,
		Serial: c.SerialNumber.Text(16), SigAlg: c.SignatureAlgorithm.String(), IsCA: c.IsCA,
	}
	out.SANs = append(out.SANs, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		out.SANs = append(out.SANs, ip.String())
	}
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		out.KeyType = fmt.Sprintf("RSA %d", k.N.BitLen())
	case *ecdsa.PublicKey:
		out.KeyType = "ECDSA " + k.Curve.Params().Name
	case ed25519.PublicKey:
		out.KeyType = "Ed25519"
	}
	return out
}
