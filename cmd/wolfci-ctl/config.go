package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape of ~/.config/wolfci/ctl.yaml.
// Overridable via the WOLFCI_CTL_CONFIG environment variable.
type Config struct {
	ServerAddress string `yaml:"server_address"`
	Certificate   string `yaml:"certificate"`
	Key           string `yaml:"key"`
	CACertificate string `yaml:"ca_certificate"`
}

// defaultConfigPath returns the resolved config path:
//
//  1. $WOLFCI_CTL_CONFIG if set, else
//  2. $XDG_CONFIG_HOME/wolfci/ctl.yaml if set, else
//  3. $HOME/.config/wolfci/ctl.yaml.
func defaultConfigPath() (string, error) {
	if env := os.Getenv("WOLFCI_CTL_CONFIG"); env != "" {
		return env, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "wolfci", "ctl.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("wolfci-ctl: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "wolfci", "ctl.yaml"), nil
}

// LoadConfig reads + validates a Config from disk.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wolfci-ctl: read %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("wolfci-ctl: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("wolfci-ctl: %w", err)
	}
	return cfg, nil
}

// Save writes c to path, mkdir-p'ing the parent.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("wolfci-ctl: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("wolfci-ctl: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("wolfci-ctl: write: %w", err)
	}
	return nil
}

// Validate enforces the four required fields.
func (c *Config) Validate() error {
	if c.ServerAddress == "" {
		return errors.New("server_address is required")
	}
	if c.Certificate == "" {
		return errors.New("certificate is required")
	}
	if c.Key == "" {
		return errors.New("key is required")
	}
	if c.CACertificate == "" {
		return errors.New("ca_certificate is required")
	}
	return nil
}
