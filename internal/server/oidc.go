package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Logto is an OpenID Connect client for a Logto tenant (or any OIDC
// provider): authorization code flow with PKCE, server-side token exchange,
// ID token verification against the provider's JWKS.
type Logto struct {
	issuer      string
	clientID    string
	secret      string
	baseURL     string
	redirectURL string          // the callback on the configured site address
	admins      map[string]bool // lower-cased sub / email / username; empty = every user
	log         *slog.Logger

	mu       sync.Mutex
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// Identity is what we learn about the signed-in user.
type Identity struct {
	Sub      string
	Name     string
	Username string
	Email    string
}

// NewLogto returns nil when Logto is not configured. Discovery happens
// lazily on the first login so a provider outage cannot block startup.
func NewLogto(cfg Config, log *slog.Logger) *Logto {
	if cfg.LogtoEndpoint == "" || cfg.LogtoAppID == "" {
		return nil
	}
	if cfg.BaseURL == "" {
		log.Warn("logto: PROBE_BASE_URL is empty; the redirect URI cannot be built, Logto login disabled")
		return nil
	}
	issuer := strings.TrimSuffix(cfg.LogtoEndpoint, "/")
	if !strings.HasSuffix(issuer, "/oidc") {
		issuer += "/oidc"
	}
	admins := map[string]bool{}
	for _, a := range strings.Split(cfg.LogtoAdmins, ",") {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			admins[a] = true
		}
	}
	l := &Logto{
		issuer:      issuer,
		clientID:    cfg.LogtoAppID,
		secret:      cfg.LogtoAppSecret,
		baseURL:     cfg.BaseURL,
		redirectURL: logtoRedirectURL(cfg.BaseURL, nil),
		admins:      admins,
		log:         log,
	}
	log.Info("logto login enabled", "issuer", issuer, "app_id", cfg.LogtoAppID, "redirect", l.redirectURL, "admin_allowlist", len(admins))
	if len(admins) == 0 {
		log.Warn("logto: no admin allowlist configured, every Logto login will be refused. List the accounts allowed to administer this dashboard on the settings page (Logto tenants usually accept self-registration).")
	}
	return l
}

// HasAdmins reports whether an allowlist is configured. Without one no Logto
// account can sign in.
func (l *Logto) HasAdmins() bool { return len(l.admins) > 0 }

// RedirectURL is what must be registered in the Logto application.
func (l *Logto) RedirectURL() string { return l.redirectURL }

// RedirectURLFor is the callback for a login that starts with r: on the site
// the visitor is actually using, so a second domain in front of the same
// server (a mirror) gets its visitors back.
func (l *Logto) RedirectURLFor(r *http.Request) string { return logtoRedirectURL(l.baseURL, r) }

const logtoCallbackPath = "/api/auth/logto/callback"

// logtoRedirectURL builds the callback address. A request that came in on the
// configured site address (or no request at all) gets exactly that address, so
// a proxy that does not pass X-Forwarded-Proto keeps working. A request for
// any other host gets its own origin: the state cookie of the login lives on
// that host, and a callback anywhere else could not read it.
//
// Taking the host from the request is safe because the provider only redirects
// to addresses registered in the application; an unregistered host ends on the
// provider's error page, not here.
func logtoRedirectURL(baseURL string, r *http.Request) string {
	base := strings.TrimSuffix(baseURL, "/")
	if r == nil || r.Host == "" {
		return base + logtoCallbackPath
	}
	prefix := ""
	if u, err := url.Parse(base); err == nil {
		if strings.EqualFold(u.Host, r.Host) {
			return base + logtoCallbackPath
		}
		prefix = strings.TrimSuffix(u.Path, "/")
	}
	origin := requestOrigin(r)
	if u, err := url.Parse(origin); err != nil || u.Host != r.Host || u.Path != "" {
		return base + logtoCallbackPath // a Host that does not survive as one
	}
	return origin + prefix + logtoCallbackPath
}

// requestOrigin is scheme://host as the visitor's browser has it. Behind a
// reverse proxy that needs the Host header passed through and
// X-Forwarded-Proto set, as the bundled proxy examples do.
func requestOrigin(r *http.Request) string {
	if secureCookie(r) {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func (l *Logto) init(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.provider != nil {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, l.issuer)
	if err != nil {
		return fmt.Errorf("oidc discovery %s: %w", l.issuer, err)
	}
	l.provider = p
	l.oauth = &oauth2.Config{
		ClientID:     l.clientID,
		ClientSecret: l.secret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  l.redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	l.verifier = p.Verifier(&oidc.Config{ClientID: l.clientID})
	return nil
}

// oauthFor is the OAuth client with the given callback; "" means the one on
// the configured site address.
func (l *Logto) oauthFor(redirect string) *oauth2.Config {
	c := *l.oauth
	if redirect != "" {
		c.RedirectURL = redirect
	}
	return &c
}

// LoginURL builds the authorization URL for a fresh state/verifier pair, with
// redirect (from RedirectURLFor) as the callback.
func (l *Logto) LoginURL(ctx context.Context, state, verifier, redirect string) (string, error) {
	if err := l.init(ctx); err != nil {
		return "", err
	}
	return l.oauthFor(redirect).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), nil
}

// Exchange turns the callback code into a verified identity. redirect must be
// the callback the login was started with: the provider compares the two.
func (l *Logto) Exchange(ctx context.Context, code, verifier, redirect string) (*Identity, error) {
	if err := l.init(ctx); err != nil {
		return nil, err
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tok, err := l.oauthFor(redirect).Exchange(tctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("no id_token in token response")
	}
	idt, err := l.verifier.Verify(tctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	if err := idt.Claims(&claims); err != nil {
		return nil, err
	}
	id := &Identity{Sub: idt.Subject, Name: claims.Name, Username: claims.Username, Email: claims.Email}
	// Logto only puts profile claims in the ID token when the scopes were granted;
	// fall back to the userinfo endpoint for a display name.
	if id.Name == "" && id.Username == "" && id.Email == "" {
		if ui, err := l.provider.UserInfo(tctx, oauth2.StaticTokenSource(tok)); err == nil {
			_ = ui.Claims(&claims)
			id.Name, id.Username, id.Email = claims.Name, claims.Username, claims.Email
			if id.Email == "" {
				id.Email = ui.Email
			}
		}
	}
	return id, nil
}

// DisplayName picks the friendliest label for the session.
func (id *Identity) DisplayName() string {
	for _, s := range []string{id.Name, id.Username, id.Email, id.Sub} {
		if s != "" {
			return s
		}
	}
	return "logto-user"
}

// IsAdmin applies the allowlist. An empty list grants nobody: the provider may
// well accept self-registration, in which case treating "no list" as "everyone"
// would hand admin to anyone who can create an account there.
func (l *Logto) IsAdmin(id *Identity) bool {
	if len(l.admins) == 0 {
		return false
	}
	for _, k := range []string{id.Sub, id.Email, id.Username} {
		if k != "" && l.admins[strings.ToLower(k)] {
			return true
		}
	}
	return false
}
