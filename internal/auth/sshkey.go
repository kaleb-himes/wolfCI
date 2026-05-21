// Package auth implements wolfCI authentication.
//
// SSH public-key auth (this file) is the preferred login
// mechanism. Password auth (Phase 3.3 onward) is optional and
// disabled by default. Authorization is layered on top via
// internal/authz (Phase 3.5+).
//
// On-disk layout:
//
//	config-files/auth/keys/<username>.pub   OpenSSH authorized_keys format
//
// One key per file. The username is the basename of the file
// (without ".pub").
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Sentinel errors. Callers should compare via errors.Is.
var (
	// ErrUnknownUser is returned when no key file exists for the
	// requested user.
	ErrUnknownUser = errors.New("auth: unknown user")

	// ErrInvalidSignature is returned when a key file exists for
	// the user but the signature does not verify against it.
	ErrInvalidSignature = errors.New("auth: invalid signature")

	// ErrInvalidUsername is returned when the supplied username
	// would escape the keys directory or is otherwise unsafe.
	ErrInvalidUsername = errors.New("auth: invalid username")
)

// KeyStore loads users' OpenSSH public keys from a directory.
type KeyStore struct {
	root string
}

// NewKeyStore returns a KeyStore rooted at the given directory.
// The directory does not need to exist; LookupKey will return
// ErrUnknownUser for any user when it is missing.
func NewKeyStore(root string) *KeyStore { return &KeyStore{root: root} }

// LookupKey returns the public key registered for user, or
// ErrUnknownUser if no key file exists.
func (ks *KeyStore) LookupKey(user string) (ssh.PublicKey, error) {
	if err := validateUsername(user); err != nil {
		return nil, err
	}
	path := filepath.Join(ks.root, user+".pub")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUnknownUser
		}
		return nil, fmt.Errorf("auth.LookupKey: read %s: %w", path, err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, fmt.Errorf("auth.LookupKey: parse %s: %w", path, err)
	}
	return pub, nil
}

// VerifySignature checks that sig signs data with the public key
// registered for user. Returns nil on success, ErrUnknownUser if
// the user has no key on file, ErrInvalidSignature if the
// signature does not verify, or ErrInvalidUsername if the
// username would escape the keys directory.
func (ks *KeyStore) VerifySignature(user string, data []byte, sig *ssh.Signature) error {
	if err := validateUsername(user); err != nil {
		return err
	}
	if sig == nil {
		return errors.New("auth.VerifySignature: nil signature")
	}
	pub, err := ks.LookupKey(user)
	if err != nil {
		return err
	}
	if err := pub.Verify(data, sig); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

// validateUsername rejects names that would escape the keys
// directory or denote a hidden/reserved entry. wolfCI usernames
// are application identifiers, not POSIX account names, so the
// rule set is intentionally conservative: no path separators, no
// nul bytes, no "." or ".." sole names, no leading dot, no empty
// string.
func validateUsername(user string) error {
	if user == "" {
		return fmt.Errorf("%w: empty", ErrInvalidUsername)
	}
	if user == "." || user == ".." {
		return fmt.Errorf("%w: reserved name %q", ErrInvalidUsername, user)
	}
	if strings.HasPrefix(user, ".") {
		return fmt.Errorf("%w: leading dot in %q", ErrInvalidUsername, user)
	}
	if strings.ContainsAny(user, "/\\\x00") {
		return fmt.Errorf("%w: path separator in %q", ErrInvalidUsername, user)
	}
	return nil
}
