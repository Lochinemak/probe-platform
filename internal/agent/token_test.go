package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"probe-platform/internal/protocol"
)

func TestTokenFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), TokenFileName)
	if loadIssuedToken(path, "shared") != "" {
		t.Fatal("missing file must yield nothing")
	}
	if err := saveIssuedToken(path, "shared", "own"); err != nil {
		t.Fatal(err)
	}
	if got := loadIssuedToken(path, "shared"); got != "own" {
		t.Fatalf("load: %q", got)
	}
	// Reinstalled (or edited) with another token: the file no longer applies.
	if got := loadIssuedToken(path, "reset-token"); got != "" {
		t.Fatalf("file must be ignored for another configured token, got %q", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	if err := saveIssuedToken(path, "shared", "bad\ntoken"); err == nil {
		t.Fatal("a token with a newline must be refused")
	}
}

// TestTokenHandoff: an agent on the shared token is handed its own, saves it,
// reconnects with it at once and starts with it next time.
func TestTokenHandoff(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	var hellos []protocol.Hello
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		auths = append(auths, auth)
		mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var m protocol.Message
		if conn.ReadJSON(&m) != nil {
			return
		}
		var h protocol.Hello
		_ = json.Unmarshal(m.Payload, &h)
		mu.Lock()
		hellos = append(hellos, h)
		mu.Unlock()
		wel := protocol.Welcome{AgentID: "n1"}
		if auth == "Bearer shared" && h.TokenHandoff {
			wel.Token = "own"
		}
		msg, _ := protocol.NewMessage(protocol.MsgWelcome, wel)
		_ = conn.WriteJSON(msg)
		for conn.ReadJSON(&m) == nil {
		}
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), TokenFileName)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{Server: srv.URL, Token: "shared", Name: "n1", Version: "v1", TokenFile: path}
	c, err := New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(auths)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(auths) < 2 || auths[0] != "Bearer shared" || auths[1] != "Bearer own" {
		t.Fatalf("connections: %v", auths)
	}
	if !hellos[0].TokenHandoff {
		t.Fatal("hello must advertise the handover")
	}
	if c.authToken() != "own" {
		t.Fatalf("in-memory token %q", c.authToken())
	}
	// Next start (e.g. after a self-update re-exec) uses the saved token.
	c2, _ := New(cfg, log)
	if c2.authToken() != "own" {
		t.Fatalf("restart: %q", c2.authToken())
	}
	// Without a token file (containers) nothing is advertised or adopted.
	cfg.TokenFile = ""
	c3, _ := New(cfg, log)
	if c3.authToken() != "shared" || c3.adoptToken("own") {
		t.Fatal("no token file: must stay on the configured token")
	}
}

func TestTokenHandoffUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a directory the test user cannot write")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	c, err := New(Config{Server: "https://probe.example.com", Token: "shared", Name: "n", TokenFile: filepath.Join(dir, TokenFileName)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if c.adoptToken("own") || c.authToken() != "shared" {
		t.Fatal("a token that cannot be saved must not be switched to")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}
