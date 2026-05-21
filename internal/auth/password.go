package auth

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// Sentinel errors specific to password auth.
var (
	// ErrPasswordAuthDisabled is returned by PasswordStore.VerifyPassword
	// when Config.PasswordEnabled is false. The hash on disk (if any)
	// is intentionally not consulted.
	ErrPasswordAuthDisabled = errors.New("auth: password auth disabled")

	// ErrInvalidPassword is returned when the user exists but the
	// supplied password does not match the stored hash.
	ErrInvalidPassword = errors.New("auth: invalid password")

	// ErrInvalidPasswordFile is returned when the on-disk hash file
	// fails its structural checks (wrong sentinel, malformed lines,
	// bad hex). Distinct from ErrInvalidPassword so the caller can
	// tell "your password is wrong" from "your hash file is
	// corrupt".
	ErrInvalidPasswordFile = errors.New("auth: invalid password file")
)

// pbkdf2SentinelV1 is the first line of every password-hash file
// this package writes. Any file with a different sentinel is
// rejected outright (never silently misread as the wrong KDF).
const pbkdf2SentinelV1 = "wolfci-pbkdf2-v1"

// pbkdf2DerivedKeyLen is the length of the PBKDF2 output we store.
// 32 bytes is one SHA-256 block; further blocks would just add
// iteration cost without raising security in any meaningful way.
const pbkdf2DerivedKeyLen = 32

// Acceptable bounds on the PBKDF2 iteration count loaded from
// config. Too few iterations defeats the KDF; too many is a DoS
// vector against legitimate logins.
const (
	pbkdf2MinIterations = 100
	pbkdf2MaxIterations = 10_000_000
)

// Config holds tunable auth settings. The canonical on-disk path
// is config-files/auth/config.yaml.
//
// Phase 10 swapped the password KDF from bcrypt to wolfCrypt
// PBKDF2-HMAC-SHA-256 so the wolfCI source tree depends on
// exactly one crypto library (wolfSSL/wolfCrypt). The old
// bcrypt_cost field is gone; iteration count and salt length now
// live under pbkdf2_iterations and pbkdf2_salt_bytes.
type Config struct {
	// PasswordEnabled gates username + password login at runtime.
	// SSH-key auth is unaffected.
	PasswordEnabled bool `yaml:"password_enabled"`

	// PBKDF2Iterations is the iteration count passed to wolfCrypt
	// PBKDF2-HMAC-SHA-256 when SetPassword hashes a new password.
	// Defaults to 600000 (OWASP 2023 guidance).
	PBKDF2Iterations int `yaml:"pbkdf2_iterations"`

	// PBKDF2SaltBytes is the length of the random salt drawn from
	// wolfCrypt's CSPRNG for each new password. Defaults to 16.
	PBKDF2SaltBytes int `yaml:"pbkdf2_salt_bytes"`
}

