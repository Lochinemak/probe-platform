package probe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"probe-platform/internal/protocol"
)

// def returns v unless it is zero, in which case it returns fallback.
func def(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000
}

// Resolve turns host into a single IP honouring the requested IP version
// ("", "4" or "6"). Plain IP literals are validated against the version.
func Resolve(ctx context.Context, host, ipVersion string) (net.IP, error) {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil, errors.New("empty target")
	}
	if ip := net.ParseIP(host); ip != nil {
		is4 := ip.To4() != nil
		if ipVersion == "4" && !is4 {
			return nil, fmt.Errorf("%s is not an IPv4 address", host)
		}
		if ipVersion == "6" && is4 {
			return nil, fmt.Errorf("%s is not an IPv6 address", host)
		}
		return ip, nil
	}
	network := "ip"
	switch ipVersion {
	case "4":
		network = "ip4"
	case "6":
		network = "ip6"
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(rctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, shortErr(err))
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no address", host)
	}
	if ipVersion == "" {
		// Prefer IPv4 when the caller has no preference; most home networks
		// still have better v4 connectivity.
		for _, ip := range ips {
			if ip.To4() != nil {
				return ip, nil
			}
		}
	}
	return ips[0], nil
}

// SplitTarget separates "host:port" (or "[v6]:port") into host and port. If
// no port is present, fallback is used. A port of 0 after all of that is an
// error.
func SplitTarget(target string, fallback int) (string, int, error) {
	target = strings.TrimSpace(target)
	// Strip a scheme if the user pasted a URL.
	if i := strings.Index(target, "://"); i >= 0 {
		if u, err := url.Parse(target); err == nil && u.Host != "" {
			target = u.Host
		}
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		// No port, or a bare IPv6 literal.
		host = strings.Trim(target, "[]")
		if fallback <= 0 {
			return "", 0, errors.New("no port specified")
		}
		return host, fallback, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

// ComputeStats aggregates RTT samples (ms) against the number of probes sent.
func ComputeStats(sent int, rtts []float64) protocol.Stats {
	s := protocol.Stats{Sent: sent, Received: len(rtts)}
	if sent > 0 {
		s.LossPct = math.Round(float64(sent-len(rtts))/float64(sent)*10000) / 100
	}
	if len(rtts) == 0 {
		return s
	}
	s.MinMs, s.MaxMs = rtts[0], rtts[0]
	var sum float64
	for _, r := range rtts {
		sum += r
		if r < s.MinMs {
			s.MinMs = r
		}
		if r > s.MaxMs {
			s.MaxMs = r
		}
	}
	s.AvgMs = sum / float64(len(rtts))
	var sq float64
	for _, r := range rtts {
		d := r - s.AvgMs
		sq += d * d
	}
	s.StdDevMs = math.Sqrt(sq / float64(len(rtts)))
	s.AvgMs = round3(s.AvgMs)
	s.StdDevMs = round3(s.StdDevMs)
	return s
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// shortErr strips the noisy "dial tcp 1.2.3.4:80:" style prefixes Go adds so
// the dashboard shows just the cause.
func shortErr(err error) error {
	if err == nil {
		return nil
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	var operr *net.OpError
	if errors.As(err, &operr) && operr.Err != nil {
		err = operr.Err
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return errors.New("no such host")
		}
		if dnsErr.IsTimeout {
			return errors.New("dns timeout")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return errors.New("timeout")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return err
}

// ErrString is shortErr as a string, "" for nil.
func ErrString(err error) string {
	if err == nil {
		return ""
	}
	return shortErr(err).Error()
}

// sleepCtx sleeps for d or until ctx is done, returning ctx.Err() in that case.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
