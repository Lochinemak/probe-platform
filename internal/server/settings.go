package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Settings are the runtime-editable options (admin page). Environment
// variables seed them on first start; anything saved from the dashboard is
// stored in the settings table and wins from then on, no restart needed.
type Settings struct {
	store *Store
	log   *slog.Logger

	mu       sync.RWMutex
	v        settingsValues
	logto    *Logto
	logtoKey string
}

type settingsValues struct {
	GuestAccess    bool
	AdminUser      string
	PasswordHash   string // bcrypt, set from the dashboard
	EnvPassword    string // PROBE_ADMIN_PASSWORD, used only until a hash exists
	LogtoEndpoint  string
	LogtoAppID     string
	LogtoAppSecret string
	LogtoAdmins    string
	BaseURL        string
	AgentImage     string
	SessionSecret  string
	SessionEpoch   int
	// LegacyToken is the old shared agent token (PROBE_AGENT_TOKEN or
	// data/agent_token), never stored in the database. LegacyEnabled says
	// whether it is still accepted while nodes migrate to their own tokens.
	LegacyToken   string
	LegacyEnabled bool
}

// SettingsView is what the dashboard sees; secrets are reported as booleans.
type SettingsView struct {
	GuestAccess      bool   `json:"guest_access"`
	AdminUser        string `json:"admin_user"`
	PasswordSet      bool   `json:"password_set"`
	PasswordSource   string `json:"password_source"` // dashboard, env, ""
	LogtoEndpoint    string `json:"logto_endpoint"`
	LogtoAppID       string `json:"logto_app_id"`
	LogtoSecretSet   bool   `json:"logto_secret_set"`
	LogtoAdmins      string `json:"logto_admins"`
	LogtoRedirectURL string `json:"logto_redirect_url"`
	LogtoEnabled     bool   `json:"logto_enabled"`
	BaseURL          string `json:"base_url"`
	AgentImage       string `json:"agent_image"`
	LoginEnabled     bool   `json:"login_enabled"`
	LegacyAgentToken string `json:"legacy_agent_token"` // none, enabled, disabled
}

// SettingsPatch is a partial update; nil fields are left unchanged. An empty
// string clears a value.
type SettingsPatch struct {
	GuestAccess    *bool   `json:"guest_access"`
	AdminUser      *string `json:"admin_user"`
	BaseURL        *string `json:"base_url"`
	AgentImage     *string `json:"agent_image"`
	LogtoEndpoint  *string `json:"logto_endpoint"`
	LogtoAppID     *string `json:"logto_app_id"`
	LogtoAppSecret *string `json:"logto_app_secret"`
	LogtoAdmins    *string `json:"logto_admins"`
	// LegacyAgentToken switches the old shared agent token on or off.
	LegacyAgentToken *bool `json:"legacy_agent_token"`
}

// LoadSettings merges env defaults with what the dashboard saved.
func LoadSettings(store *Store, env Config, log *slog.Logger) (*Settings, error) {
	v := settingsValues{
		GuestAccess:    env.GuestAccess,
		AdminUser:      env.AdminUser,
		EnvPassword:    env.AdminPassword,
		LogtoEndpoint:  env.LogtoEndpoint,
		LogtoAppID:     env.LogtoAppID,
		LogtoAppSecret: env.LogtoAppSecret,
		LogtoAdmins:    env.LogtoAdmins,
		BaseURL:        env.BaseURL,
		AgentImage:     env.AgentImage,
		LegacyToken:    strings.TrimSpace(env.AgentToken),
		LegacyEnabled:  true,
	}
	if v.AdminUser == "" {
		v.AdminUser = "admin"
	}
	db, err := store.GetSettings()
	if err != nil {
		return nil, err
	}
	if s, ok := db["guest_access"]; ok {
		v.GuestAccess = s == "true"
	}
	if s, ok := db["admin_user"]; ok && s != "" {
		v.AdminUser = s
	}
	if s, ok := db["admin_password_hash"]; ok {
		v.PasswordHash = s
	}
	for key, dst := range map[string]*string{
		"logto_endpoint": &v.LogtoEndpoint, "logto_app_id": &v.LogtoAppID, "logto_app_secret": &v.LogtoAppSecret,
		"logto_admins": &v.LogtoAdmins, "base_url": &v.BaseURL, "agent_image": &v.AgentImage, "session_secret": &v.SessionSecret,
	} {
		if s, ok := db[key]; ok {
			*dst = s
		}
	}
	if s, ok := db["legacy_agent_token_enabled"]; ok {
		v.LegacyEnabled = s == "true"
	}
	if s, ok := db["session_epoch"]; ok {
		v.SessionEpoch, _ = strconv.Atoi(s)
	}
	if v.SessionSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		v.SessionSecret = hex.EncodeToString(b)
		if err := store.SetSettings(map[string]string{"session_secret": v.SessionSecret}); err != nil {
			return nil, err
		}
	}
	return &Settings{store: store, log: log, v: v}, nil
}

