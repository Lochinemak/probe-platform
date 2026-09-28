//go:build !windows

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"probe-platform/internal/protocol"
)

func TestSelfUpdate(t *testing.T) {
	newBinary := "#!/bin/sh\necho probe-agent v2\n"
	sum := sha256.Sum256([]byte(newBinary))
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/agent/download/linux-amd64" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, newBinary)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "probe-agent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho probe-agent v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	executablePath = func() (string, error) { return exe, nil }
	restarted := ""
	restartProcess = func(e string) error { restarted = e; return nil }
	updateJitter, drainTimeout = 0, 0
	t.Cleanup(func() { executablePath, restartProcess = os.Executable, execSelf })

	c, err := New(Config{Server: srv.URL + "/ws/agent", Token: "tok", Name: "t", Version: "v1", SelfUpdate: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpBase != srv.URL {
		t.Fatalf("httpBase %q", c.httpBase)
	}

	// Wrong checksum: nothing must change.
	bad := protocol.Update{Version: "v2", Path: "/api/agent/download/linux-amd64", SHA256: strings.Repeat("0", 64), Size: int64(len(newBinary))}
	if err := c.selfUpdate(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "v1") {
		t.Fatal("binary must be untouched after a failed update")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".probe-agent-update-*")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}

	// Version the new binary does not claim: reject.
	wrongVer := protocol.Update{Version: "v3", Path: bad.Path, SHA256: hex.EncodeToString(sum[:]), Size: bad.Size}
	if err := c.selfUpdate(context.Background(), wrongVer); err == nil || !strings.Contains(err.Error(), "expected version") {
		t.Fatalf("expected version check error, got %v", err)
	}

	// Good update.
	good := protocol.Update{Version: "v2", Path: bad.Path, SHA256: hex.EncodeToString(sum[:]), Size: bad.Size}
	if err := c.selfUpdate(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth header %q", gotAuth)
	}
	if b, _ := os.ReadFile(exe); string(b) != newBinary {
		t.Fatalf("new binary not installed: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".prev"); !strings.Contains(string(b), "v1") {
		t.Fatal("previous binary must be kept as .prev")
	}
	wantExe, _ := filepath.EvalSymlinks(exe) // macOS: /var -> /private/var
	if restarted != wantExe {
		t.Fatalf("restart called with %q, want %q", restarted, wantExe)
	}
	if st, _ := os.Stat(exe); st.Mode()&0o111 == 0 {
		t.Fatal("new binary must be executable")
	}

	// maybeUpdate honours the opt-out and same-version cases without touching anything.
	c.cfg.SelfUpdate = false
	restarted = ""
	c.maybeUpdate(context.Background(), good)
	if restarted != "" {
		t.Fatal("must not update when disabled")
	}
}

func TestHTTPBaseWithPrefix(t *testing.T) {
	c, err := New(Config{Server: "https://example.com/probe/ws/agent", Token: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.httpBase != "https://example.com/probe" {
		t.Fatalf("httpBase %q", c.httpBase)
	}
	c, _ = New(Config{Server: "http://example.com", Token: "t"}, nil)
	if c.httpBase != "http://example.com" || c.cfg.Server != "ws://example.com/ws/agent" {
		t.Fatalf("defaults: %q %q", c.httpBase, c.cfg.Server)
	}
}

// TestSelfUpdateRefusesUnauthenticatedTransport: the checksum in an update
// offer arrives over the same connection as the bytes, so it only proves the
// download was not corrupted. Replacing a binary that runs as root therefore
// requires a connection we can actually authenticate.
func TestSelfUpdateRefusesUnauthenticatedTransport(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	offer := protocol.Update{Version: "v2", Path: "/api/agent/download/linux-amd64", SHA256: strings.Repeat("a", 64), Size: 10}

	// Plain http to a real host: refused before anything is downloaded.
	c, err := New(Config{Server: "http://probe.example.com", Token: "t", Name: "n", Version: "v1", SelfUpdate: true}, log)
	if err != nil {
		t.Fatal(err)
	}
	err = c.selfUpdate(context.Background(), offer)
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("plain http must be refused, got %v", err)
	}

	// https but with certificate checks turned off is no better.
	c, err = New(Config{Server: "https://probe.example.com", Token: "t", Name: "n", Version: "v1", SelfUpdate: true, InsecureTLS: true}, log)
	if err != nil {
		t.Fatal(err)
	}
	err = c.selfUpdate(context.Background(), offer)
	if err == nil || !strings.Contains(err.Error(), "TLS verification") {
		t.Fatalf("insecure TLS must be refused, got %v", err)
	}

	// https, and loopback for local development, are both fine.
	for _, server := range []string{"https://probe.example.com", "http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080"} {
		c, err = New(Config{Server: server, Token: "t", Name: "n", Version: "v1", SelfUpdate: true}, log)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.updateTransportOK(); err != nil {
			t.Errorf("%s must be allowed: %v", server, err)
		}
	}
}
