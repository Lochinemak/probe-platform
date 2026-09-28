// Command probe-agent connects to a probe-platform server and executes
// ping / tcping / http / mtr / dns tasks on its behalf.
//
// Usage:
//
//	probe-agent --server https://probe.example.com --token XXX --name home-sz --location "广东 深圳" --isp 电信
//	probe-agent --env-file /etc/probe-agent.env      # KEY=VALUE file instead of flags / environment
//	probe-agent test 8.8.8.8                          # run every probe type locally, no server needed
//	probe-agent test mtr www.qq.com
//	probe-agent version
//	probe-agent service install --env-file C:\ProgramData\probe-agent\probe-agent.env   # Windows service
//
// Every flag can also be given as an environment variable: PROBE_SERVER,
// PROBE_TOKEN, PROBE_NAME, PROBE_LOCATION, PROBE_ISP, PROBE_TAGS,
// PROBE_CONCURRENCY, PROBE_INSECURE, PROBE_SELF_UPDATE, PROBE_LOG_LEVEL,
// PROBE_LOG_FILE, PROBE_ENV_FILE.
//
// On Windows the agent runs as a Windows service (see `probe-agent service`):
// it notices being started by the service control manager, logs to
// probe-agent.log next to the executable and stops cleanly on service stop.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"probe-platform/internal/agent"
	"probe-platform/internal/buildinfo"
	"probe-platform/internal/probe"
	"probe-platform/internal/protocol"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("probe-agent", buildinfo.Version)
			return
		case "test":
			os.Exit(runSelfTest(os.Args[2:]))
		case "service":
			os.Exit(runServiceCommand(os.Args[2:]))
		}
	}

	// --env-file is applied before the flag defaults read the environment, so
	// a KEY=VALUE file behaves exactly like exported variables.
	if p := envFileFromArgs(os.Args[1:]); p != "" {
		if err := loadEnvFile(p); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
	}

	fs := flag.NewFlagSet("probe-agent", flag.ExitOnError)
	server := fs.String("server", envOr("PROBE_SERVER", ""), "server URL, e.g. https://probe.example.com")
	token := fs.String("token", envOr("PROBE_TOKEN", ""), "agent token shared with the server")
	name := fs.String("name", envOr("PROBE_NAME", ""), "agent name (unique per agent; default hostname)")
	location := fs.String("location", envOr("PROBE_LOCATION", ""), "human label for where this agent is, e.g. \"广东 深圳\"")
	isp := fs.String("isp", envOr("PROBE_ISP", ""), "human label for the ISP, e.g. \"电信\"")
	tags := fs.String("tags", envOr("PROBE_TAGS", ""), "comma separated tags")
	conc := fs.Int("concurrency", atoiOr(envOr("PROBE_CONCURRENCY", ""), 4), "max concurrent tasks")
	insecure := fs.Bool("insecure", envOr("PROBE_INSECURE", "") == "true", "skip TLS certificate verification")
	selfUpdate := fs.Bool("self-update", envOr("PROBE_SELF_UPDATE", "true") != "false", "replace this binary when the server ships a different version")
	logLevel := fs.String("log-level", envOr("PROBE_LOG_LEVEL", "info"), "debug|info|warn|error")
	logFile := fs.String("log-file", envOr("PROBE_LOG_FILE", ""), "append logs to this file instead of stderr (Windows service default: probe-agent.log next to the executable)")
	fs.String("env-file", envOr("PROBE_ENV_FILE", ""), "KEY=VALUE file applied to the environment before flags are read")
	_ = fs.Parse(os.Args[1:])

	service := runningAsService()
	logPath := *logFile
	if logPath == "" && service {
		logPath = defaultServiceLogPath()
	}
	var out io.Writer = os.Stderr
	if logPath != "" {
		w, err := openRotatingFile(logPath, 5<<20)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: open log file:", err)
			os.Exit(2)
		}
		defer w.Close()
		out = w
	}
	log := newLogger(*logLevel, out)

	var tagList []string
	for _, t := range strings.Split(*tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tagList = append(tagList, t)
		}
	}
	cli, err := agent.New(agent.Config{
		Server:         *server,
		Token:          *token,
		Name:           *name,
		Location:       *location,
		ISP:            *isp,
		Tags:           tagList,
		MaxConcurrency: *conc,
		InsecureTLS:    *insecure,
		Version:        buildinfo.Version,
		Variant:        buildinfo.Variant,
		SelfUpdate:     *selfUpdate,
	}, log)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		if service {
			// Report through the SCM so `service status` / the event log show a failure instead of a start timeout.
			_ = runAsService(log, func(context.Context) error { return err })
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
			fs.Usage()
		}
		os.Exit(2)
	}
	caps := cli.Capabilities()
	log.Info("probe-agent starting", "version", buildinfo.Version, "arch", runtime.GOOS+"/"+runtime.GOARCH+buildinfo.Variant, "self_update", *selfUpdate, "service", service, "capabilities", strings.Join(caps, ","))
	if !contains(caps, "icmp_raw") {
		log.Warn("no raw ICMP socket: mtr unavailable and ping may fail", "fix", probe.PrivilegeHint())
	}

	if service {
		if err := runAsService(log, cli.Run); err != nil {
			log.Error("service failed", "err", err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx); err != nil {
		log.Error("agent exited", "err", err)
		os.Exit(1)
	}
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func newLogger(level string, w io.Writer) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl}))
}

