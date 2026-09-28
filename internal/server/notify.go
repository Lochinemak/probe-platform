package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"probe-platform/internal/protocol"
)

// Notifier delivers alert messages to configured channels.
type Notifier struct {
	http *http.Client
	log  *slog.Logger
}

// NewNotifier creates a notifier with sane timeouts.
func NewNotifier(log *slog.Logger) *Notifier {
	return &Notifier{http: &http.Client{Timeout: 15 * time.Second}, log: log}
}

// ChannelTypes lists the supported channel types and their config keys, for
// the dashboard form.
var ChannelTypes = map[string][]string{
	"telegram": {"bot_token", "chat_id"},
	"wecom":    {"webhook_url"},
	"dingtalk": {"webhook_url", "secret"},
	"bark":     {"url"},
	"webhook":  {"url", "auth_header"},
	"smtp":     {"host", "port", "username", "password", "from", "to", "tls"},
	"pushdeer": {"pushkey", "server"},
	"gotify":   {"server", "token", "priority"},
}

// channelSecretKeys lists, per channel type, the config keys that are
// credentials. They are write-only over the API: a response reports whether
// one is set, never its value. A Bark URL is included because the device key
// lives in its path.
var channelSecretKeys = map[string]map[string]bool{
	"telegram": {"bot_token": true},
	"wecom":    {"webhook_url": true},
	"dingtalk": {"webhook_url": true, "secret": true},
	"bark":     {"url": true},
	"webhook":  {"auth_header": true},
	"smtp":     {"password": true},
	"pushdeer": {"pushkey": true},
	"gotify":   {"token": true},
}

// isSecretKey reports whether key is a credential for this channel type.
func isSecretKey(chType, key string) bool { return channelSecretKeys[chType][key] }

// pushdeerDefaultServer is the hosted PushDeer API; self-hosted instances
// override it with the "server" config key.
const pushdeerDefaultServer = "https://api2.pushdeer.com"

// gotifyDefaultPriority is used when the channel leaves "priority" empty.
// Gotify's Android client rings for priority >= 4.
const gotifyDefaultPriority = 5

// Send delivers title/text to one channel.
func (n *Notifier) Send(ctx context.Context, ch *protocol.NotifyChannel, title, text string) error {
	cfg := ch.Config
	switch ch.Type {
	case "telegram":
		if cfg["bot_token"] == "" || cfg["chat_id"] == "" {
			return errors.New("telegram needs bot_token and chat_id")
		}
		return n.postJSON(ctx, "https://api.telegram.org/bot"+cfg["bot_token"]+"/sendMessage", nil,
			map[string]any{"chat_id": cfg["chat_id"], "text": title + "\n" + text, "disable_web_page_preview": true})
	case "wecom":
		if cfg["webhook_url"] == "" {
			return errors.New("wecom needs webhook_url")
		}
		return n.postJSON(ctx, cfg["webhook_url"], nil, map[string]any{"msgtype": "text", "text": map[string]string{"content": title + "\n" + text}})
	case "dingtalk":
		u := cfg["webhook_url"]
		if u == "" {
			return errors.New("dingtalk needs webhook_url")
		}
		if secret := cfg["secret"]; secret != "" {
			ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(ts + "\n" + secret))
			sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			sep := "&"
			if !strings.Contains(u, "?") {
				sep = "?"
			}
			u += sep + "timestamp=" + ts + "&sign=" + sign
		}
		return n.postJSON(ctx, u, nil, map[string]any{"msgtype": "text", "text": map[string]string{"content": title + "\n" + text}})
	case "bark":
		if cfg["url"] == "" {
			return errors.New("bark needs url (https://api.day.app/<key>)")
		}
		return n.postJSON(ctx, strings.TrimSuffix(cfg["url"], "/"), nil, map[string]any{"title": title, "body": text, "group": "probe-platform"})
	case "webhook":
		if cfg["url"] == "" {
			return errors.New("webhook needs url")
		}
		hdr := map[string]string{}
		if v := cfg["auth_header"]; v != "" {
			if k, val, ok := strings.Cut(v, ":"); ok {
				hdr[strings.TrimSpace(k)] = strings.TrimSpace(val)
			}
		}
		return n.postJSON(ctx, cfg["url"], hdr, map[string]any{"title": title, "text": text, "at": time.Now().Format(time.RFC3339)})
	case "smtp":
		return n.sendMail(cfg, title, text)
	case "pushdeer":
		return n.sendPushDeer(ctx, cfg, title, text)
	case "gotify":
		return n.sendGotify(ctx, cfg, title, text)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

// sendPushDeer posts to the official or a self-hosted PushDeer server.
// The message is sent as markdown: "text" becomes the headline and "desp" the
// body, so each line of the alert stays on its own line in the app.
func (n *Notifier) sendPushDeer(ctx context.Context, cfg map[string]string, title, text string) error {
	if cfg["pushkey"] == "" {
		return errors.New("pushdeer needs pushkey")
	}
	server := strings.TrimSuffix(strings.TrimSpace(cfg["server"]), "/")
	if server == "" {
		server = pushdeerDefaultServer
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}
	form := url.Values{
		"pushkey": {cfg["pushkey"]},
		"text":    {title},
		"desp":    {strings.ReplaceAll(text, "\n", "\n\n")},
		"type":    {"markdown"},
	}
	rb, err := n.postForm(ctx, server+"/message/push", form)
	if err != nil {
		return err
	}
	// PushDeer reports failures inside a 200 body: {"code":80100,"error":"..."}.
	var res struct {
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rb, &res); err != nil {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(rb)))
	}
	if res.Code != 0 {
		return fmt.Errorf("rejected: %d %s", res.Code, res.Error)
	}
	return nil
}

