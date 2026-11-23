package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogtoRedirectURL(t *testing.T) {
	req := func(host, proto string) *http.Request {
		r := httptest.NewRequest("GET", "/api/auth/logto/login", nil)
		r.Host = host
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}
	const cb = "/api/auth/logto/callback"
	for _, c := range []struct {
		name, base string
		r          *http.Request
		want       string
	}{
		{"no request", "https://probe.example.com/", nil, "https://probe.example.com" + cb},
		// The site address wins on its own host, whatever the proxy says about the scheme.
		{"site address", "https://probe.example.com", req("probe.example.com", ""), "https://probe.example.com" + cb},
		{"site address, case", "https://probe.example.com", req("Probe.Example.com", "https"), "https://probe.example.com" + cb},
		{"second domain", "https://probe.example.com", req("mirror.example.net", "https"), "https://mirror.example.net" + cb},
		{"second domain, plain http", "https://probe.example.com", req("10.0.0.5:8080", ""), "http://10.0.0.5:8080" + cb},
		{"other port is another site", "https://probe.example.com", req("probe.example.com:8443", "https"), "https://probe.example.com:8443" + cb},
		{"path prefix kept", "https://probe.example.com/probe", req("mirror.example.net", "https"), "https://mirror.example.net/probe" + cb},
		{"no Host", "https://probe.example.com", req("", "https"), "https://probe.example.com" + cb},
		{"Host that is not a host", "https://probe.example.com", req("evil.example/x?y", "https"), "https://probe.example.com" + cb},
	} {
		if got := logtoRedirectURL(c.base, c.r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// fakeOIDC is just enough of an OpenID provider for the code flow: discovery,
// keys and a token endpoint that signs an ID token for whatever code it gets.
type fakeOIDC struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu        sync.Mutex
	redirects []string // redirect_uri of every token request
}

func newFakeOIDC(t *testing.T, clientID, sub string) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDC{key: key}
	b64 := base64.RawURLEncoding.EncodeToString
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oidc/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss := f.srv.URL + "/oidc"
		writeJSON(w, 200, map[string]any{
			"issuer": iss, "authorization_endpoint": iss + "/auth", "token_endpoint": iss + "/token", "jwks_uri": iss + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /oidc/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /oidc/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.redirects = append(f.redirects, r.PostForm.Get("redirect_uri"))
		f.mu.Unlock()
		head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		now := time.Now()
		body, _ := json.Marshal(map[string]any{
			"iss": f.srv.URL + "/oidc", "sub": sub, "aud": clientID, "name": "Tester",
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		})
		signed := b64(head) + "." + b64(body)
		sum := sha256.Sum256([]byte(signed))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": signed + "." + b64(sig)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// TestLogtoLoginReturnsToTheDomainItStartedOn: with a second domain in front
// of the same server, a login started there must come back there. The state
// cookie is set on that domain; a callback on the site address cannot see it.
func TestLogtoLoginReturnsToTheDomainItStartedOn(t *testing.T) {
	idp := newFakeOIDC(t, "app1", "u1")
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := Config{
		AdminPassword: "pw", AdminUser: "admin", TaskTimeout: time.Minute,
		BaseURL: "https://probe.example.com", LogtoEndpoint: idp.srv.URL, LogtoAppID: "app1", LogtoAppSecret: "s", LogtoAdmins: "u1",
	}
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, mustSettings(t, st, cfg), nil, nil, discardLogger()))
	defer srv.Close()

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// do sends a request the way the reverse proxy would for a visitor of host.
	do := func(host, pathAndQuery string, cookies []*http.Cookie) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+pathAndQuery, nil)
		req.Host = host
		req.Header.Set("X-Forwarded-Proto", "https")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := noFollow.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	for _, c := range []struct{ host, callback string }{
		{"mirror.example.net", "https://mirror.example.net/api/auth/logto/callback"},
		{"probe.example.com", "https://probe.example.com/api/auth/logto/callback"},
	} {
		resp := do(c.host, "/api/auth/logto/login", nil)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("%s: login: status %d", c.host, resp.StatusCode)
		}
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || !strings.HasPrefix(loc.String(), idp.srv.URL+"/oidc/auth?") {
			t.Fatalf("%s: login must go to the provider, got %q", c.host, resp.Header.Get("Location"))
		}
		if got := loc.Query().Get("redirect_uri"); got != c.callback {
			t.Fatalf("%s: redirect_uri = %q, want %q", c.host, got, c.callback)
		}
		// Without prompt=login the provider reuses its SSO session and a
		// signed-out admin can never switch to another Logto account.
		if got := loc.Query().Get("prompt"); got != "login" {
			t.Fatalf("%s: prompt = %q, want \"login\"", c.host, got)
		}

		// The provider sends the browser back to that callback, on the same host.
		resp = do(c.host, "/api/auth/logto/callback?code=abc&state="+loc.Query().Get("state"), resp.Cookies())
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
			t.Fatalf("%s: callback: status %d, Location %q", c.host, resp.StatusCode, resp.Header.Get("Location"))
		}
		var session *http.Cookie
		for _, ck := range resp.Cookies() {
			if ck.Name == sessionCookie && ck.Value != "" {
				session = ck
			}
		}
		if session == nil {
			t.Fatalf("%s: no session cookie after the callback", c.host)
		}
		// The token request must name the callback the login started with:
		// the provider refuses the code otherwise.
		idp.mu.Lock()
		last := idp.redirects[len(idp.redirects)-1]
		idp.mu.Unlock()
		if last != c.callback {
			t.Fatalf("%s: token request redirect_uri = %q, want %q", c.host, last, c.callback)
		}
	}

	// The settings page shows the callback to register for the domain it is opened on.
	login, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"pw"}`))
	if err != nil || login.StatusCode != 200 {
		t.Fatalf("password login: %v %v", err, login)
	}
	login.Body.Close()
	view := func(host string) SettingsView {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/api/settings", nil)
		req.Host = host
		req.Header.Set("X-Forwarded-Proto", "https")
		for _, c := range login.Cookies() {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v SettingsView
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := view("mirror.example.net"); v.LogtoRedirectURL != "https://probe.example.com/api/auth/logto/callback" || v.LogtoRedirectHere != "https://mirror.example.net/api/auth/logto/callback" {
		t.Fatalf("settings on the second domain: %q / %q", v.LogtoRedirectURL, v.LogtoRedirectHere)
	}
	if v := view("probe.example.com"); v.LogtoRedirectHere != "" {
		t.Fatalf("settings on the site address must not list a second callback: %q", v.LogtoRedirectHere)
	}
}
