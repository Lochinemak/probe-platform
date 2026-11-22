package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

// guestLookupTimeout bounds the name lookup done while validating a target.
const guestLookupTimeout = 3 * time.Second

// lookupIP is the resolver used to validate a target. Overridden in tests.
var lookupIP = net.DefaultResolver.LookupIP

// validateGuestTarget rejects an anonymous visitor's task when its destination
// is not on the public internet. The agent enforces the same policy on the
// address it actually connects to (Params.PublicOnly), which is what stops a
// name resolving differently there; this check is the first line and also
// covers agents running a build older than that policy.
func validateGuestTarget(ctx context.Context, typ protocol.TaskType, target string, p protocol.Params) error {
	target = strings.TrimSpace(target)
	// Where the node is told to connect instead, and whom it is told to ask,
	// are destinations as much as the target is.
	if p.ConnectTo != "" {
		if err := checkPublicHost(ctx, p.ConnectTo); err != nil {
			return err
		}
	}
	if p.Resolver != "" && !strings.EqualFold(p.Resolver, "system") {
		if err := checkGuestDNSServer(ctx, p.Resolver); err != nil {
			return err
		}
	}
	switch typ {
	case protocol.TaskPing, protocol.TaskMTR:
		return checkPublicHost(ctx, target)
	case protocol.TaskTCPing:
		host, _, err := probe.SplitTarget(target, def(p.Port, 80))
		if err != nil {
			return err
		}
		return checkPublicHost(ctx, host)
	case protocol.TaskHTTP:
		raw := target
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return fmt.Errorf("invalid url %q", target)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
		return checkPublicHost(ctx, u.Hostname())
	case protocol.TaskDNS:
		// The queried name is not a destination; the resolver is. An empty spec
		// means the node's own resolver, which the visitor does not choose.
		return checkGuestDNSServer(ctx, p.DNSServer)
	case protocol.TaskUDP:
		host, _, err := probe.SplitTarget(target, p.Port)
		if err != nil {
			return errors.New("UDP 探测目标必须是 host:port")
		}
		return checkPublicHost(ctx, host)
	case protocol.TaskTLS:
		host, _, err := probe.SplitTarget(target, def(p.Port, 443))
		if err != nil {
			return err
		}
		return checkPublicHost(ctx, host)
	case protocol.TaskDNSCompare:
		if len(p.DNSServers) > guestCompareServers {
			return fmt.Errorf("游客最多对比 %d 个解析器", guestCompareServers)
		}
		for _, s := range p.DNSServers {
			if strings.EqualFold(strings.TrimSpace(s), "system") {
				continue
			}
			if err := checkGuestDNSServer(ctx, s); err != nil {
				return err
			}
		}
		return nil
	}
	// A type this function has not been taught about is refused, not waved
	// through.
	return fmt.Errorf("游客不能发起 %s 类型的探测", typ)
}

// guestCompareServers is how many resolvers a guest may compare at once.
const guestCompareServers = 6

// checkGuestDNSServer limits a guest-supplied resolver to a public address on
// the standard DNS port, or a public DNS-over-HTTPS endpoint. Without the port
// restriction the DNS probe would be a general-purpose port prober.
func checkGuestDNSServer(ctx context.Context, spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	if strings.HasPrefix(spec, "https://") {
		u, err := url.Parse(spec)
		if err != nil || u.Hostname() == "" {
			return errors.New("DoH 地址无效")
		}
		if port := u.Port(); port != "" && port != "443" {
			return errors.New("游客的 DoH 服务器只能使用 443 端口")
		}
		return checkPublicHost(ctx, u.Hostname())
	}
	if strings.HasPrefix(spec, "http://") {
		return errors.New("DoH 必须使用 https")
	}
	hostport := strings.TrimPrefix(strings.TrimPrefix(spec, "udp://"), "tcp://")
	host := hostport
	if h, port, err := net.SplitHostPort(hostport); err == nil {
		host = h
		if n, err := strconv.Atoi(port); err != nil || n != 53 {
			return errors.New("游客指定的 DNS 服务器只能使用 53 端口")
		}
	}
	return checkPublicHost(ctx, host)
}

// checkPublicHost resolves host if needed and refuses anything that is not
// globally routable. Every answer has to be public, so a name with one private
// address among several is rejected rather than raced.
func checkPublicHost(ctx context.Context, host string) error {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return errors.New("target is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !probe.IsPublicIP(ip) {
			return probe.PrivateTargetError(host, ip)
		}
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, guestLookupTimeout)
	defer cancel()
	ips, err := lookupIP(rctx, "ip", host)
	if err != nil {
		// Our own lookup failing says nothing about the name: it may resolve
		// only from the node. The agent applies the same policy to the address
		// it connects to, so let the probe run and report the real error.
		return nil
	}
	for _, ip := range ips {
		// A fake-IP answer means this server's own DNS is hijacked by a
		// transparent proxy, which is a different problem from someone aiming
		// at an internal address; say so instead of blaming the target.
		if probe.IsFakeIP(ip) {
			return probe.FakeIPError(host, ip)
		}
		if !probe.IsPublicIP(ip) {
			return probe.PrivateTargetError(host, ip)
		}
	}
	return nil
}

// def returns v unless it is zero or negative, in which case fallback is used.
func def(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
