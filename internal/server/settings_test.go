package server

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsAPI(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := Config{AdminPassword: "envpass", AdminUser: "admin", AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true, AgentImage: "img:latest"}
	settings := mustSettings(t, st, cfg)
	hub := NewHub(cfg, st, nil, nil, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, settings, nil, nil, discardLogger()))
	defer srv.Close()

	if resp, _ := http.Get(srv.URL + "/api/settings"); resp.StatusCode != 401 {
		t.Fatalf("guest settings: %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp, _ := c.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"envpass"}`)); resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var view SettingsView
	resp, _ := c.Get(srv.URL + "/api/settings")
	_ = json.NewDecoder(resp.Body).Decode(&view)
	if !view.PasswordSet || view.PasswordSource != "env" || view.AgentImage != "img:latest" || view.LogtoEnabled {
		t.Fatalf("initial view: %+v", view)
	}

	// Save Logto + base URL from the dashboard: takes effect immediately.
	req, _ := http.NewRequest("PUT", srv.URL+"/api/settings", strings.NewReader(`{"base_url":"https://probe.example.com/","logto_endpoint":"https://auth.example.com/","logto_app_id":"app1","logto_app_secret":"s3cret","guest_access":false}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = c.Do(req)
	_ = json.NewDecoder(resp.Body).Decode(&view)
	if resp.StatusCode != 200 || !view.LogtoEnabled || !view.LogtoSecretSet || view.LogtoRedirectURL != "https://probe.example.com/api/auth/logto/callback" || view.GuestAccess {
		t.Fatalf("after update: %d %+v", resp.StatusCode, view)
	}
	if b, _ := json.Marshal(view); strings.Contains(string(b), "s3cret") {
		t.Fatal("secret must never be returned")
	}
	// Guest access is now off: anonymous requests are refused.
	if resp, _ := http.Get(srv.URL + "/api/agents"); resp.StatusCode != 401 {
		t.Fatalf("guest after disable: %d", resp.StatusCode)
	}
	var sess map[string]any
	resp, _ = http.Get(srv.URL + "/api/session")
	_ = json.NewDecoder(resp.Body).Decode(&sess)
	if sess["login"].(map[string]any)["logto"] != true {
		t.Fatalf("session must advertise logto: %v", sess)
	}
	// Bad values are rejected.
	req, _ = http.NewRequest("PUT", srv.URL+"/api/settings", strings.NewReader(`{"base_url":"not a url"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, _ := c.Do(req); resp.StatusCode != 400 {
		t.Fatalf("bad base_url: %d", resp.StatusCode)
	}

	// Change password: needs the current one, then logs the caller out.
	if resp, _ := c.Post(srv.URL+"/api/settings/password", "application/json", strings.NewReader(`{"current":"wrong","new":"longenough"}`)); resp.StatusCode != 401 {
		t.Fatalf("wrong current: %d", resp.StatusCode)
	}
	if resp, _ := c.Post(srv.URL+"/api/settings/password", "application/json", strings.NewReader(`{"current":"envpass","new":"short"}`)); resp.StatusCode != 400 {
		t.Fatalf("short new: %d", resp.StatusCode)
	}
	if resp, _ := c.Post(srv.URL+"/api/settings/password", "application/json", strings.NewReader(`{"current":"envpass","new":"longenough"}`)); resp.StatusCode != 200 {
		t.Fatalf("change: %d", resp.StatusCode)
	}
	if resp, _ := c.Get(srv.URL + "/api/settings"); resp.StatusCode != 401 {
		t.Fatalf("session must be gone after password change: %d", resp.StatusCode)
	}
	if resp, _ := c.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"envpass"}`)); resp.StatusCode != 401 {
		t.Fatal("env password must stop working")
	}
	if resp, _ := c.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"longenough"}`)); resp.StatusCode != 200 {
		t.Fatal("new password must work")
	}

	// Open mode: the request that enables the first login method keeps its
	// author signed in as admin instead of locking them out.
	st4, _ := OpenStore(filepath.Join(t.TempDir(), "open.db"))
	defer st4.Close()
	openCfg := Config{AgentToken: "tok", TaskTimeout: time.Minute, GuestAccess: true}
	openSrv := httptest.NewServer(NewHandler(openCfg, hub, st4, emptyFS{}, mustSettings(t, st4, openCfg), nil, nil, discardLogger()))
	defer openSrv.Close()
	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2}
	req, _ = http.NewRequest("PUT", openSrv.URL+"/api/settings", strings.NewReader(`{"base_url":"https://p.example.com","logto_endpoint":"https://auth.example.com","logto_app_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, _ := c2.Do(req); resp.StatusCode != 200 {
		t.Fatalf("open-mode update: %d", resp.StatusCode)
	}
	if resp, _ := c2.Get(openSrv.URL + "/api/settings"); resp.StatusCode != 200 {
		t.Fatalf("author must stay admin after enabling login: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(openSrv.URL + "/api/settings"); resp.StatusCode != 401 {
		t.Fatalf("everyone else is now a guest: %d", resp.StatusCode)
	}

	// Settings survive a reload from the store.
	again := mustSettings(t, st, Config{})
	if v := again.View(); v.LogtoAppID != "app1" || !v.LogtoSecretSet || v.PasswordSource != "dashboard" || v.GuestAccess {
		t.Fatalf("reloaded: %+v", v)
	}
	if again.SessionSecret() != settings.SessionSecret() {
		t.Fatal("session secret must persist")
	}
}