// DefaultConfig returns a Config with the wolfCI defaults.
func DefaultConfig() *Config {
	return &Config{
		PasswordEnabled:  false,
		PBKDF2Iterations: 600_000,
		PBKDF2SaltBytes:  16,
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
	if cfg.PBKDF2Iterations < pbkdf2MinIterations || cfg.PBKDF2Iterations > pbkdf2MaxIterations {
		return nil, fmt.Errorf("auth.LoadConfig: pbkdf2_iterations %d out of range [%d, %d]",
			cfg.PBKDF2Iterations, pbkdf2MinIterations, pbkdf2MaxIterations)
	}
	if cfg.PBKDF2SaltBytes < 8 || cfg.PBKDF2SaltBytes > 64 {
		return nil, fmt.Errorf("auth.LoadConfig: pbkdf2_salt_bytes %d out of range [8, 64]",
			cfg.PBKDF2SaltBytes)
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

// PasswordStore manages per-user password hashes on disk. Hashes
// live at <root>/<username>.pbkdf2 with mode 0600 and the file
// layout described on pbkdf2SentinelV1.
type PasswordStore struct {
	root string
	cfg  *Config
}

// NewPasswordStore returns a PasswordStore rooted at the given
// directory using cfg for runtime settings.
func NewPasswordStore(root string, cfg *Config) *PasswordStore {
	return &PasswordStore{root: root, cfg: cfg}
}

// SetPassword derives a fresh PBKDF2-HMAC-SHA-256 hash of password
// for user and writes it to disk. Works regardless of
// cfg.PasswordEnabled so an administrator can provision
// credentials before enabling the toggle.
func (ps *PasswordStore) SetPassword(user, password string) error {
	if err := validateUsername(user); err != nil {
		return err
	}
	if password == "" {
		return errors.New("auth.SetPassword: empty password")
	}

	iterations := ps.cfg.PBKDF2Iterations
	if iterations <= 0 {
		iterations = 600_000
	}
	saltLen := ps.cfg.PBKDF2SaltBytes
	if saltLen <= 0 {
		saltLen = 16
	}

	salt, err := wolfcrypt.RandBytes(saltLen)
	if err != nil {
		return fmt.Errorf("auth.SetPassword: salt: %w", err)
	}
	derived, err := wolfcrypt.PBKDF2HMACSHA256([]byte(password), salt, iterations, pbkdf2DerivedKeyLen)
	if err != nil {
		return fmt.Errorf("auth.SetPassword: pbkdf2: %w", err)
	}

	blob := encodePBKDF2File(iterations, salt, derived)
	if err := os.MkdirAll(ps.root, 0o700); err != nil {
		return fmt.Errorf("auth.SetPassword: mkdir: %w", err)
	}
	if err := os.WriteFile(ps.hashPath(user), blob, 0o600); err != nil {
		return fmt.Errorf("auth.SetPassword: write: %w", err)
	}
	return nil
}

// VerifyPassword returns nil if password matches the stored hash
// for user. If cfg.PasswordEnabled is false, the function returns
// ErrPasswordAuthDisabled without touching disk. The compare runs
// through the HMAC-both-sides pattern from internal/wolfcrypt:
// any timing leak in the final byte loop only reveals HMAC
// outputs over a fresh random key, which the attacker cannot
// predict.
func (ps *PasswordStore) VerifyPassword(user, password string) error {
	if !ps.cfg.PasswordEnabled {
		return ErrPasswordAuthDisabled
	}
	if err := validateUsername(user); err != nil {
		return err
	}
	data, err := os.ReadFile(ps.hashPath(user))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrUnknownUser
		}
		return fmt.Errorf("auth.VerifyPassword: read: %w", err)
	}
	iterations, salt, stored, err := decodePBKDF2File(data)
	if err != nil {
		return err
	}
	candidate, err := wolfcrypt.PBKDF2HMACSHA256([]byte(password), salt, iterations, len(stored))
	if err != nil {
		return fmt.Errorf("auth.VerifyPassword: pbkdf2: %w", err)
	}
	ok, err := constantTimeEqual(stored, candidate)
	if err != nil {
		return fmt.Errorf("auth.VerifyPassword: compare: %w", err)
	}
	if !ok {
		return ErrInvalidPassword
	}
	return nil
}

func (ps *PasswordStore) hashPath(user string) string {
	return filepath.Join(ps.root, user+".pbkdf2")
}

// encodePBKDF2File renders the on-disk hash format:
//
//	wolfci-pbkdf2-v1
//	iterations:<decimal>
//	salt:<hex>
//	key:<hex>
//
// One field per line keeps the format trivially editable / grep-
// able and lets a future version bump add fields without touching
// the v1 reader.
func encodePBKDF2File(iterations int, salt, key []byte) []byte {
	var b strings.Builder
	b.WriteString(pbkdf2SentinelV1)
	b.WriteByte('\n')
	b.WriteString("iterations:")
	b.WriteString(strconv.Itoa(iterations))
	b.WriteByte('\n')
	b.WriteString("salt:")
	b.WriteString(hex.EncodeToString(salt))
	b.WriteByte('\n')
	b.WriteString("key:")
	b.WriteString(hex.EncodeToString(key))
	b.WriteByte('\n')
	return []byte(b.String())
}

func decodePBKDF2File(data []byte) (iterations int, salt, key []byte, err error) {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 4 || lines[0] != pbkdf2SentinelV1 {
		return 0, nil, nil, fmt.Errorf("%w: missing or wrong sentinel", ErrInvalidPasswordFile)
	}
	var iterStr, saltHex, keyHex string
	for _, ln := range lines[1:] {
		kv := strings.SplitN(ln, ":", 2)
		if len(kv) != 2 {
			return 0, nil, nil, fmt.Errorf("%w: malformed line %q", ErrInvalidPasswordFile, ln)
		}
		switch kv[0] {
		case "iterations":
			iterStr = kv[1]
		case "salt":
			saltHex = kv[1]
		case "key":
			keyHex = kv[1]
		}
	}
	if iterStr == "" || saltHex == "" || keyHex == "" {
		return 0, nil, nil, fmt.Errorf("%w: missing field", ErrInvalidPasswordFile)
	}
	iterations, err = strconv.Atoi(iterStr)
	if err != nil || iterations < pbkdf2MinIterations || iterations > pbkdf2MaxIterations {
		return 0, nil, nil, fmt.Errorf("%w: iterations %q", ErrInvalidPasswordFile, iterStr)
	}
	salt, err = hex.DecodeString(saltHex)
	if err != nil || len(salt) == 0 {
		return 0, nil, nil, fmt.Errorf("%w: salt hex", ErrInvalidPasswordFile)
	}
	key, err = hex.DecodeString(keyHex)
	if err != nil || len(key) == 0 {
		return 0, nil, nil, fmt.Errorf("%w: key hex", ErrInvalidPasswordFile)
	}
	return iterations, salt, key, nil
}

// constantTimeEqual is the HMAC-both-sides constant-time hash
// compare from the Phase 10 wolfCrypt-only contract. A fresh
// random key is HMAC'd over both candidate and stored hashes;
// the final byte compare is over HMAC outputs the attacker
// cannot predict, so any timing leak in this loop is harmless.
func constantTimeEqual(a, b []byte) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	key, err := wolfcrypt.RandBytes(32)
	if err != nil {
		return false, err
	}
	ha, err := wolfcrypt.HMACSHA256(key, a)
	if err != nil {
		return false, err
	}
	hb, err := wolfcrypt.HMACSHA256(key, b)
	if err != nil {
		return false, err
	}
	var diff byte
	for i := range ha {
		diff |= ha[i] ^ hb[i]
	}
	return diff == 0, nil
}