func (s *Settings) snapshot() settingsValues {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v
}

// View renders the settings for the dashboard.
func (s *Settings) View() SettingsView {
	v := s.snapshot()
	view := SettingsView{
		GuestAccess: v.GuestAccess, AdminUser: v.AdminUser,
		PasswordSet:   v.PasswordHash != "" || v.EnvPassword != "",
		LogtoEndpoint: v.LogtoEndpoint, LogtoAppID: v.LogtoAppID, LogtoSecretSet: v.LogtoAppSecret != "", LogtoAdmins: v.LogtoAdmins,
		BaseURL: v.BaseURL, AgentImage: v.AgentImage,
		LegacyAgentToken: s.LegacyTokenState(),
	}
	switch {
	case v.PasswordHash != "":
		view.PasswordSource = "dashboard"
	case v.EnvPassword != "":
		view.PasswordSource = "env"
	}
	if v.BaseURL != "" {
		view.LogtoRedirectURL = strings.TrimSuffix(v.BaseURL, "/") + "/api/auth/logto/callback"
	}
	view.LogtoEnabled = s.Logto() != nil
	view.LoginEnabled = view.PasswordSet || view.LogtoEnabled
	return view
}

// --- values used by other components ---------------------------------------

func (s *Settings) GuestAccess() bool  { return s.snapshot().GuestAccess }
func (s *Settings) AdminUser() string  { return s.snapshot().AdminUser }
func (s *Settings) BaseURL() string    { return strings.TrimSuffix(s.snapshot().BaseURL, "/") }
func (s *Settings) AgentImage() string { return s.snapshot().AgentImage }
func (s *Settings) SessionSecret() string {
	return s.snapshot().SessionSecret
}
func (s *Settings) SessionEpoch() int { return s.snapshot().SessionEpoch }

// LegacyAgentToken returns the old shared agent token while it is accepted,
// "" when there is none or it has been switched off.
func (s *Settings) LegacyAgentToken() string {
	v := s.snapshot()
	if !v.LegacyEnabled {
		return ""
	}
	return v.LegacyToken
}

// LegacyTokenState is "none" (no shared token configured), "enabled" or
// "disabled".
func (s *Settings) LegacyTokenState() string {
	v := s.snapshot()
	switch {
	case v.LegacyToken == "":
		return "none"
	case v.LegacyEnabled:
		return "enabled"
	}
	return "disabled"
}

// PasswordLoginEnabled reports whether a password (dashboard or env) exists.
func (s *Settings) PasswordLoginEnabled() bool {
	v := s.snapshot()
	return v.PasswordHash != "" || v.EnvPassword != ""
}

// LoginEnabled reports whether any admin login method is configured.
func (s *Settings) LoginEnabled() bool { return s.PasswordLoginEnabled() || s.Logto() != nil }

// CheckPassword verifies a username/password pair.
func (s *Settings) CheckPassword(user, pass string) bool {
	v := s.snapshot()
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(user)), []byte(v.AdminUser)) == 1
	switch {
	case v.PasswordHash != "":
		return bcrypt.CompareHashAndPassword([]byte(v.PasswordHash), []byte(pass)) == nil && userOK
	case v.EnvPassword != "":
		return subtle.ConstantTimeCompare([]byte(pass), []byte(v.EnvPassword)) == 1 && userOK
	}
	return false
}

