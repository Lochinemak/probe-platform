package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "probe_session"

// sessionAuth issues and validates stateless HMAC cookies. The key derives
// from the admin password so sessions survive restarts; changing the password
// invalidates every session.
type sessionAuth struct {
	enabled  bool
	password string
	key      []byte
	ttl      time.Duration

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newSessionAuth(password string) *sessionAuth {
	sum := sha256.Sum256([]byte("probe-platform/session/" + password))
	return &sessionAuth{
		enabled:  password != "",
		password: password,
		key:      sum[:],
		ttl:      30 * 24 * time.Hour,
		attempts: map[string][]time.Time{},
	}
}

func (s *sessionAuth) sign(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *sessionAuth) issue() (string, time.Time) {
	exp := time.Now().Add(s.ttl)
	payload := strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.sign(payload), exp
}

func (s *sessionAuth) valid(tok string) bool {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(payload))) == 1
}

func (s *sessionAuth) checkPassword(p string) bool {
	return subtle.ConstantTimeCompare([]byte(p), []byte(s.password)) == 1
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

func (s *sessionAuth) authenticated(r *http.Request) bool {
	if !s.enabled {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return s.valid(c.Value)
}

func (s *sessionAuth) setCookie(w http.ResponseWriter, r *http.Request) {
	tok, exp := s.issue()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
}

func (s *sessionAuth) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}
