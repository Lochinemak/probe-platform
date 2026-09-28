// Package agent implements the probe agent: it keeps an outbound WebSocket
// connection to the server (so it works behind home NAT), receives tasks and
// streams results back.
package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

// Config configures an agent.
type Config struct {
	Server         string // http(s):// or ws(s):// URL of the server; /ws/agent is appended when no path is given
	Token          string
	Name           string
	Location       string
	ISP            string
	Tags           []string
	MaxConcurrency int
	InsecureTLS    bool
	Version        string
}

// Client is a running agent.
type Client struct {
	cfg  Config
	log  *slog.Logger
	caps []string

	mu      sync.Mutex
	running map[string]context.CancelFunc
	sem     chan struct{}
}

// New validates cfg and prepares a client.
func New(cfg Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Server == "" {
		return nil, errors.New("server URL is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("token is required")
	}
	u, err := url.Parse(cfg.Server)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("server URL must be http(s):// or ws(s)://, got %q", u.Scheme)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws/agent"
	}
	cfg.Server = u.String()
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	if cfg.Name == "" {
		return nil, errors.New("agent name is required")
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	return &Client{
		cfg:     cfg,
		log:     log,
		caps:    probe.DetectCapabilities(),
		running: map[string]context.CancelFunc{},
		sem:     make(chan struct{}, cfg.MaxConcurrency),
	}, nil
}

// Capabilities reports what this agent detected it can do.
func (c *Client) Capabilities() []string { return c.caps }

// Run connects and reconnects until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		c.log.Warn("disconnected from server", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

func (c *Client) session(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: c.cfg.InsecureTLS}, //nolint:gosec // explicit operator opt-in
	}
	hdr := http.Header{"Authorization": {"Bearer " + c.cfg.Token}}
	conn, resp, err := dialer.DialContext(ctx, c.cfg.Server, hdr)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: %w (http %d)", c.cfg.Server, err, resp.StatusCode)
		}
		return fmt.Errorf("dial %s: %w", c.cfg.Server, err)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)

	hostname, _ := os.Hostname()
	hello, err := protocol.NewMessage(protocol.MsgHello, protocol.Hello{
		Name:           c.cfg.Name,
		Version:        c.cfg.Version,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Hostname:       hostname,
		Location:       c.cfg.Location,
		ISP:            c.cfg.ISP,
		Tags:           c.cfg.Tags,
		Capabilities:   c.caps,
		MaxConcurrency: c.cfg.MaxConcurrency,
	})
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var first protocol.Message
	if err := conn.ReadJSON(&first); err != nil {
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	if first.Type != protocol.MsgWelcome {
		return fmt.Errorf("unexpected first message %q", first.Type)
	}
	var welcome protocol.Welcome
	_ = json.Unmarshal(first.Payload, &welcome)
	c.log.Info("connected", "server", c.cfg.Server, "agent_id", welcome.AgentID, "public_ip", welcome.PublicIP, "caps", strings.Join(c.caps, ","))

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	send := make(chan protocol.Message, 512)
	errc := make(chan error, 2)

	// Writer: the only goroutine calling WriteJSON.
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case m := <-send:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(m); err != nil {
					errc <- fmt.Errorf("write: %w", err)
					return
				}
			case <-ping.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					errc <- fmt.Errorf("ping: %w", err)
					return
				}
			}
		}
	}()

	// Reader.
	go func() {
		const readTimeout = 90 * time.Second
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readTimeout)) })
		for {
			_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
			var m protocol.Message
			if err := conn.ReadJSON(&m); err != nil {
				errc <- fmt.Errorf("read: %w", err)
				return
			}
			c.handle(sessCtx, m, send)
		}
	}()

	select {
	case err := <-errc:
		cancel()
		c.cancelAll()
		return err
	case <-ctx.Done():
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"), time.Now().Add(2*time.Second))
		c.cancelAll()
		return ctx.Err()
	}
}

func (c *Client) handle(ctx context.Context, m protocol.Message, send chan<- protocol.Message) {
	switch m.Type {
	case protocol.MsgTask:
		var t protocol.Task
		if err := json.Unmarshal(m.Payload, &t); err != nil {
			c.log.Warn("bad task payload", "err", err)
			return
		}
		go c.runTask(ctx, t, send)
	case protocol.MsgCancel:
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(m.Payload, &p); err == nil {
			c.mu.Lock()
			if cancel, ok := c.running[p.TaskID]; ok {
				cancel()
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) cancelAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, cancel := range c.running {
		cancel()
		delete(c.running, id)
	}
}

func (c *Client) runTask(ctx context.Context, t protocol.Task, send chan<- protocol.Message) {
	tctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.running[t.ID] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.running, t.ID)
		c.mu.Unlock()
	}()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-tctx.Done():
		return
	}

	log := c.log.With("task", t.ID, "type", t.Type, "target", t.Target)
	log.Info("task start")
	start := time.Now()
	seq := 0
	progress := func(kind string, data any) {
		seq++
		b, err := json.Marshal(data)
		if err != nil {
			return
		}
		msg, err := protocol.NewMessage(protocol.MsgProgress, protocol.Progress{TaskID: t.ID, Seq: seq, Kind: kind, Data: b})
		if err != nil {
			return
		}
		select {
		case send <- msg:
		default: // never block a probe on a slow link; the final result still carries everything
		}
	}

	data, err := probe.Run(tctx, t, progress)
	res := protocol.Result{TaskID: t.ID, OK: err == nil, DurationMs: float64(time.Since(start)) / float64(time.Millisecond)}
	if data != nil {
		res.Data, _ = json.Marshal(data)
	}
	if err != nil {
		res.Error = err.Error()
	}
	if tctx.Err() != nil && ctx.Err() == nil {
		res.OK = false
		res.Error = "cancelled"
	}
	log.Info("task done", "ok", res.OK, "err", res.Error, "ms", int(res.DurationMs))

	msg, err := protocol.NewMessage(protocol.MsgResult, res)
	if err != nil {
		return
	}
	select {
	case send <- msg:
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
	}
}
