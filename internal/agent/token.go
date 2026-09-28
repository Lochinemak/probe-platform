package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Migration off the shared token. Nodes installed before per-node tokens have
// the shared token in their configuration (/etc/probe-agent.env, which the
// agent cannot write). When such an agent connects, the server hands it the
// node's own token; the agent keeps it in probe-agent.token next to its
// binary and uses it instead of the configured one from then on.
//
// The file records a hash of the token it replaces, so it only applies while
// the configuration still holds that shared token: once the node is
// reinstalled (or its configuration edited) with another token, the file is
// ignored. The install scripts adopt and remove it.

// TokenFileName is the file a handed-over node token is kept in.
const TokenFileName = "probe-agent.token"

// errTokenSwitched ends a session so the agent reconnects with its own token.
var errTokenSwitched = errors.New("switched to this node's own token; reconnecting")

// DefaultTokenFile is where a handed-over token is kept: next to the
// executable, the directory an installed agent already owns for self-update.
func DefaultTokenFile() string {
	exe, err := executablePath()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(exe), TokenFileName)
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// loadIssuedToken returns the node token stored at path if it was issued to
// replace configured, "" otherwise.
func loadIssuedToken(path, configured string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var tok, replaces string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "PROBE_TOKEN":
			tok = v
		case "REPLACES_SHA256":
			replaces = v
		}
	}
	if tok == "" || !strings.EqualFold(replaces, tokenHash(configured)) {
		return ""
	}
	return tok
}

// saveIssuedToken writes the file loadIssuedToken reads, readable by the
// agent's own user only.
func saveIssuedToken(path, replaced, issued string) error {
	if path == "" {
		return errors.New("no token file location")
	}
	if strings.ContainsAny(issued, "\r\n=") {
		return errors.New("malformed token")
	}
	content := "# This node's own token, handed over by the probe-platform server. It replaces the\n" +
		"# shared token configured as PROBE_TOKEN (matched by REPLACES_SHA256) and is ignored\n" +
		"# once PROBE_TOKEN holds anything else. Reinstalling with the node's command removes it.\n" +
		"PROBE_TOKEN=" + issued + "\n" +
		"REPLACES_SHA256=" + tokenHash(replaced) + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := restrictToOwner(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if loadIssuedToken(path, replaced) != issued {
		return fmt.Errorf("%s does not read back", path)
	}
	return nil
}
