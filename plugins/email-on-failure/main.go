// email-on-failure is the wolfCI reference notification
// plugin. It fires an SMTP email to a configured recipient
// list whenever a build's OnBuildComplete arrives with a
// non-success status (failure, error, or cancelled).
//
// Install layout:
//
//	plugins/installed/email-on-failure/
//	    email-on-failure       the binary built from this package
//	    config.yaml            SMTP host/port/from + recipient list
//
// SMTP credentials come from the WOLFCI_EMAIL_PLUGIN_USER and
// WOLFCI_EMAIL_PLUGIN_PASS environment variables so they are
// not committed to disk alongside the YAML config.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	"gopkg.in/yaml.v3"

	plugin "github.com/kaleb-himes/wolfCI/internal/plugin"
)

// Config is the on-disk shape of config.yaml next to the
// plugin binary.
type Config struct {
	SMTP SMTPConfig `yaml:"smtp"`
	// To is the recipient list. Required; at least one entry.
	To []string `yaml:"to"`
}

// SMTPConfig holds the SMTP-server-facing settings.
type SMTPConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	From string `yaml:"from"`
}

// LoadConfig reads + validates the YAML config from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("email-on-failure: read %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("email-on-failure: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("email-on-failure: %w", err)
	}
	return cfg, nil
}

// Validate returns nil if every required field is populated.
func (c *Config) Validate() error {
	if c.SMTP.Host == "" {
		return errors.New("smtp.host is required")
	}
	if c.SMTP.Port == 0 {
		return errors.New("smtp.port is required")
	}
	if c.SMTP.From == "" {
		return errors.New("smtp.from is required")
	}
	if len(c.To) == 0 {
		return errors.New("at least one 'to' address is required")
	}
	return nil
}

// Sender abstracts the SMTP send so tests can record args
// without touching the network.
type Sender interface {
	Send(addr, from string, to []string, msg []byte) error
}

// smtpSender is the production Sender; it uses net/smtp.
type smtpSender struct {
	username string
	password string
}

func (s *smtpSender) Send(addr, from string, to []string, msg []byte) error {
	var auth smtp.Auth
	if s.username != "" {
		host, _, _ := net.SplitHostPort(addr)
		auth = smtp.PlainAuth("", s.username, s.password, host)
	}
	return smtp.SendMail(addr, auth, from, to, msg)
}

// formatMessage builds the RFC 822 message body for the event.
// Test-friendly: takes now so the Date header is deterministic.
func formatMessage(cfg *Config, ev plugin.BuildCompleteEvent, now time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", cfg.SMTP.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: wolfCI %s: %s/%d\r\n", ev.Status, ev.JobName, ev.BuildNumber)
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "\r\n")
	fmt.Fprintf(&b, "Job:       %s\r\n", ev.JobName)
	fmt.Fprintf(&b, "Build:     %d\r\n", ev.BuildNumber)
	fmt.Fprintf(&b, "Status:    %s\r\n", ev.Status)
	fmt.Fprintf(&b, "Exit code: %d\r\n", ev.ExitCode)
	if ev.Error != "" {
		fmt.Fprintf(&b, "Error:     %s\r\n", ev.Error)
	}
	return []byte(b.String())
}

// emailPlugin is the WolfCIPlugin implementation.
type emailPlugin struct {
	cfg    *Config
	sender Sender
	now    func() time.Time
}

func (p *emailPlugin) OnBuildComplete(ctx context.Context, ev plugin.BuildCompleteEvent) error {
	_ = ctx
	// Only fire on non-success terminal states.
	switch ev.Status {
	case "failure", "error", "cancelled":
		// fall through
	default:
		return nil
	}
	msg := formatMessage(p.cfg, ev, p.now())
	addr := fmt.Sprintf("%s:%d", p.cfg.SMTP.Host, p.cfg.SMTP.Port)
	return p.sender.Send(addr, p.cfg.SMTP.From, p.cfg.To, msg)
}

func configPathNextToBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}

func main() {
	cfg, err := LoadConfig(configPathNextToBinary())
	if err != nil {
		log.Fatalf("email-on-failure: %v", err)
	}
	p := &emailPlugin{
		cfg: cfg,
		sender: &smtpSender{
			username: os.Getenv("WOLFCI_EMAIL_PLUGIN_USER"),
			password: os.Getenv("WOLFCI_EMAIL_PLUGIN_PASS"),
		},
		now: time.Now,
	}
	hcplugin.Serve(&hcplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.PluginMap(p),
		GRPCServer:      hcplugin.DefaultGRPCServer,
	})
}