// SetPassword stores a bcrypt hash and bumps the session epoch so every
// existing session (including the caller's) has to log in again.
func (s *Settings) SetPassword(newPass string) error {
	if len(newPass) < 8 {
		return errors.New("密码至少 8 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	epoch := s.v.SessionEpoch + 1
	if err := s.store.SetSettings(map[string]string{"admin_password_hash": string(hash), "session_epoch": strconv.Itoa(epoch)}); err != nil {
		return err
	}
	s.v.PasswordHash = string(hash)
	s.v.SessionEpoch = epoch
	return nil
}

// Update applies a patch, validates and persists it.
func (s *Settings) Update(p SettingsPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.v
	kv := map[string]string{}
	if p.GuestAccess != nil {
		v.GuestAccess = *p.GuestAccess
		kv["guest_access"] = strconv.FormatBool(v.GuestAccess)
	}
	if p.AdminUser != nil {
		u := strings.TrimSpace(*p.AdminUser)
		if u == "" || len(u) > 64 {
			return errors.New("用户名不能为空且不超过 64 字符")
		}
		v.AdminUser = u
		kv["admin_user"] = u
	}
	if p.BaseURL != nil {
		b := strings.TrimSpace(*p.BaseURL)
		if b != "" {
			u, err := url.Parse(b)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return errors.New("站点地址必须是 http(s):// 开头的完整地址")
			}
			b = strings.TrimSuffix(u.Scheme+"://"+u.Host+u.Path, "/")
		}
		v.BaseURL = b
		kv["base_url"] = b
	}
	if p.AgentImage != nil {
		img := strings.TrimSpace(*p.AgentImage)
		if img == "" {
			return errors.New("agent 镜像名不能为空")
		}
		v.AgentImage = img
		kv["agent_image"] = img
	}
	if p.LogtoEndpoint != nil {
		e := strings.TrimSuffix(strings.TrimSpace(*p.LogtoEndpoint), "/")
		if e != "" {
			u, err := url.Parse(e)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return errors.New("Logto 地址必须是 http(s):// 开头")
			}
		}
		v.LogtoEndpoint = e
		kv["logto_endpoint"] = e
	}
	if p.LogtoAppID != nil {
		v.LogtoAppID = strings.TrimSpace(*p.LogtoAppID)
		kv["logto_app_id"] = v.LogtoAppID
	}
	if p.LogtoAppSecret != nil {
		v.LogtoAppSecret = strings.TrimSpace(*p.LogtoAppSecret)
		kv["logto_app_secret"] = v.LogtoAppSecret
	}
	if p.LogtoAdmins != nil {
		v.LogtoAdmins = strings.TrimSpace(*p.LogtoAdmins)
		kv["logto_admins"] = v.LogtoAdmins
	}
	if p.LegacyAgentToken != nil {
		if v.LegacyToken == "" && *p.LegacyAgentToken {
			return errors.New("没有配置旧版共享 token（PROBE_AGENT_TOKEN / data/agent_token），无需启用")
		}
		v.LegacyEnabled = *p.LegacyAgentToken
		kv["legacy_agent_token_enabled"] = strconv.FormatBool(v.LegacyEnabled)
	}
	if len(kv) == 0 {
		return nil
	}
	if err := s.store.SetSettings(kv); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	s.v = v
	return nil
}

// Logto returns the OIDC client for the current settings, rebuilding it when
// any relevant value changed. nil when not configured.
func (s *Settings) Logto() *Logto {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.v
	key := strings.Join([]string{v.LogtoEndpoint, v.LogtoAppID, v.LogtoAppSecret, v.LogtoAdmins, v.BaseURL}, "\x00")
	if key != s.logtoKey {
		s.logto = NewLogto(Config{LogtoEndpoint: v.LogtoEndpoint, LogtoAppID: v.LogtoAppID, LogtoAppSecret: v.LogtoAppSecret, LogtoAdmins: v.LogtoAdmins, BaseURL: v.BaseURL}, s.log)
		s.logtoKey = key
	}
	return s.logto
}
