package server

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

// Resolver is one entry of the public DNS directory: a resolver people can
// point a device or a probe at.
type Resolver struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Org  string `json:"org"`
	CC   string `json:"cc"`
	// Scope: "global" (anycast, meant for everybody), "national" (a Chinese
	// public resolver) or "province" (a carrier's resolver for its own
	// customers in one province).
	Scope    string   `json:"scope"`
	Province string   `json:"province,omitempty"`
	Line     string   `json:"line,omitempty"`
	IPv4     []string `json:"ipv4"`
	IPv6     []string `json:"ipv6"`
	DoH      string   `json:"doh,omitempty"`
	DoT      string   `json:"dot,omitempty"`
	// Tier says how far a resolver is used beyond the directory: "quick" and
	// "full" are the sets the propagation check asks; "dir" is listed only
	// (carrier resolvers mostly answer their own customers).
	Tier string `json:"tier"`
	Note string `json:"note,omitempty"`
}

// The directory is curated by hand in data/resolvers.json and shipped inside
// the binary. Addresses in it were checked by querying them from the nodes
// (see README); one that cannot be confirmed does not go in.
//
//go:embed data/resolvers.json
var resolversJSON []byte

var resolvers = func() []Resolver {
	var out []Resolver
	if err := json.Unmarshal(resolversJSON, &out); err != nil {
		panic("data/resolvers.json: " + err.Error())
	}
	return out
}()

func (a *API) listResolvers(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"resolvers": resolvers})
}
