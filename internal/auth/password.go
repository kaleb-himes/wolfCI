package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// Sentinel errors specific to password auth.
var (
	// ErrPasswordAuthDisabled is returned by PasswordStore.VerifyPassword
	// when Config.PasswordEnabled is false. The hash on disk (if any)
	// is intentionally not consulted.
	ErrPasswordAuthDisabled = errors.New("auth: password auth disabled")

	// ErrInvalidPassword is returned when the user exists but the
	// supplied password does not match the stored bcrypt hash.
	ErrInvalidPassword = errors.New("auth: invalid password")
)

// Config holds tunable auth settings. The canonical on-disk path is
// config-files/auth/config.yaml, but the package itself accepts any
// path so tests can use t.TempDir.
//
// Defaults follow the project's locked-in Phase 3 decisions:
// password auth disabled, bcrypt cost 12.
type Config struct {
	// PasswordEnabled gates username + password login at runtime.
	// SSH-key auth is unaffected.
	PasswordEnabled bool `yaml:"password_enabled"`

	// BcryptCost is the bcrypt work factor used when SetPassword
	// hashes a new password. Must be in [bcrypt.MinCost, bcrypt.MaxCost].
	BcryptCost int `yaml:"bcrypt_cost"`
}

// DefaultConfig returns a Config with the wolfCI defaults.
func DefaultConfig() *Config {
	return &Config{
		PasswordEnabled: false,
		BcryptCost:      12,
	}
}

// LoadConfig reads an auth config from disk and validates it.
// Missing fields fall back to the DefaultConfig values.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth.LoadConfig: %w", err)
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("auth.LoadConfig: parse %s: %w", path, err)
	}
	if cfg.BcryptCost < bcrypt.MinCost || cfg.BcryptCost > bcrypt.MaxCost {
		return nil, fmt.Errorf("auth.LoadConfig: bcrypt_cost %d out of range [%d, %d]",
			cfg.BcryptCost, bcrypt.MinCost, bcrypt.MaxCost)
	}
	return cfg, nil
}

// Save writes c to path, creating intermediate directories as needed.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("auth.Config.Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("auth.Config.Save: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("auth.Config.Save: write: %w", err)
	}
	return nil
}

// PasswordStore manages per-user bcrypt password hashes on disk.
// Hashes live at <root>/<username>.bcrypt with mode 0600.
type PasswordStore struct {
	root string
	cfg  *Config
}

// NewPasswordStore returns a PasswordStore rooted at the given
// directory using cfg for runtime settings (PasswordEnabled,
// BcryptCost). cfg must not be nil; pass DefaultConfig() if you
// have no other source.
func NewPasswordStore(root string, cfg *Config) *PasswordStore {
	return &PasswordStore{root: root, cfg: cfg}
}

// SetPassword stores a bcrypt hash of password for user. Works
// regardless of cfg.PasswordEnabled so an administrator can
// provision credentials before enabling the toggle.
func (ps *PasswordStore) SetPassword(user, password string) error {
	if err := validateUsername(user); err != nil {
		return err
	}
	if password == "" {
		return errors.New("auth.SetPassword: empty password")
	}
	cost := ps.cfg.BcryptCost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return fmt.Errorf("auth.SetPassword: bcrypt: %w", err)
	}
	if err := os.MkdirAll(ps.root, 0o700); err != nil {
		return fmt.Errorf("auth.SetPassword: mkdir: %w", err)
	}
	if err := os.WriteFile(ps.hashPath(user), hash, 0o600); err != nil {
		return fmt.Errorf("auth.SetPassword: write: %w", err)
	}
	return nil
}

// VerifyPassword returns nil if password matches the stored hash
// for user. If cfg.PasswordEnabled is false, the function returns
// ErrPasswordAuthDisabled without touching disk.
func (ps *PasswordStore) VerifyPassword(user, password string) error {
	if !ps.cfg.PasswordEnabled {
		return ErrPasswordAuthDisabled
	}
	if err := validateUsername(user); err != nil {
		return err
	}
	hash, err := os.ReadFile(ps.hashPath(user))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrUnknownUser
		}
		return fmt.Errorf("auth.VerifyPassword: read: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return ErrInvalidPassword
	}
	return nil
}

func (ps *PasswordStore) hashPath(user string) string {
	return filepath.Join(ps.root, user+".bcrypt")
}
