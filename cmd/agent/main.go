// Command probe-agent connects to a probe-platform server and executes
// ping / tcping / http / mtr tasks on its behalf.
//
// Usage:
//
//	probe-agent --server https://probe.example.com --token XXX --name home-sz --location "广东 深圳" --isp 电信
//	probe-agent test 8.8.8.8            # run every probe type locally, no server needed
//	probe-agent test mtr www.qq.com
//	probe-agent version
//
// Every flag can also be given as an environment variable: PROBE_SERVER,
// PROBE_TOKEN, PROBE_NAME, PROBE_LOCATION, PROBE_ISP, PROBE_TAGS,
// PROBE_CONCURRENCY, PROBE_INSECURE, PROBE_SELF_UPDATE, PROBE_LOG_LEVEL.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	_ = fs.Parse(os.Args[1:])

	log := newLogger(*logLevel)
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
		fmt.Fprintln(os.Stderr, "error:", err)
		fs.Usage()
		os.Exit(2)
	}
	caps := cli.Capabilities()
	log.Info("probe-agent starting", "version", buildinfo.Version, "arch", runtime.GOOS+"/"+runtime.GOARCH+buildinfo.Variant, "self_update", *selfUpdate, "capabilities", strings.Join(caps, ","))
	if !contains(caps, "icmp_raw") {
		log.Warn("no raw ICMP socket: mtr unavailable and ping may fail; run as root, use setcap cap_net_raw+ep, or docker --cap-add NET_RAW")
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

func newLogger(level string) *slog.Logger {
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
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
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
			if n, err := strconv.Atoi(v); err == nil {
				extra[k] = n
			} else if v == "true" || v == "false" {
				extra[k] = v == "true"
			} else {
				extra[k] = v
			}
		}
	}
	fmt.Fprintln(os.Stderr, "capabilities:", strings.Join(probe.DetectCapabilities(), ","))
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
