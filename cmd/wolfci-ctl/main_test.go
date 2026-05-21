package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureFiles returns two pipe writers we can hand to dispatch
// + the reader side so the test can read what the dispatcher
// wrote. *os.File is what the subcommand signature expects;
// pipes satisfy that and let us assert on output.
func captureFiles(t *testing.T) (stdout, stderr *os.File, readOut, readErr func() string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		outR.Close()
		t.Fatalf("pipe stderr: %v", err)
	}
	t.Cleanup(func() {
		outW.Close()
		errW.Close()
		outR.Close()
		errR.Close()
	})
	read := func(r *os.File, w *os.File) string {
		w.Close()
		data, _ := io.ReadAll(r)
		return string(data)
	}
	return outW, errW, func() string { return read(outR, outW) }, func() string { return read(errR, errW) }
}

// TestDispatch_Version returns 0 and prints "wolfci-ctl <ver>"
// to stdout.
func TestDispatch_Version(t *testing.T) {
	out, err, getOut, _ := captureFiles(t)
	code := dispatch([]string{"version"}, out, err)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := getOut()
	if !strings.Contains(got, "wolfci-ctl ") {
		t.Errorf("stdout = %q, want to contain 'wolfci-ctl '", got)
	}
}

// TestDispatch_NoArgs prints usage to stdout and returns 0.
func TestDispatch_NoArgs(t *testing.T) {
	out, err, getOut, _ := captureFiles(t)
	code := dispatch(nil, out, err)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := getOut()
	if !strings.Contains(got, "Subcommands:") {
		t.Errorf("stdout = %q, want usage block", got)
	}
	for _, want := range []string{"login", "version"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage missing subcommand %q", want)
		}
	}
}

// TestDispatch_UnknownSubcommand returns 2 and complains to
// stderr.
func TestDispatch_UnknownSubcommand(t *testing.T) {
	out, err, _, getErr := captureFiles(t)
	code := dispatch([]string{"bogus"}, out, err)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	got := getErr()
	if !strings.Contains(got, "unknown subcommand") {
		t.Errorf("stderr = %q, want unknown-subcommand complaint", got)
	}
}

// TestLogin_WritesConfig is the gating test for the 8.1a login
// subcommand: it writes a complete ctl.yaml at the path passed
// via --config, and the saved file round-trips via LoadConfig.
func TestLogin_WritesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ctl.yaml")

	out, err, getOut, _ := captureFiles(t)
	code := dispatch([]string{"login",
		"--server", "ci.example.com:8443",
		"--cert", "/etc/wolfci/cli.crt",
		"--key", "/etc/wolfci/cli.key",
		"--ca-cert", "/etc/wolfci/ca.crt",
		"--config", cfgPath,
	}, out, err)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := getOut()
	if !strings.Contains(got, cfgPath) {
		t.Errorf("stdout should mention config path: %q", got)
	}

	cfg, err2 := LoadConfig(cfgPath)
	if err2 != nil {
		t.Fatalf("LoadConfig: %v", err2)
	}
	if cfg.ServerAddress != "ci.example.com:8443" {
		t.Errorf("ServerAddress = %q", cfg.ServerAddress)
	}
	if cfg.Certificate != "/etc/wolfci/cli.crt" {
		t.Errorf("Certificate = %q", cfg.Certificate)
	}
	if cfg.Key != "/etc/wolfci/cli.key" {
		t.Errorf("Key = %q", cfg.Key)
	}
	if cfg.CACertificate != "/etc/wolfci/ca.crt" {
		t.Errorf("CACertificate = %q", cfg.CACertificate)
	}
}

// TestLogin_MissingFlags returns 2 and lists what is missing.
func TestLogin_MissingFlags(t *testing.T) {
	out, err, _, getErr := captureFiles(t)
	code := dispatch([]string{"login", "--server", "x"}, out, err)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	got := getErr()
	for _, want := range []string{"missing required flag", "--cert", "--key", "--ca-cert"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr missing %q\nfull: %s", want, got)
		}
	}
}

// TestDefaultConfigPath honors WOLFCI_CTL_CONFIG > XDG_CONFIG_HOME
// > $HOME/.config in that order.
func TestDefaultConfigPath(t *testing.T) {
	// 1) Explicit env var wins.
	t.Setenv("WOLFCI_CTL_CONFIG", "/tmp/explicit-config.yaml")
	t.Setenv("XDG_CONFIG_HOME", "/should-not-be-used")
	got, err := defaultConfigPath()
	if err != nil {
		t.Fatalf("defaultConfigPath: %v", err)
	}
	if got != "/tmp/explicit-config.yaml" {
		t.Errorf("env path = %q, want /tmp/explicit-config.yaml", got)
	}

	// 2) XDG_CONFIG_HOME wins when WOLFCI_CTL_CONFIG is empty.
	t.Setenv("WOLFCI_CTL_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	got, err = defaultConfigPath()
	if err != nil {
		t.Fatalf("defaultConfigPath: %v", err)
	}
	if got != "/x/cfg/wolfci/ctl.yaml" {
		t.Errorf("xdg path = %q, want /x/cfg/wolfci/ctl.yaml", got)
	}
}
