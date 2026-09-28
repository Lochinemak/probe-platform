package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"probe-platform/internal/protocol"
)

const (
	maxBodyRead  = 8 << 20   // read at most 8 MiB to time the transfer
	maxSpeedRead = 512 << 20 // hard cap for a speed-test download
)

// HTTP performs one or more HTTP requests against target and reports a phase
// breakdown (DNS, TCP connect, TLS, TTFB, transfer) for each. When redirects
// are followed the phase timings describe the final request while TotalMs
// covers the whole chain.
//
// progress receives one "attempt" (protocol.HTTPAttempt) per request.
func HTTP(ctx context.Context, target string, p protocol.Params, progress ProgressFunc) (*protocol.HTTPResult, error) {
	count := clamp(def(p.Count, 1), 1, 20)
	interval := time.Duration(def(p.IntervalMs, 500)) * time.Millisecond
	timeout := time.Duration(def(p.TimeoutMs, 10000)) * time.Millisecond
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = "GET"
	}

	rawURL := strings.TrimSpace(target)
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid url %q", target)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	// Resolve up front purely to reject fake-IP answers; the HTTP client does
	// its own lookup for the request itself.
	if _, err := Resolve(ctx, u.Hostname(), p.IPVersion); err != nil {
		return nil, err
	}

	res := &protocol.HTTPResult{URL: u.String()}
	var rtts []float64
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := sleepCtx(ctx, interval); err != nil {
				break
			}
		}
		a := doHTTP(ctx, u.String(), method, p, timeout)
		a.Seq = i
		res.Attempts = append(res.Attempts, a)
		if a.OK && a.AssertOK {
			rtts = append(rtts, a.Timing.TotalMs)
		}
		progress.emit("attempt", a)
		if ctx.Err() != nil {
			break
		}
	}
	res.Stats = ComputeStats(len(res.Attempts), rtts)
	return res, nil
}

type phaseClock struct {
	mu                  sync.Mutex
	start               time.Time
	dnsStart, dnsDone   time.Time
	connStart, connDone time.Time
	tlsStart, tlsDone   time.Time
	wroteReq, firstByte time.Time
	ip                  string
	tlsState            *tls.ConnectionState
}

func (c *phaseClock) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dnsStart, c.dnsDone = time.Time{}, time.Time{}
	c.connStart, c.connDone = time.Time{}, time.Time{}
	c.tlsStart, c.tlsDone = time.Time{}, time.Time{}
	c.wroteReq, c.firstByte = time.Time{}, time.Time{}
	c.ip = ""
	c.tlsState = nil
}

func doHTTP(ctx context.Context, rawURL, method string, p protocol.Params, timeout time.Duration) protocol.HTTPAttempt {
	a := protocol.HTTPAttempt{URL: rawURL}
	clock := &phaseClock{start: time.Now()}

	trace := &httptrace.ClientTrace{
		// GetConn fires once per request in a redirect chain; reset the phase
		// timers so the reported phases describe the final request.
		GetConn: func(string) { clock.reset() },
		DNSStart: func(httptrace.DNSStartInfo) {
			clock.mu.Lock()
			clock.dnsStart = time.Now()
			clock.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			clock.mu.Lock()
			clock.dnsDone = time.Now()
			clock.mu.Unlock()
		},
		ConnectStart: func(_, _ string) {
			clock.mu.Lock()
			if clock.connStart.IsZero() {
				clock.connStart = time.Now()
			}
			clock.mu.Unlock()
		},
		ConnectDone: func(_, addr string, err error) {
			clock.mu.Lock()
			if err == nil {
				clock.connDone = time.Now()
				if h, _, e := net.SplitHostPort(addr); e == nil {
					clock.ip = h
				}
			}
			clock.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			clock.mu.Lock()
			clock.tlsStart = time.Now()
			clock.mu.Unlock()
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
			clock.mu.Lock()
			if err == nil {
				clock.tlsDone = time.Now()
				clock.tlsState = &cs
			}
			clock.mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			clock.mu.Lock()
			clock.wroteReq = time.Now()
			clock.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			clock.mu.Lock()
			clock.firstByte = time.Now()
			clock.mu.Unlock()
		},
	}

	network := "tcp"
	switch p.IPVersion {
	case "4":
		network = "tcp4"
	case "6":
		network = "tcp6"
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	transport := &http.Transport{
		Proxy: nil, // always measure the direct path, ignore HTTP_PROXY
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: p.InsecureTLS}, //nolint:gosec // operator-controlled toggle
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !p.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			status := 0
			if req.Response != nil {
				status = req.Response.StatusCode
			}
			a.Redirects = append(a.Redirects, fmt.Sprintf("%d %s", status, req.URL.String()))
			return nil
		},
	}

	var body io.Reader
	if p.Body != "" && method != "GET" && method != "HEAD" {
		body = strings.NewReader(p.Body)
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, rawURL, body)
	if err != nil {
		a.Error = ErrString(err)
		return a
	}
	req.Header.Set("User-Agent", "probe-platform/1.0 (+https://github.com/probe-platform)")
	req.Header.Set("Accept", "*/*")
	for k, v := range p.Headers {
		if strings.EqualFold(k, "host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		a.Error = ErrString(err)
		a.Timing.TotalMs = ms(time.Since(clock.start))
		clock.mu.Lock()
		a.IP = clock.ip
		clock.mu.Unlock()
		return a
	}
	defer resp.Body.Close()

	// Body: normally read (and discard) up to 8 MiB to time the transfer.
	// With a keyword to check, keep the bytes. With a speed test, keep
	// downloading for the window and measure throughput.
	var bodyBuf bytes.Buffer
	var n int64
	var readErr error
	switch {
	case p.SpeedTest:
		window := time.Duration(clamp(def(p.SpeedSeconds, 5), 1, 60)) * time.Second
		speedStart := time.Now()
		var sink io.Writer = io.Discard
		if p.ExpectKeyword != "" {
			sink = &limitedWriter{w: &bodyBuf, n: maxBodyRead}
		}
		n, readErr = copyFor(ctx, sink, resp.Body, window, maxSpeedRead)
		a.SpeedMs = ms(time.Since(speedStart))
		a.SpeedBytes = n
		if a.SpeedMs > 0 {
			a.ThroughputMbps = round3(float64(n) * 8 / (a.SpeedMs / 1000) / 1e6)
		}
	case p.ExpectKeyword != "":
		n, readErr = io.Copy(&bodyBuf, io.LimitReader(resp.Body, maxBodyRead))
	default:
		n, readErr = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
	}
	end := time.Now()

	clock.mu.Lock()
	defer clock.mu.Unlock()

	a.OK = true
	a.StatusCode = resp.StatusCode
	a.Status = resp.Status
	a.Proto = resp.Proto
	a.ContentLength = resp.ContentLength
	a.BodyBytes = n
	a.IP = clock.ip
	a.FinalURL = resp.Request.URL.String()
	if readErr != nil {
		a.Error = "body: " + ErrString(readErr)
	}
	a.Headers = make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		a.Headers[k] = strings.Join(v, ", ")
	}
	if cs := resp.TLS; cs != nil {
		fillTLS(&a, cs)
	} else if clock.tlsState != nil {
		fillTLS(&a, clock.tlsState)
	}

	t := &a.Timing
	if !clock.dnsStart.IsZero() && !clock.dnsDone.IsZero() {
		t.DNSMs = ms(clock.dnsDone.Sub(clock.dnsStart))
	}
	if !clock.connStart.IsZero() && !clock.connDone.IsZero() {
		t.ConnectMs = ms(clock.connDone.Sub(clock.connStart))
	}
	if !clock.tlsStart.IsZero() && !clock.tlsDone.IsZero() {
		t.TLSMs = ms(clock.tlsDone.Sub(clock.tlsStart))
	}
	if !clock.wroteReq.IsZero() && !clock.firstByte.IsZero() {
		t.TTFBMs = ms(clock.firstByte.Sub(clock.wroteReq))
	}
	if !clock.firstByte.IsZero() {
		t.TransferMs = ms(end.Sub(clock.firstByte))
	}
	t.TotalMs = ms(end.Sub(clock.start))

	a.Assertions, a.AssertOK = evalAssertions(p, &a, bodyBuf.Bytes())
	return a
}

