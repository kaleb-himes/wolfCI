package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plugin "github.com/kaleb-himes/wolfCI/internal/plugin"
)

var fakeNow = time.Date(2026, 5, 21, 12, 30, 0, 0, time.UTC)

// TestFormatMessage_Failure pins the rendered email for a
// failed build: subject mentions the status + job + build
// number, body lists every field.
func TestFormatMessage_Failure(t *testing.T) {
	cfg := &Config{
		SMTP: SMTPConfig{From: "wolfci@example.com"},
		To:   []string{"oncall@example.com", "lead@example.com"},
	}
	ev := plugin.BuildCompleteEvent{
		JobName:     "build-all",
		BuildNumber: 42,
		Status:      "failure",
		ExitCode:    1,
		Error:       "",
	}
	msg := string(formatMessage(cfg, ev, fakeNow))
	for _, want := range []string{
		"From: wolfci@example.com",
		"To: oncall@example.com, lead@example.com",
		"Subject: wolfCI failure: build-all/42",
		"Status:    failure",
		"Exit code: 1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\nfull:\n%s", want, msg)
		}
	}
}

// TestFormatMessage_IncludesErrorField verifies the optional
// Error field surfaces when non-empty.
func TestFormatMessage_IncludesErrorField(t *testing.T) {
	cfg := &Config{
		SMTP: SMTPConfig{From: "wolfci@example.com"},
		To:   []string{"oncall@example.com"},
	}
	ev := plugin.BuildCompleteEvent{
		JobName: "boom", BuildNumber: 1,
		Status: "error", Error: "shell missing /bin/zsh",
	}
	msg := string(formatMessage(cfg, ev, fakeNow))
	if !strings.Contains(msg, "Error:     shell missing /bin/zsh") {
		t.Errorf("missing error line:\n%s", msg)
	}
}

// TestOnBuildComplete_SkipsSuccess: success does NOT trigger a
// send.
func TestOnBuildComplete_SkipsSuccess(t *testing.T) {
	rec := &recordingSender{}
	p := &emailPlugin{cfg: minConfig(), sender: rec, now: func() time.Time { return fakeNow }}
	err := p.OnBuildComplete(context.Background(), plugin.BuildCompleteEvent{
		JobName: "demo", BuildNumber: 1, Status: "success",
	})
	if err != nil {
		t.Fatalf("OnBuildComplete: %v", err)
	}
	if rec.callCount != 0 {
		t.Errorf("Send call count = %d on success; want 0", rec.callCount)
	}
}

// TestOnBuildComplete_SendsOnFailure: failure triggers exactly
// one Send with the right addr/from/to/msg content.
func TestOnBuildComplete_SendsOnFailure(t *testing.T) {
	rec := &recordingSender{}
	cfg := minConfig()
	p := &emailPlugin{cfg: cfg, sender: rec, now: func() time.Time { return fakeNow }}
	err := p.OnBuildComplete(context.Background(), plugin.BuildCompleteEvent{
		JobName: "demo", BuildNumber: 7, Status: "failure", ExitCode: 2,
	})
	if err != nil {
		t.Fatalf("OnBuildComplete: %v", err)
	}
	if rec.callCount != 1 {
		t.Fatalf("Send call count = %d; want 1", rec.callCount)
	}
	if rec.addr != "smtp.example.com:587" {
		t.Errorf("addr = %q, want smtp.example.com:587", rec.addr)
	}
	if rec.from != "wolfci@example.com" {
		t.Errorf("from = %q, want wolfci@example.com", rec.from)
	}
	if len(rec.to) != 1 || rec.to[0] != "oncall@example.com" {
		t.Errorf("to = %v, want [oncall@example.com]", rec.to)
	}
	if !strings.Contains(string(rec.msg), "Exit code: 2") {
		t.Errorf("msg missing 'Exit code: 2':\n%s", rec.msg)
	}
}

// TestLoadConfig_RoundTrip exercises Save/Load on a minimal
// valid config.
func TestLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlText := "smtp:\n" +
		"  host: smtp.example.com\n" +
		"  port: 587\n" +
		"  from: wolfci@example.com\n" +
		"to:\n" +
		"  - oncall@example.com\n"
	if err := os.WriteFile(path, []byte(yamlText), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SMTP.Host != "smtp.example.com" || cfg.SMTP.Port != 587 {
		t.Errorf("smtp = %+v, want host=smtp.example.com port=587", cfg.SMTP)
	}
	if cfg.SMTP.From != "wolfci@example.com" {
		t.Errorf("from = %q", cfg.SMTP.From)
	}
	if len(cfg.To) != 1 || cfg.To[0] != "oncall@example.com" {
		t.Errorf("to = %v", cfg.To)
	}
}

// TestLoadConfig_RejectsMissingFields enforces every required
// piece of the SMTP block + recipient list.
func TestLoadConfig_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"empty", ""},
		{"missing host", "smtp:\n  port: 587\n  from: a@b\nto:\n  - x@y\n"},
		{"missing port", "smtp:\n  host: h\n  from: a@b\nto:\n  - x@y\n"},
		{"missing from", "smtp:\n  host: h\n  port: 25\nto:\n  - x@y\n"},
		{"empty to", "smtp:\n  host: h\n  port: 25\n  from: a@b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Errorf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

func minConfig() *Config {
	return &Config{
		SMTP: SMTPConfig{
			Host: "smtp.example.com",
			Port: 587,
			From: "wolfci@example.com",
		},
		To: []string{"oncall@example.com"},
	}
}

type recordingSender struct {
	callCount int
	addr      string
	from      string
	to        []string
	msg       []byte
}

func (s *recordingSender) Send(addr, from string, to []string, msg []byte) error {
	s.callCount++
	s.addr = addr
	s.from = from
	s.to = append([]string(nil), to...)
	s.msg = append([]byte(nil), msg...)
	return nil
}
