package probe

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"probe-platform/internal/protocol"
)

const maxCompareServers = 8

// knownResolvers names the public resolvers people usually compare.
var knownResolvers = map[string]string{
	"223.5.5.5": "阿里 DNS", "223.6.6.6": "阿里 DNS", "119.29.29.29": "腾讯 DNSPod", "180.76.76.76": "百度 DNS",
	"114.114.114.114": "114 DNS", "180.184.1.1": "火山引擎 DNS", "1.2.4.8": "CNNIC",
	"1.1.1.1": "Cloudflare", "1.0.0.1": "Cloudflare", "8.8.8.8": "Google", "8.8.4.4": "Google",
	"9.9.9.9": "Quad9", "208.67.222.222": "OpenDNS", "208.67.220.220": "OpenDNS",
}

// DNSCompare asks several resolvers the same question from this node, at the
// same time, and says whether they agree. "system" stands for the node's own
// resolver. Resolvers that fail are listed with their error and left out of
// the comparison.
//
// Different answers are not necessarily a fault: a CDN hands different
// resolvers different addresses on purpose. What the verdict flags is that
// the name does not resolve the same everywhere, which is the thing to know
// when chasing hijacking, stale caches or a half-finished DNS change.
//
// progress receives one "entry" (protocol.DNSCompareEntry) per resolver, in
// the order they finish.
func DNSCompare(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.DNSCompareResult, error) {
	timeout := time.Duration(clamp(def(p.TimeoutMs, 3000), 500, 10000)) * time.Millisecond
	name := strings.TrimSuffix(strings.TrimSpace(target), ".")
	if name == "" || strings.ContainsAny(name, " /\\") {
		return nil, errors.New("invalid name")
	}
	rtype, rname, err := dnsType(p.RecordType)
	if err != nil {
		return nil, err
	}
	var servers []string
	seen := map[string]bool{}
	for _, s := range p.DNSServers {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		servers = append(servers, s)
	}
	if len(servers) == 0 {
		servers = []string{"system"}
	}
	if len(servers) > maxCompareServers {
		return nil, errors.New("too many resolvers (max 8)")
	}

	res := &protocol.DNSCompareResult{Target: name, RecordType: rname, Entries: make([]protocol.DNSCompareEntry, len(servers))}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, spec := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := protocol.DNSCompareEntry{Server: spec, Attempt: protocol.DNSAttempt{Answers: []protocol.DNSAnswer{}}}
			system := strings.EqualFold(spec, "system")
			arg := spec
			if system {
				arg, e.Label = "", "系统 DNS"
			}
			server, proto, err := dnsServer(arg)
			if err == nil && p.PublicOnly && !system {
				err = checkPublicDNSServer(ctx, server, proto, p.IPVersion)
			}
			if err != nil {
				e.Attempt.Error = err.Error()
			} else {
				e.Attempt = dnsQuery(ctx, name+".", rtype, server, proto, timeout, p.IPVersion, p.PublicOnly)
				if host, _, ok := strings.Cut(server, ":"); ok && e.Label == "" {
					e.Label = knownResolvers[host]
				}
				e.Key = answerKey(e.Attempt, rname)
			}
			mu.Lock()
			res.Entries[i] = e
			progress.emit("entry", e)
			mu.Unlock()
		}()
	}
	wg.Wait()

	keys := map[string]bool{}
	for _, e := range res.Entries {
		if e.Attempt.OK {
			keys[e.Key] = true
		}
	}
	res.Groups = len(keys)
	switch {
	case len(keys) == 0:
		res.Verdict = "failed"
	case len(keys) == 1:
		res.Verdict = "consistent"
	default:
		res.Verdict = "inconsistent"
	}
	return res, nil
}

// answerKey is what two resolvers' answers are compared by: the values of the
// records of the asked type, sorted. TTLs and the CNAME chain on the way to
// them differ between resolvers without the answer being different.
func answerKey(a protocol.DNSAttempt, rtype string) string {
	var vals []string
	for _, ans := range a.Answers {
		if ans.Type == rtype {
			vals = append(vals, strings.ToLower(ans.Value))
		}
	}
	sort.Strings(vals)
	return strings.Join(vals, ",")
}
