package server

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

// TestResolversJSON checks the shipped directory for the mistakes a hand
// edit makes. It does not touch the network.
func TestResolversJSON(t *testing.T) {
	if len(resolvers) < 15 {
		t.Fatalf("only %d resolvers", len(resolvers))
	}
	ids, addrs := map[string]bool{}, map[string]string{}
	quick := 0
	for _, r := range resolvers {
		if r.ID == "" || r.Name == "" || r.Org == "" || !isCountryCode(r.CC) {
			t.Errorf("%q: id, name, org and cc are required: %+v", r.ID, r)
		}
		if ids[r.ID] {
			t.Errorf("duplicate id %q", r.ID)
		}
		ids[r.ID] = true
		switch r.Scope {
		case "global", "national":
		case "province":
			if _, ok := findProvince(r.Province); !ok || !validLine(r.Line) {
				t.Errorf("%s: a provincial resolver needs a known province and line, got %q / %q", r.ID, r.Province, r.Line)
			}
		default:
			t.Errorf("%s: scope %q", r.ID, r.Scope)
		}
		if r.Province != "" {
			if p, ok := findProvince(r.Province); !ok || p.short != r.Province {
				t.Errorf("%s: province %q is not a short province name", r.ID, r.Province)
			}
		}
		switch r.Tier {
		case "quick":
			quick++
		case "full", "dir":
		default:
			t.Errorf("%s: tier %q", r.ID, r.Tier)
		}
		if len(r.IPv4) == 0 {
			t.Errorf("%s: no IPv4 address", r.ID)
		}
		for _, a := range r.IPv4 {
			ip := net.ParseIP(a)
			if ip == nil || ip.To4() == nil || strings.TrimSpace(a) != a {
				t.Errorf("%s: %q is not an IPv4 address", r.ID, a)
			}
		}
		for _, a := range r.IPv6 {
			ip := net.ParseIP(a)
			if ip == nil || ip.To4() != nil {
				t.Errorf("%s: %q is not an IPv6 address", r.ID, a)
			}
		}
		for _, a := range append(append([]string{}, r.IPv4...), r.IPv6...) {
			if ip := net.ParseIP(a); ip != nil && !ip.IsGlobalUnicast() {
				t.Errorf("%s: %s is not a public address", r.ID, a)
			}
			if other, dup := addrs[a]; dup {
				t.Errorf("%s is listed under both %s and %s", a, other, r.ID)
			}
			addrs[a] = r.ID
		}
		if r.DoH != "" {
			if u, err := url.Parse(r.DoH); err != nil || u.Scheme != "https" || u.Host == "" {
				t.Errorf("%s: DoH %q", r.ID, r.DoH)
			}
		}
		if strings.ContainsAny(r.DoT, "/: ") {
			t.Errorf("%s: DoT is a host name, got %q", r.ID, r.DoT)
		}
	}
	if quick < 6 {
		t.Errorf("the quick tier has %d resolvers", quick)
	}
}
