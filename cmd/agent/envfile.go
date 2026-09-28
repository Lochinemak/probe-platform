package main

import (
	"fmt"
	"os"
	"strings"
)

// envFileFromArgs finds --env-file (or -env-file, either "=value" or a
// separate value) in args without parsing anything else, falling back to
// PROBE_ENV_FILE. It has to run before the flag defaults read the environment.
func envFileFromArgs(args []string) string {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.TrimLeft(a, "-")
		if a == "env-file" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "env-file="); ok {
			return v
		}
	}
	return os.Getenv("PROBE_ENV_FILE")
}

// loadEnvFile applies KEY=VALUE lines to the process environment, in the
// style of a systemd EnvironmentFile: blank lines and # comments are ignored,
// an optional "export " prefix and matching single or double quotes around the
// value are stripped, CRLF line endings and a UTF-8 BOM are tolerated (the
// Windows installer writes the file from PowerShell). Variables that are
// already set win, so flags > environment > file.
func loadEnvFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("env file: %w", err)
	}
	for n, line := range strings.Split(strings.TrimPrefix(string(b), "\uFEFF"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return fmt.Errorf("env file %s: line %d is not KEY=VALUE", path, n+1)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}
