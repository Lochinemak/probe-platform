package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"probe-platform/internal/protocol"
)

// Self-update: the server bundles agent binaries and offers one whenever an
// agent connects with a different version. We download next to the running
// executable, verify the checksum, make sure the new file runs, wait for
// in-flight tasks, swap atomically and re-exec in place.

// Overridable for tests.
var (
	executablePath = os.Executable
	restartProcess = execSelf
	updateJitter   = 20 * time.Second
	drainTimeout   = 60 * time.Second
)

const updateRetryBackoff = 10 * time.Minute

func (c *Client) maybeUpdate(ctx context.Context, u protocol.Update) {
	if !c.cfg.SelfUpdate || u.Version == "" || u.Version == c.cfg.Version {
		return
	}
	if !c.updating.CompareAndSwap(false, true) {
		return
	}
	defer c.updating.Store(false)
	c.mu.Lock()
	recentFailure := !c.updateFailedAt.IsZero() && time.Since(c.updateFailedAt) < updateRetryBackoff
	c.mu.Unlock()
	if recentFailure {
		c.log.Info("self-update offered but the last attempt failed recently; will retry later", "to", u.Version)
		return
	}
	c.log.Info("self-update offered", "from", c.cfg.Version, "to", u.Version, "path", u.Path)
	if updateJitter > 0 {
		// Spread downloads out: every agent reconnects at once after a deploy.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(rand.Int64N(int64(updateJitter)))):
		}
	}
	if err := c.selfUpdate(ctx, u); err != nil {
		c.log.Error("self-update failed", "err", err)
		c.mu.Lock()
		c.updateFailedAt = time.Now()
		c.mu.Unlock()
	}
}

// updateTransportOK refuses to replace the running binary unless the download
// is authenticated. The checksum in the offer comes from the same connection as
// the bytes, so it only guards against corruption: over plain http, or with
// certificate checks turned off, anyone on the path could serve a binary that
// then runs as root (LocalSystem on Windows) on this node. Loopback is allowed
// so local development still works.
func (c *Client) updateTransportOK() error {
	if c.cfg.InsecureTLS {
		return errors.New("refusing to self-update while TLS verification is disabled (--insecure / PROBE_INSECURE)")
	}
	u, err := url.Parse(c.httpBase)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to self-update over plain http (%s): use an https server URL", c.httpBase)
}

func (c *Client) selfUpdate(ctx context.Context, u protocol.Update) error {
	if err := c.updateTransportOK(); err != nil {
		return err
	}
	exe, err := executablePath()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	dir := filepath.Dir(exe)
	// Keep the executable's extension (".exe" on Windows) so the new file can be run for its version check.
	tmp, err := os.CreateTemp(dir, ".probe-agent-update-*"+filepath.Ext(exe))
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w (install with install-agent.sh so the binary lives in the writable /var/lib/probe-agent)", exe, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed into place

	if err := c.download(ctx, u, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}

	// The new file must at least start and identify itself as the offered version.
	vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, tmpPath, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("new binary does not run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), u.Version) {
		return fmt.Errorf("new binary reports %q, expected version %s", strings.TrimSpace(string(out)), u.Version)
	}

	c.waitForTasks(drainTimeout)

	prev := exe + ".prev"
	_ = os.Remove(prev)
	if err := os.Rename(exe, prev); err != nil {
		return fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		_ = os.Rename(prev, exe)
		return fmt.Errorf("install new binary: %w", err)
	}
	c.log.Info("self-update installed, restarting", "version", u.Version, "exe", exe, "backup", prev)
	return restartProcess(exe)
}

func (c *Client) download(ctx context.Context, u protocol.Update, w io.Writer) error {
	if u.Size <= 0 || u.Size > 512<<20 {
		return fmt.Errorf("implausible size %d", u.Size)
	}
	if !strings.HasPrefix(u.Path, "/") {
		return errors.New("update path must be server-relative")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpBase+u.Path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	client := &http.Client{
		Timeout: 15 * time.Minute,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: c.cfg.InsecureTLS}, //nolint:gosec // operator opt-in
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: http %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, u.Size+1))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n != u.Size {
		return fmt.Errorf("download: got %d bytes, expected %d", n, u.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, u.SHA256) {
		return fmt.Errorf("checksum mismatch: got %s, expected %s", got, u.SHA256)
	}
	return nil
}

// waitForTasks blocks until no task is running or max elapses.
func (c *Client) waitForTasks(max time.Duration) {
	deadline := time.Now().Add(max)
	for {
		c.mu.Lock()
		n := len(c.running)
		c.mu.Unlock()
		if n == 0 || time.Now().After(deadline) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
