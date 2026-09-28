package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAgentFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "probe-agent-linux-amd64", "a")
	writeFile(t, dir, "probe-agent-linux-armv7", "b")
	writeFile(t, dir, "probe-agent-windows-amd64.exe", "c")
	writeFile(t, dir, "SHA256SUMS", "ignored")
	writeFile(t, dir, "install-agent.sh", "#!/bin/sh")

	f := NewAgentFiles(dir, discardLogger())
	if f == nil {
		t.Fatal("expected files")
	}
	list := f.List()
	if len(list) != 3 || list[0].Key != "linux-amd64" || list[1].Key != "linux-armv7" || list[2].Key != "windows-amd64" {
		t.Fatalf("list: %+v", list)
	}
	sum := sha256.Sum256([]byte("a"))
	if got := f.Lookup("linux", "amd64", ""); got == nil || got.SHA256 != hex.EncodeToString(sum[:]) || got.Size != 1 {
		t.Fatalf("lookup amd64: %+v", got)
	}
	if got := f.Lookup("linux", "arm", "v7"); got == nil || got.Key != "linux-armv7" {
		t.Fatalf("lookup armv7: %+v", got)
	}
	if got := f.Lookup("linux", "arm", ""); got != nil {
		t.Fatal("arm without variant must not guess")
	}
	if got := f.Lookup("windows", "amd64", ""); got == nil || got.Name != "probe-agent-windows-amd64.exe" {
		t.Fatalf("lookup windows: %+v", got)
	}
	if got := f.Lookup("linux", "mips", ""); got != nil {
		t.Fatal("unknown platform must be nil")
	}
	// Cache invalidates when the file changes.
	time.Sleep(10 * time.Millisecond)
	writeFile(t, dir, "probe-agent-linux-amd64", "aa")
	if got := f.Lookup("linux", "amd64", ""); got == nil || got.Size != 2 {
		t.Fatalf("stale cache: %+v", got)
	}
	if NewAgentFiles(t.TempDir(), discardLogger()) != nil {
		t.Fatal("empty dir must disable")
	}
}

func TestAgentDownloadAPI(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	writeFile(t, dir, "probe-agent-linux-amd64", "binary-bytes")
	writeFile(t, dir, "install-agent.sh", "#!/bin/sh\necho hi\n")
	cfg := Config{AdminPassword: "pw", AgentToken: "tok", TaskTimeout: time.Minute, AgentsDir: dir}
	files := NewAgentFiles(dir, discardLogger())
	hub := NewHub(cfg, st, nil, files, discardLogger())
	srv := httptest.NewServer(NewHandler(cfg, hub, st, emptyFS{}, discardLogger()))
	defer srv.Close()

	get := func(path, token string) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := get("/api/agent/version", ""); r.StatusCode != 401 {
		t.Fatalf("no auth: %d", r.StatusCode)
	}
	if r := get("/api/agent/version", "wrong"); r.StatusCode != 401 {
		t.Fatalf("wrong token: %d", r.StatusCode)
	}
	r := get("/api/agent/version", "tok")
	var body struct {
		Version string
		Files   []AgentFile
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if r.StatusCode != 200 || len(body.Files) != 1 || body.Files[0].Key != "linux-amd64" {
		t.Fatalf("version: %d %+v", r.StatusCode, body)
	}
	r = get("/api/agent/download/linux-amd64", "tok")
	b, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256([]byte("binary-bytes"))
	if r.StatusCode != 200 || string(b) != "binary-bytes" || r.Header.Get("X-Checksum-Sha256") != hex.EncodeToString(sum[:]) {
		t.Fatalf("download: %d %q %s", r.StatusCode, b, r.Header.Get("X-Checksum-Sha256"))
	}
	if r := get("/api/agent/download/linux-arm64", "tok"); r.StatusCode != 404 {
		t.Fatalf("missing platform: %d", r.StatusCode)
	}
	if r := get("/api/agent/download/../etc", "tok"); r.StatusCode == 200 {
		t.Fatal("path traversal must not serve")
	}
	r = get("/install-agent.sh", "")
	b, _ = io.ReadAll(r.Body)
	if r.StatusCode != 200 || string(b) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("install script: %d %q", r.StatusCode, b)
	}
}
