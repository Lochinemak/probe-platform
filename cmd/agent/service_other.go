//go:build !windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// Service integration only exists on Windows; Linux uses systemd / procd via
// the install scripts.

func runningAsService() bool { return false }

func defaultServiceLogPath() string { return "" }

func runAsService(*slog.Logger, func(context.Context) error) error { return nil }

func runServiceCommand([]string) int {
	fmt.Fprintln(os.Stderr, "probe-agent service: only available on Windows; on Linux use install-agent.sh (systemd) or install-agent-openwrt.sh (procd)")
	return 2
}