// evalAssertions applies the configured checks. When no status is expected,
// anything below 400 passes.
func evalAssertions(p protocol.Params, a *protocol.HTTPAttempt, body []byte) ([]protocol.Assertion, bool) {
	var out []protocol.Assertion
	ok := true
	add := func(name string, pass bool, detail string) {
		out = append(out, protocol.Assertion{Name: name, Pass: pass, Detail: detail})
		if !pass {
			ok = false
		}
	}
	if p.ExpectStatus > 0 {
		add("状态码", a.StatusCode == p.ExpectStatus, fmt.Sprintf("%d，期望 %d", a.StatusCode, p.ExpectStatus))
	} else {
		add("状态码", a.StatusCode < 400, fmt.Sprintf("%d，期望 < 400", a.StatusCode))
	}
	if p.ExpectKeyword != "" {
		found := bytes.Contains(body, []byte(p.ExpectKeyword))
		detail := "响应体包含 " + fmt.Sprintf("%q", p.ExpectKeyword)
		if !found {
			detail = fmt.Sprintf("响应体（前 %d 字节）不含 %q", len(body), p.ExpectKeyword)
		}
		add("关键字", found, detail)
	}
	if p.ExpectMaxMs > 0 {
		add("耗时", a.Timing.TotalMs <= float64(p.ExpectMaxMs), fmt.Sprintf("%.0f ms，上限 %d ms", a.Timing.TotalMs, p.ExpectMaxMs))
	}
	return out, ok
}

// copyFor copies from r to w until the window elapses, EOF, or max bytes.
func copyFor(ctx context.Context, w io.Writer, r io.Reader, window time.Duration, max int64) (int64, error) {
	deadline := time.Now().Add(window)
	buf := make([]byte, 64<<10)
	var total int64
	for total < max {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return total, nil
		}
		n, err := r.Read(buf)
		if n > 0 {
			total += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}

// limitedWriter keeps the first n bytes and silently drops the rest.
type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	keep := p
	if int64(len(keep)) > l.n {
		keep = keep[:l.n]
	}
	l.n -= int64(len(keep))
	if _, err := l.w.Write(keep); err != nil {
		return 0, err
	}
	return len(p), nil
}

func fillTLS(a *protocol.HTTPAttempt, cs *tls.ConnectionState) {
	a.TLSVersion = tls.VersionName(cs.Version)
	a.TLSCipher = tls.CipherSuiteName(cs.CipherSuite)
	if len(cs.PeerCertificates) > 0 {
		c := cs.PeerCertificates[0]
		a.CertSubject = c.Subject.CommonName
		if a.CertSubject == "" && len(c.DNSNames) > 0 {
			a.CertSubject = c.DNSNames[0]
		}
		a.CertIssuer = c.Issuer.CommonName
		if a.CertIssuer == "" && len(c.Issuer.Organization) > 0 {
			a.CertIssuer = c.Issuer.Organization[0]
		}
		na := c.NotAfter
		a.CertNotAfter = &na
	}
}
