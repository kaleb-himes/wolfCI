package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// Session is the on-disk shape of one logged-in user's session
// at config-files/auth/sessions/<token>.yaml. Tokens are 32
// random bytes hex-encoded.
type Session struct {
	Token     string    `yaml:"token"`
	Username  string    `yaml:"username"`
	CreatedAt time.Time `yaml:"created_at"`
	ExpiresAt time.Time `yaml:"expires_at"`
}

// SessionStore persists Sessions as YAML files under root.
type SessionStore struct {
	root string
	ttl  time.Duration
}

// NewSessionStore returns a SessionStore that mints sessions
// living for ttl. The root directory is created on demand.
func NewSessionStore(root string, ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &SessionStore{root: root, ttl: ttl}
}

// Create mints a new session for username and writes it to
// disk. The Token field is filled in by the store; the rest
// is metadata callers can inspect.
func (s *SessionStore) Create(username string) (*Session, error) {
	if username == "" {
		return nil, errors.New("server.SessionStore.Create: empty username")
	}
	token, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("server.SessionStore.Create: randomToken: %w", err)
	}
	now := time.Now().UTC()
	sess := &Session{
		Token:     token,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	if err := s.write(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Lookup returns the Session for the given token, or an error
// if the token is unknown or expired.
func (s *SessionStore) Lookup(token string) (*Session, error) {
	if !validToken(token) {
		return nil, errors.New("server.SessionStore.Lookup: invalid token")
	}
	path := s.path(token)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("server.SessionStore.Lookup: unknown token")
		}
		return nil, fmt.Errorf("server.SessionStore.Lookup: read: %w", err)
	}
	sess := &Session{}
	if err := yaml.Unmarshal(data, sess); err != nil {
		return nil, fmt.Errorf("server.SessionStore.Lookup: parse: %w", err)
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = os.Remove(path)
		return nil, errors.New("server.SessionStore.Lookup: token expired")
	}
	return sess, nil
}

// Delete removes the session file for token. Idempotent.
func (s *SessionStore) Delete(token string) error {
	if !validToken(token) {
		return errors.New("server.SessionStore.Delete: invalid token")
	}
	err := os.Remove(s.path(token))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("server.SessionStore.Delete: %w", err)
	}
	return nil
}

func (s *SessionStore) write(sess *Session) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("server.SessionStore.write: mkdir: %w", err)
	}
	data, err := yaml.Marshal(sess)
	if err != nil {
		return fmt.Errorf("server.SessionStore.write: marshal: %w", err)
	}
	if err := os.WriteFile(s.path(sess.Token), data, 0o600); err != nil {
		return fmt.Errorf("server.SessionStore.write: write: %w", err)
	}
	return nil
}

func (s *SessionStore) path(token string) string {
	return filepath.Join(s.root, token+".yaml")
}

func randomToken() (string, error) {
	buf, err := wolfcrypt.RandBytes(32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// validToken guards against filesystem traversal via the token
// path. Tokens are 64 hex chars; anything else is rejected
// before any os.* call.
func validToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	if strings.ContainsAny(token, "/\\.\x00") {
		return false
	}
	return true
}
