package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "probe_session"
	oidcCookie    = "probe_oidc"
	guestCookie   = "probe_guest"

	roleAdmin = "admin"
	roleGuest = "guest"

	ownerAdminPrefix = "admin:"
	ownerGuestPrefix = "guest:"
)

// Session is what the signed cookie carries.
type Session struct {
	Role  string `json:"r"`
	Name  string `json:"n"`
	Via   string `json:"v"` // password, logto
	Exp   int64  `json:"e"`
	Epoch int    `json:"p"` // bumped on password change to log everyone out
}

// sessionAuth issues and validates stateless HMAC cookies. The key derives
// from the configured secrets so sessions survive restarts; changing any of
// them invalidates every session.
//
// Modes:
//   - no admin login configured (no password, no Logto): everyone is admin
//     (open mode, for private networks);
//   - admin login configured: anonymous visitors are guests when GuestAccess
//     is on, otherwise everything needs a login.
type sessionAuth struct {
	settings *Settings
	ttl      time.Duration

	mu        sync.Mutex
	attempts  map[string][]time.Time
	lastSweep time.Time
}

func newSessionAuth(settings *Settings) *sessionAuth {
	return &sessionAuth{settings: settings, ttl: 30 * 24 * time.Hour, attempts: map[string][]time.Time{}}
}

func (s *sessionAuth) key() []byte {
	sum := sha256.Sum256([]byte("probe-platform/session/" + s.settings.SessionSecret()))
	return sum[:]
}

func (s *sessionAuth) sign(payload string) string {
	m := hmac.New(sha256.New, s.key())
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *sessionAuth) enabled() bool     { return s.settings.LoginEnabled() }
func (s *sessionAuth) guestAccess() bool { return s.settings.GuestAccess() }

// issue encodes and signs a session.
func (s *sessionAuth) issue(sess Session) (string, time.Time) {
	exp := time.Now().Add(s.ttl)
	sess.Exp = exp.Unix()
	sess.Epoch = s.settings.SessionEpoch()
	b, _ := json.Marshal(sess)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.sign(payload), exp
}

// parse validates a token and returns the session it carries.
func (s *sessionAuth) parse(tok string) (Session, bool) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(payload))) != 1 {
		return Session{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Session{}, false
	}
	var sess Session
	if json.Unmarshal(b, &sess) != nil || time.Now().Unix() > sess.Exp || sess.Role == "" || sess.Epoch != s.settings.SessionEpoch() {
		return Session{}, false
	}
	return sess, true
}

// sessionFrom returns the request's valid session, if any.
func (s *sessionAuth) sessionFrom(r *http.Request) *Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	sess, ok := s.parse(c.Value)
	if !ok {
		return nil
	}
	return &sess
}

// role resolves the effective role of a request: "admin", "guest" or ""
// (must log in).
func (s *sessionAuth) role(r *http.Request) string {
	if !s.enabled() {
		return roleAdmin // open mode
	}
	if sess := s.sessionFrom(r); sess != nil && sess.Role == roleAdmin {
		return roleAdmin
	}
	if s.guestAccess() {
		return roleGuest
	}
	return ""
}

func (s *sessionAuth) isAdmin(r *http.Request) bool { return s.role(r) == roleAdmin }

func (s *sessionAuth) passwordLoginEnabled() bool { return s.settings.PasswordLoginEnabled() }

func (s *sessionAuth) checkPassword(user, pass string) bool {
	return s.settings.CheckPassword(user, pass)
}

// maxTrackedIPs caps the rate-limiter table. Entries expire after a minute and
// are swept, so this is only a backstop against a flood of distinct addresses
// (IPv6 makes those free); hitting it resets the window rather than letting the
// map grow without bound.
const maxTrackedIPs = 20000

// allowAttempt rate-limits login attempts to 10 per minute per IP.
func (s *sessionAuth) allowAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.sweepLocked(now)
	recent := s.attempts[ip][:0]
	for _, t := range s.attempts[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		s.attempts[ip] = recent
		return false
	}
	s.attempts[ip] = append(recent, now)
	return true
}

// sweepLocked drops addresses whose window has passed. Without it the table
// only ever shrinks for addresses that come back, so it would grow for as long
// as the process runs.
func (s *sessionAuth) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < 30*time.Second && len(s.attempts) < maxTrackedIPs {
		return
	}
	s.lastSweep = now
	for ip, ts := range s.attempts {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= time.Minute {
			delete(s.attempts, ip)
		}
	}
	if len(s.attempts) >= maxTrackedIPs {
		s.attempts = map[string][]time.Time{}
	}
}

// --- task ownership --------------------------------------------------------

// owner returns the task-owner string for a request: "admin:<name>" for
// administrators (open mode counts as the configured admin user), "guest:<id>"
// for a visitor carrying a guest cookie, "" for a visitor without one.
func (s *sessionAuth) owner(r *http.Request) string {
	if s.role(r) == roleAdmin {
		name := s.settings.AdminUser()
		if sess := s.sessionFrom(r); sess != nil && sess.Name != "" {
			name = sess.Name
		}
		return ownerAdminPrefix + name
	}
	if id := guestIDFrom(r); id != "" {
		return ownerGuestPrefix + id
	}
	return ""
}

// guestIDFrom returns the anonymous id from the guest cookie, or "" when the
// cookie is missing or malformed. The id is 128 random bits, so it is not
// signed: knowing someone else's id is as hard as guessing their task ids.
func guestIDFrom(r *http.Request) string {
	c, err := r.Cookie(guestCookie)
	if err != nil || len(c.Value) != 32 {
		return ""
	}
	for _, ch := range c.Value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return c.Value
}

// ensureGuestOwner returns the visitor's guest owner string, minting the
// cookie when they do not have one yet and refreshing its expiry otherwise so
// a regular visitor keeps their history.
func (s *sessionAuth) ensureGuestOwner(w http.ResponseWriter, r *http.Request) string {
	id := guestIDFrom(r)
	if id == "" {
		id = randomHex(16)
	}
	http.SetCookie(w, &http.Cookie{
		Name: guestCookie, Value: id, Path: "/", MaxAge: int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r),
	})
	return ownerGuestPrefix + id
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *sessionAuth) setCookie(w http.ResponseWriter, r *http.Request, sess Session) {
	tok, exp := s.issue(sess)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r),
	})
}

func (s *sessionAuth) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

// --- OIDC login state (state + PKCE verifier in a short-lived signed cookie) ---

type oidcState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Exp      int64  `json:"e"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *sessionAuth) setOIDCCookie(w http.ResponseWriter, r *http.Request, st oidcState) {
	st.Exp = time.Now().Add(10 * time.Minute).Unix()
	b, _ := json.Marshal(st)
	payload := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: oidcCookie, Value: payload + "." + s.sign(payload), Path: "/api/auth/", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r),
	})
}

func (s *sessionAuth) takeOIDCCookie(w http.ResponseWriter, r *http.Request) (oidcState, bool) {
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/api/auth/", MaxAge: -1, HttpOnly: true})
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		return oidcState{}, false
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(payload))) != 1 {
		return oidcState{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return oidcState{}, false
	}
	var st oidcState
	if json.Unmarshal(b, &st) != nil || time.Now().Unix() > st.Exp {
		return oidcState{}, false
	}
	return st, true
}