// runSelfTest executes probes locally and prints JSON. It is the quickest way
// to verify ICMP permissions on a new box.
func runSelfTest(args []string) int {
	types := []protocol.TaskType{protocol.TaskPing, protocol.TaskTCPing, protocol.TaskHTTP, protocol.TaskMTR, protocol.TaskDNS}
	target := "1.1.1.1"
	extra := map[string]any{}
	switch len(args) {
	case 0:
	case 1:
		target = args[0]
	default:
		t := protocol.TaskType(args[0])
		if !t.Valid() {
			fmt.Fprintf(os.Stderr, "unknown type %q (ping|tcping|http|mtr|dns)\n", args[0])
			return 2
		}
		types = []protocol.TaskType{t}
		target = args[1]
		// Remaining args are params, e.g. protocol=tcp port=443 count=3 expect_keyword=hello
		for _, kv := range args[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "params must be key=value, got %q\n", kv)
				return 2
			}
			switch k {
			case "count", "interval_ms", "timeout_ms", "packet_size", "port", "max_hops", "expect_status", "expect_max_ms", "speed_seconds":
				n, err := strconv.Atoi(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s must be a number, got %q\n", k, v)
					return 2
				}
				extra[k] = n
			case "follow_redirects", "insecure_tls", "speed_test", "resolve":
				extra[k] = v == "true" || v == "1"
			default: // ip_version, protocol, method, record_type, dns_server, expect_keyword, body ...
				extra[k] = v
			}
		}
	}
	caps := probe.DetectCapabilities()
	fmt.Fprintln(os.Stderr, "capabilities:", strings.Join(caps, ","))
	if !contains(caps, "icmp_raw") {
		fmt.Fprintln(os.Stderr, "no raw ICMP socket:", probe.PrivilegeHint())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code := 0
	for _, typ := range types {
		task := protocol.Task{ID: "selftest", Type: typ, Target: target, Params: protocol.Params{Count: 4}}
		if len(extra) > 0 {
			raw, _ := json.Marshal(extra)
			if err := json.Unmarshal(raw, &task.Params); err != nil {
				fmt.Fprintf(os.Stderr, "bad params: %v\n", err)
				return 2
			}
		}
		if typ == protocol.TaskDNS && len(extra) == 0 {
			task.Params.Count = 1
		}
		if typ == protocol.TaskHTTP && !strings.Contains(target, "://") {
			task.Target = "https://" + target
		}
		if typ == protocol.TaskTCPing {
			task.Params.Port = 443
		}
		fmt.Fprintf(os.Stderr, "\n== %s %s ==\n", typ, task.Target)
		start := time.Now()
		res, err := probe.Run(ctx, task, func(kind string, data any) {
			if kind != "hops" {
				b, _ := json.Marshal(data)
				fmt.Fprintf(os.Stderr, "  %s %s\n", kind, b)
			}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			code = 1
			continue
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Printf("%s\n", b)
		fmt.Fprintf(os.Stderr, "  done in %s\n", time.Since(start).Round(time.Millisecond))
	}
	return code
}
