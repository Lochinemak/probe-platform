package server

import (
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
	loc, isp := g.LookupAgent("223.5.5.5")
	t.Logf("agent lookup -> loc=%q isp=%q", loc, isp)
	if loc == "" {
		t.Fatal("agent lookup via offline db failed")
	}
}
