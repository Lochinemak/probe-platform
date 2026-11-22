package server

import (
	"net"
	"os"
	"testing"

	"probe-platform/internal/protocol"
)

// TestGeoIPOffline needs a real ip2region database; set PROBE_TEST_XDB to run it.
func TestGeoIPOffline(t *testing.T) {
	path := os.Getenv("PROBE_TEST_XDB")
	if path == "" {
		t.Skip("PROBE_TEST_XDB not set")
	}
	g := NewGeoIP(Config{IP2RegionDB: path, DataDir: t.TempDir(), GeoIPOnline: false}, discardLogger())
	if g == nil || !g.HasOffline() {
		t.Fatal("database not loaded")
	}
	label := g.Offline("114.114.114.114")
	t.Logf("114.114.114.114 -> %q", label)
	if label == "" {
		t.Fatal("expected a label for a well-known Chinese IP")
	}
	if got := g.Offline("192.168.1.1"); got != "内网" {
		t.Fatalf("private ip: %q", got)
	}
	hops := []protocol.MTRHop{{TTL: 1, Hosts: []string{"192.168.1.1"}}, {TTL: 2, Hosts: []string{"223.5.5.5", "114.114.114.114"}}, {TTL: 3, Hosts: []string{}}}
	g.AnnotateHops(hops)
	if len(hops[1].Geo) != 2 || hops[1].Geo[0] == "" || hops[0].Geo[0] != "内网" || len(hops[2].Geo) != 0 {
		t.Fatalf("annotate: %+v", hops)
	}
	t.Logf("223.5.5.5 -> %q", hops[1].Geo[0])
	loc, isp, geo := g.LookupAgent("223.5.5.5")
	t.Logf("agent lookup -> loc=%q isp=%q geo=%+v", loc, isp, geo)
	if loc == "" {
		t.Fatal("agent lookup via offline db failed")
	}
	if geo.CC != "CN" || geo.Province == "" {
		t.Fatalf("structured lookup of a Chinese IP: %+v", geo)
	}
	// A few well-known addresses, to see what the database really returns.
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "119.29.29.29", "202.12.27.33", "168.95.1.1", "202.14.67.4"} {
		_, _, geo := g.LookupAgent(ip)
		t.Logf("%s -> %+v (raw %q)", ip, geo, g.offlineRaw(net.ParseIP(ip)))
	}
	if _, _, geo := g.LookupAgent("192.168.1.1"); geo != (protocol.Geo{}) {
		t.Fatalf("private address must have no place: %+v", geo)
	}
}
