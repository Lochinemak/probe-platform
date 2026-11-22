package probe

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestUnknownRData(t *testing.T) {
	for _, c := range []struct {
		name string
		typ  dnsmessage.Type
		data []byte
		want string
	}{
		{"CAA", typeCAA, append([]byte{0, 5}, "issueletsencrypt.org"...), `0 issue "letsencrypt.org"`},
		{"DS", typeDS, []byte{0x30, 0x39, 13, 2, 0xab, 0xcd}, "12345 13 2 ABCD"},
		{"DNSKEY", typeDNSKEY, []byte{1, 1, 3, 13, 'k', 'e', 'y'}, "257 3 13 a2V5"},
		{"TLSA", typeTLSA, []byte{3, 1, 1, 0xde, 0xad}, "3 1 1 dead"},
		{"HTTPS alias form", typeHTTPS, []byte{0, 0, 3, 'c', 'd', 'n', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}, "0 cdn.example"},
		{"HTTPS service form", typeHTTPS, []byte{0, 1, 0 /* alpn */, 0, 1, 0, 6, 2, 'h', '3', 2, 'h', '2' /* port */, 0, 3, 0, 2, 0x20, 0xfb /* ipv4hint */, 0, 4, 0, 4, 1, 2, 3, 4}, "1 . alpn=h3,h2 port=8443 ipv4hint=1.2.3.4"},
		{"a type nobody decodes", dnsmessage.Type(999), []byte{1, 2, 3}, `\# 3 010203`},
		{"truncated CAA", typeCAA, []byte{0, 9, 'x'}, `\# 3 000978`},
		{"SVCB with a parameter running past the end", typeSVCB, []byte{0, 1, 0, 0, 1, 0, 9, 'x'}, `\# 8 0001000001000978`},
	} {
		if got := unknownRData(c.typ, c.data); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for name, want := range extraTypes {
		if got, canon, err := dnsType(strings.ToLower(name)); err != nil || got != want || canon != name || typeName(got) != name {
			t.Errorf("%s: %v %q %v", name, got, canon, err)
		}
	}
}

func TestDNSExchange(t *testing.T) {
	server := fakeDNS(t, [4]byte{93, 184, 216, 34}, 300)
	msg, err := DNSExchange(context.Background(), DNSQuestion{Name: "example.com", Type: "A", Server: server, Recurse: true})
	if err != nil {
		t.Fatal(err)
	}
	if msg.RCode != "Success" || len(msg.Answers) != 1 || msg.Answers[0].Value != "93.184.216.34" || msg.Answers[0].Type != "A" || msg.RTTMs <= 0 {
		t.Fatalf("message: %+v", msg)
	}
	// The fake server knows no CAA: an error code, not an error.
	msg, err = DNSExchange(context.Background(), DNSQuestion{Name: "example.com.", Type: "CAA", Server: server})
	if err != nil || msg.RCode != "NameError" || len(msg.Answers) != 0 {
		t.Fatalf("CAA: %v %+v", err, msg)
	}
	if _, err := DNSExchange(context.Background(), DNSQuestion{Name: "example.com", Type: "BOGUS", Server: server}); err == nil {
		t.Error("unknown record type accepted")
	}
	if _, err := DNSExchange(context.Background(), DNSQuestion{Name: "example.com", Type: "A", Server: server, PublicOnly: true}); err == nil {
		t.Error("public-only reached a loopback server")
	}
	if _, err := DNSExchange(context.Background(), DNSQuestion{Name: "example.com", Type: "A", Server: "127.0.0.1:1", Timeout: 300_000_000}); err == nil {
		t.Error("a dead server answered")
	}
}
