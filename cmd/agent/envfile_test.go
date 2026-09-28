package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvFileFromArgs(t *testing.T) {
	t.Setenv("PROBE_ENV_FILE", "")
	cases := map[string][]string{
		"a.env": {"--env-file", "a.env"},
		"b.env": {"-env-file=b.env", "--name", "x"},
		"c.env": {"--server", "http://x", "--env-file=c.env"},
		"":      {"--server", "http://x", "--name", "env-file"},
	}
	for want, args := range cases {
		if got := envFileFromArgs(args); got != want {
			t.Errorf("%v: got %q, want %q", args, got, want)
		}
	}
	t.Setenv("PROBE_ENV_FILE", "d.env")
	if got := envFileFromArgs(nil); got != "d.env" {
		t.Errorf("PROBE_ENV_FILE fallback: got %q", got)
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-agent.env")
	content := "\uFEFF# comment\r\nPROBE_SERVER=https://probe.example.com\r\n" +
		"export PROBE_NAME=\"home win\"\r\nPROBE_LOCATION='广东 深圳'\r\nPROBE_ISP=\r\n\r\nPROBE_TAGS = a,b\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"PROBE_SERVER", "PROBE_NAME", "PROBE_LOCATION", "PROBE_ISP", "PROBE_TAGS"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	t.Setenv("PROBE_SERVER", "http://already-set") // environment wins over the file
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PROBE_SERVER":   "http://already-set",
		"PROBE_NAME":     "home win",
		"PROBE_LOCATION": "广东 深圳",
		"PROBE_ISP":      "",
		"PROBE_TAGS":     "a,b",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}
	if _, set := os.LookupEnv("PROBE_ISP"); !set {
		t.Error("empty values must still be set")
	}

	bad := filepath.Join(t.TempDir(), "bad.env")
	os.WriteFile(bad, []byte("PROBE_SERVER=x\nthis is not a pair\n"), 0o600)
	if err := loadEnvFile(bad); err == nil {
		t.Error("malformed line must be reported")
	}
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Error("missing file must be reported")
	}
}
