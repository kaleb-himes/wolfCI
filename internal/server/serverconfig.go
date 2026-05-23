package server

/* ServerConfig is the on-disk shape of config-files/server.yaml
 * (PLAN.md Phase 11). cmd/wolfci loads it before wiring up the
 * dependency graph (storage + scheduler + agentsvc + cliservice +
 * plugin host + server UI). The format is YAML 1.2 to match every
 * other config file in the tree.
 */

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "gopkg.in/yaml.v3"
)

const (
    defaultPluginDir              = "plugins/"
    defaultShutdownDrainTimeout   = 30 * time.Second
    defaultRetentionSweepInterval = 5 * time.Minute
)

type ServerConfig struct {
    /* ListenAddr is the host:port the HTTPS listener binds. The
     * one listener serves both the web UI and the gRPC services
     * (multiplex by Content-Type in the dispatcher).
     */
    ListenAddr string `yaml:"listen_addr"`

    /* Cert is the path to the PEM-encoded server certificate. */
    Cert string `yaml:"cert"`

    /* Key is the path to the PEM-encoded server private key. */
    Key string `yaml:"key"`

    /* CACert is the path to the PEM bundle of CAs that signed
     * the agent/ctl client certificates. mTLS verification on the
     * gRPC path uses this bundle.
     */
    CACert string `yaml:"ca_cert"`

    /* WorkDir is the storage root (jobs/, builds/, ...). */
    WorkDir string `yaml:"work_dir"`

    /* AuthDir is the auth root (keys/, passwords/, matrix.yaml,
     * config.yaml, bootstrap/). Required: the bootstrap and
     * /setup paths need to know where to write the first-admin
     * pubkey and the matrix entry.
     */
    AuthDir string `yaml:"auth_dir"`

    /* ShutdownDrainTimeout is parsed by time.ParseDuration. Empty
     * string means defaultShutdownDrainTimeout (30s). Use
     * DrainTimeout() to get a typed time.Duration.
     */
    ShutdownDrainTimeout string `yaml:"shutdown_drain_timeout,omitempty"`

    /* PluginDir is where the plugin host looks for plugin
     * executables. Empty string means defaultPluginDir.
     */
    PluginDir string `yaml:"plugin_dir,omitempty"`

    /* GCEConfig is the path to an optional GCE provisioner
     * config. Empty string means the GCE provisioner is not
     * wired in.
     */
    GCEConfig string `yaml:"gce_config,omitempty"`

    /* RetentionSweepInterval is how often the per-job
     * retention sweeper runs. Parsed by time.ParseDuration;
     * empty string means defaultRetentionSweepInterval (5m).
     * Setting it to "0" disables the sweeper entirely (useful
     * for tests and for operators who manage retention
     * out-of-band via cron).
     */
    RetentionSweepInterval string `yaml:"retention_sweep_interval,omitempty"`

    /* CredentialMasterSecret is the hex-encoded server-wide
     * master secret used for HKDF-derived AES-256-GCM seal keys
     * in internal/credstore (PLAN.md Phase 18 decisions).
     * Recommended size: 32 bytes (64 hex chars). The wolfci-ctl
     * cred subcommands and the pipeline withCredentials step
     * both read this value via LoadServerConfig.
     */
    CredentialMasterSecret string `yaml:"credential_master_secret,omitempty"`

    /* CredentialDir is the directory that holds the sealed
     * credential files and their index.yaml. Empty value means
     * the credstore is disabled - the wolfci-ctl cred
     * subcommands will refuse to run rather than silently writing
     * sealed files to an unrelated path.
     */
    CredentialDir string `yaml:"credential_dir,omitempty"`
}

/* DefaultServerConfig returns a ServerConfig with the optional
 * fields populated. Required fields stay zero so the caller still
 * has to fill them in (or LoadServerConfig will reject).
 */
func DefaultServerConfig() *ServerConfig {
    return &ServerConfig{
        PluginDir: defaultPluginDir,
    }
}

/* LoadServerConfig reads, parses, and validates a server config. */
func LoadServerConfig(path string) (*ServerConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("server.LoadServerConfig: %w", err)
    }
    cfg := DefaultServerConfig()
    if err := yaml.Unmarshal(data, cfg); err != nil {
        return nil, fmt.Errorf("server.LoadServerConfig: parse %s: %w",
            path, err)
    }
    /* The default applies only when the YAML did not name the
     * key. yaml.Unmarshal sets PluginDir to "" if the file
     * has `plugin_dir: ""` literally, which is unlikely but
     * possible; treat an explicit empty as a request for the
     * default rather than for "no plugin dir".
     */
    if cfg.PluginDir == "" {
        cfg.PluginDir = defaultPluginDir
    }
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("server.LoadServerConfig: %w", err)
    }
    return cfg, nil
}

/* Save writes c to path, creating intermediate directories. */
func (c *ServerConfig) Save(path string) error {
    data, err := yaml.Marshal(c)
    if err != nil {
        return fmt.Errorf("server.ServerConfig.Save: marshal: %w", err)
    }
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return fmt.Errorf("server.ServerConfig.Save: mkdir: %w", err)
    }
    if err := os.WriteFile(path, data, 0o644); err != nil {
        return fmt.Errorf("server.ServerConfig.Save: write: %w", err)
    }
    return nil
}

/* Validate checks that all required fields are populated and that
 * the drain-timeout string parses cleanly.
 */
func (c *ServerConfig) Validate() error {
    if c.ListenAddr == "" {
        return errors.New("listen_addr is required")
    }
    if c.Cert == "" {
        return errors.New("cert is required")
    }
    if c.Key == "" {
        return errors.New("key is required")
    }
    if c.CACert == "" {
        return errors.New("ca_cert is required")
    }
    if c.WorkDir == "" {
        return errors.New("work_dir is required")
    }
    if c.AuthDir == "" {
        return errors.New("auth_dir is required")
    }
    if _, err := c.DrainTimeout(); err != nil {
        return fmt.Errorf("shutdown_drain_timeout: %w", err)
    }
    if _, err := c.RetentionInterval(); err != nil {
        return fmt.Errorf("retention_sweep_interval: %w", err)
    }
    return nil
}

/* DrainTimeout returns the parsed shutdown drain timeout, or the
 * default when the field is empty.
 */
func (c *ServerConfig) DrainTimeout() (time.Duration, error) {
    if c.ShutdownDrainTimeout == "" {
        return defaultShutdownDrainTimeout, nil
    }
    d, err := time.ParseDuration(c.ShutdownDrainTimeout)
    if err != nil {
        return 0, fmt.Errorf("parse %q: %w", c.ShutdownDrainTimeout, err)
    }
    return d, nil
}

/* RetentionInterval returns the parsed retention sweep interval.
 * Empty string -> defaultRetentionSweepInterval. A literal "0"
 * (or any zero duration) returns zero and the caller treats
 * that as "do not run the sweeper".
 */
func (c *ServerConfig) RetentionInterval() (time.Duration, error) {
    if c.RetentionSweepInterval == "" {
        return defaultRetentionSweepInterval, nil
    }
    d, err := time.ParseDuration(c.RetentionSweepInterval)
    if err != nil {
        return 0, fmt.Errorf("parse %q: %w",
            c.RetentionSweepInterval, err)
    }
    return d, nil
}