// sendGotify posts to a Gotify server using an application token.
func (n *Notifier) sendGotify(ctx context.Context, cfg map[string]string, title, text string) error {
	server := strings.TrimSuffix(strings.TrimSpace(cfg["server"]), "/")
	if server == "" || cfg["token"] == "" {
		return errors.New("gotify needs server and token")
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}
	priority := gotifyDefaultPriority
	if v := strings.TrimSpace(cfg["priority"]); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 0 || p > 10 {
			return errors.New("gotify priority must be an integer between 0 and 10")
		}
		priority = p
	}
	return n.postJSON(ctx, server+"/message", map[string]string{"X-Gotify-Key": cfg["token"]},
		map[string]any{"title": title, "message": text, "priority": priority})
}

func (n *Notifier) postJSON(ctx context.Context, u string, headers map[string]string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rb, err := n.do(req)
	if err != nil {
		return err
	}
	// Telegram / WeCom / DingTalk report errors inside a 200 body.
	var probe struct {
		OK      *bool  `json:"ok"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(rb, &probe) == nil {
		if probe.OK != nil && !*probe.OK {
			return fmt.Errorf("rejected: %s", strings.TrimSpace(string(rb)))
		}
		if probe.ErrCode != 0 {
			return fmt.Errorf("rejected: %d %s", probe.ErrCode, probe.ErrMsg)
		}
	}
	return nil
}

// postForm sends an application/x-www-form-urlencoded POST and returns the
// (truncated) response body for caller-specific error checks.
func (n *Notifier) postForm(ctx context.Context, u string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return n.do(req)
}

// do executes req, treating any non-2xx status as an error, and returns up to
// 4 KiB of the body.
func (n *Notifier) do(req *http.Request) ([]byte, error) {
	resp, err := n.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return rb, nil
}

func (n *Notifier) sendMail(cfg map[string]string, subject, text string) error {
	host, from, to := cfg["host"], cfg["from"], cfg["to"]
	if host == "" || from == "" || to == "" {
		return errors.New("smtp needs host, from and to")
	}
	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(host, port)
	var recipients []string
	for _, r := range strings.Split(to, ",") {
		if r = strings.TrimSpace(r); r != "" {
			recipients = append(recipients, r)
		}
	}
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"",
		text,
	}, "\r\n")

	var auth smtp.Auth
	if cfg["username"] != "" {
		auth = smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	}
	mode := strings.ToLower(cfg["tls"])
	if mode == "" && port == "465" {
		mode = "ssl"
	}
	if mode != "ssl" {
		// Plain connection; net/smtp upgrades with STARTTLS when offered.
		return smtp.SendMail(addr, auth, from, recipients, []byte(msg))
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: host}) //nolint:gosec
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range recipients {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
