//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Windows service support. The same executable is both the service (started
// by the service control manager, detected with IsWindowsService) and the
// command-line tool that registers it:
//
//	probe-agent.exe service install --env-file C:\ProgramData\probe-agent\probe-agent.env
//	probe-agent.exe service start | stop | restart | status | uninstall
//
// Everything after "service install" becomes the service's command line, so
// the configuration lives in the env file rather than in the registry. The
// service runs as LocalSystem (raw sockets for ICMP / MTR just work), starts
// at boot and is restarted by the SCM whenever it exits without a clean stop.
// Self-update relies on that: Windows cannot exec in place, so the updated
// agent simply exits and the recovery action starts the new binary.

const (
	serviceName        = "probe-agent"
	serviceDisplayName = "probe-platform agent"
)

func runningAsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

func defaultServiceLogPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "probe-agent.log")
}

type serviceHandler struct {
	log *slog.Logger
	run func(ctx context.Context) error
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 1 // service-specific exit code: SCM recovery restarts us
			}
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				h.log.Info("service stop requested")
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func runAsService(log *slog.Logger, run func(ctx context.Context) error) error {
	return svc.Run(serviceName, &serviceHandler{log: log, run: run})
}

func runServiceCommand(args []string) int {
	if len(args) == 0 {
		serviceUsage()
		return 2
	}
	var err error
	switch args[0] {
	case "install":
		err = installService(args[1:])
	case "uninstall", "remove":
		err = withService(func(s *mgr.Service) error {
			if err := stopService(s); err != nil {
				return err
			}
			if err := s.Delete(); err != nil {
				return fmt.Errorf("delete service: %w", err)
			}
			fmt.Println("removed service", serviceName)
			return nil
		})
	case "start":
		err = withService(startService)
	case "stop":
		err = withService(stopService)
	case "restart":
		err = withService(func(s *mgr.Service) error {
			if err := stopService(s); err != nil {
				return err
			}
			return startService(s)
		})
	case "status":
		err = withService(func(s *mgr.Service) error {
			st, err := s.Query()
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", serviceName, stateName(st.State))
			if cfg, err := s.Config(); err == nil {
				fmt.Println("command:", cfg.BinaryPathName)
			}
			return nil
		})
	default:
		serviceUsage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			fmt.Fprintln(os.Stderr, "run this from a PowerShell window started as Administrator")
		}
		return 1
	}
	return 0
}

func serviceUsage() {
	fmt.Fprintln(os.Stderr, `usage: probe-agent service <install [agent flags...] | uninstall | start | stop | restart | status>

  install    register (or reconfigure) the "probe-agent" Windows service running this
             executable with the given agent flags, then start it, e.g.
               probe-agent service install --env-file C:\ProgramData\probe-agent\probe-agent.env
             The service runs as LocalSystem, starts at boot, logs to probe-agent.log next
             to the executable and is restarted automatically when it exits (self-update).
  uninstall  stop and remove the service; files are left in place`)
}

func withService(fn func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed (%w)", serviceName, err)
	}
	defer s.Close()
	return fn(s)
}

func installService(agentArgs []string) error {
	for _, a := range agentArgs {
		switch a {
		case "service", "test", "version":
			return fmt.Errorf("unexpected %q after 'service install': only agent flags such as --env-file are allowed", a)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return err
	}
	cmdline := syscall.EscapeArg(exe)
	for _, a := range agentArgs {
		cmdline += " " + syscall.EscapeArg(a)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		DisplayName:  serviceDisplayName,
		Description:  "probe-platform network probe node (ping / tcping / http / mtr / dns). Arguments: " + strings.Join(agentArgs, " "),
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}
	s, err := m.OpenService(serviceName)
	if err == nil {
		// Already installed (upgrade or reconfigure): stop it and point it at
		// this executable with these flags.
		defer s.Close()
		if err := stopService(s); err != nil {
			return err
		}
		cur, err := s.Config()
		if err != nil {
			return fmt.Errorf("read service config: %w", err)
		}
		cur.BinaryPathName = cmdline
		cur.DisplayName, cur.Description = cfg.DisplayName, cfg.Description
		cur.StartType, cur.ErrorControl = cfg.StartType, cfg.ErrorControl
		if err := s.UpdateConfig(cur); err != nil {
			return fmt.Errorf("update service: %w", err)
		}
		fmt.Println("updated service", serviceName)
	} else {
		if s, err = m.CreateService(serviceName, exe, cfg, agentArgs...); err != nil {
			return fmt.Errorf("create service: %w", err)
		}
		defer s.Close()
		fmt.Println("installed service", serviceName)
	}
	// Restart after any exit that is not a clean stop: a crash, or the
	// deliberate exit at the end of a self-update. The last action repeats.
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, 86400); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("set recovery flag: %w", err)
	}
	fmt.Println("command:", cmdline)
	return startService(s)
}

func startService(s *mgr.Service) error {
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		fmt.Println(serviceName, "is running")
		return nil
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	if _, err := waitState(s, svc.Running, 20*time.Second); err != nil {
		return err
	}
	fmt.Println(serviceName, "started")
	return nil
}

func stopService(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}
	if st.State == svc.Stopped {
		return nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("stop service: %w", err)
		}
	}
	if _, err := waitState(s, svc.Stopped, 30*time.Second); err != nil {
		return err
	}
	fmt.Println(serviceName, "stopped")
	return nil
}

func waitState(s *mgr.Service, want svc.State, timeout time.Duration) (svc.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Query()
		if err != nil {
			return st, fmt.Errorf("query service: %w", err)
		}
		if st.State == want {
			return st, nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return st, errors.New("service stopped right after starting; see probe-agent.log next to the executable")
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("timed out waiting for the service to be %s (it is %s)", stateName(want), stateName(st.State))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return "running"
	case svc.ContinuePending, svc.PausePending, svc.Paused:
		return "paused"
	}
	return fmt.Sprintf("state %d", s)
}
