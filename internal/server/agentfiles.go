package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentFile describes one downloadable agent binary.
type AgentFile struct {
	Key    string `json:"key"` // "<os>-<arch><variant>", e.g. linux-amd64, linux-armv7
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`

	path    string
	modTime time.Time
}

// AgentFiles serves the agent binaries bundled with the server so agents can
// update themselves from the dashboard they already talk to. Files are named
// probe-agent-<os>-<arch>[<variant>][.exe]; checksums are computed lazily and
// cached per (size, mtime).
type AgentFiles struct {
	dir string
	log *slog.Logger

	mu    sync.Mutex
	cache map[string]*AgentFile
}

const agentFilePrefix = "probe-agent-"

// NewAgentFiles returns nil when dir is empty or contains no agent binaries.
func NewAgentFiles(dir string, log *slog.Logger) *AgentFiles {
	if dir == "" {
		return nil
	}
	f := &AgentFiles{dir: dir, log: log, cache: map[string]*AgentFile{}}
	list := f.List()
	if len(list) == 0 {
		log.Info("agent self-update disabled: no probe-agent-* binaries found", "dir", dir)
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, a := range list {
		keys = append(keys, a.Key)
	}
	log.Info("agent self-update enabled", "dir", dir, "platforms", strings.Join(keys, ","))
	return f
}

// Dir returns the directory being served.
func (f *AgentFiles) Dir() string { return f.dir }

func keyFromName(name string) (string, bool) {
	n := strings.TrimSuffix(name, ".exe")
	if !strings.HasPrefix(n, agentFilePrefix) || n == agentFilePrefix {
		return "", false
	}
	return strings.TrimPrefix(n, agentFilePrefix), true
}

// List rescans the directory and returns every agent binary with checksums.
func (f *AgentFiles) List() []*AgentFile {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil
	}
	var out []*AgentFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := keyFromName(e.Name()); !ok {
			continue
		}
		if a, err := f.get(e.Name()); err == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Lookup finds the binary for a platform. arm builds need a variant; without
// one we refuse to guess between v6 and v7.
func (f *AgentFiles) Lookup(goos, goarch, variant string) *AgentFile {
	if goos == "" || goarch == "" {
		return nil
	}
	if goarch == "arm" && variant == "" {
		return nil
	}
	key := goos + "-" + goarch + variant
	return f.ByKey(key)
}

// ByKey returns the binary with the given key ("linux-amd64"), or nil.
func (f *AgentFiles) ByKey(key string) *AgentFile {
	for _, name := range []string{agentFilePrefix + key, agentFilePrefix + key + ".exe"} {
		if a, err := f.get(name); err == nil {
			return a
		}
	}
	return nil
}

// Open opens the file for streaming.
func (f *AgentFiles) Open(a *AgentFile) (*os.File, error) { return os.Open(a.path) }

func (f *AgentFiles) get(name string) (*AgentFile, error) {
	path := filepath.Join(f.dir, name)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.cache[name]; ok && c.Size == st.Size() && c.modTime.Equal(st.ModTime()) {
		return c, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return nil, err
	}
	key, _ := keyFromName(name)
	a := &AgentFile{Key: key, Name: name, Size: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil)), path: path, modTime: st.ModTime()}
	f.cache[name] = a
	return a, nil
}
