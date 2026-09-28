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

	roleAdmin = "admin"
	roleGuest = "guest"
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

	mu       sync.Mutex
	attempts map[string][]time.Time
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

// allowAttempt rate-limits login attempts to 10 per minute per IP.
func (s *sessionAuth) allowAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
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
