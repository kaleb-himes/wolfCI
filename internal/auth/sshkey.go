// Package auth implements wolfCI authentication.
//
// SSH public-key auth (this file) is the preferred login
// mechanism. Password auth (password.go) is optional and
// disabled by default. Authorization is layered on top via
// internal/authz.
//
// On-disk layout:
//
//	config-files/auth/keys/<username>.pub   OpenSSH authorized_keys format
//
// One key per file. The username is the basename of the file
// (without ".pub").
//
// Phase 10.8b moved the SSH wire-format parsing to
// github.com/wolfssl/go-wolfssl/wolfssh: this file no longer
// hand-rolls RFC 4253 parsing. The actual signature verify
// dispatch (Ed25519 / ECC / RSA) still routes through
// internal/wolfcrypt because wolfssh's session-oriented C API
// is not the right shape for standalone authorized_keys
// verification.
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
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

	// ErrUnsupportedKeyType is returned when the on-disk key (or
	// the incoming signature) uses an algorithm wolfCI does not
	// support. Today: ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa
	// (with rsa-sha2-256 signatures).
	ErrUnsupportedKeyType = errors.New("auth: unsupported key type")
)

// PublicKey is re-exported from the gowolfssh sub-package so
// callers do not need to import gowolfssh directly.
type PublicKey = gowolfssh.PublicKey

// Algorithm-name constants re-exported from gowolfssh.
const (
	AlgSSHEd25519      = gowolfssh.AlgSSHEd25519
	AlgECDSAP256SHA256 = gowolfssh.AlgECDSAP256SHA256
	AlgSSHRSA          = gowolfssh.AlgSSHRSA
	AlgRSASHA256       = gowolfssh.AlgRSASHA256
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
func (ks *KeyStore) LookupKey(user string) (*PublicKey, error) {
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
	pk, err := gowolfssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, fmt.Errorf("auth.LookupKey: parse %s: %w", path, err)
	}
	return pk, nil
}

// VerifySignature checks that sigSSHWire signs data with the public
// key registered for user. sigSSHWire is an SSH wire-format
// signature (RFC 4253 Section 6.6: string algo || string blob).
//
// Returns nil on success, ErrUnknownUser if the user has no key on
// file, ErrInvalidSignature if the signature does not verify (for
// any reason: wrong key, wrong algo, malformed blob), or
// ErrInvalidUsername if the username would escape the keys
// directory.
func (ks *KeyStore) VerifySignature(user string, data, sigSSHWire []byte) error {
	if err := validateUsername(user); err != nil {
		return err
	}
	pk, err := ks.LookupKey(user)
	if err != nil {
		return err
	}

	sigAlgo, sigBlob, err := gowolfssh.ParseSSHSignature(sigSSHWire)
	if err != nil {
		return ErrInvalidSignature
	}

	switch sigAlgo {
	case AlgSSHEd25519:
		if pk.Algorithm != AlgSSHEd25519 {
			return ErrInvalidSignature
		}
		ok, err := wolfcrypt.Ed25519Verify(pk.Ed25519, data, sigBlob)
		if err != nil || !ok {
			return ErrInvalidSignature
		}
	case AlgECDSAP256SHA256:
		if pk.Algorithm != AlgECDSAP256SHA256 {
			return ErrInvalidSignature
		}
		// The SSH ECDSA signature blob is itself wire-encoded:
		// "string r || string s" (each an SSH mpint).
		r, rest, err := gowolfssh.ReadSSHMpint(sigBlob)
		if err != nil {
			return ErrInvalidSignature
		}
		s, rest, err := gowolfssh.ReadSSHMpint(rest)
		if err != nil || len(rest) != 0 {
			return ErrInvalidSignature
		}
		hash, err := wolfcrypt.SHA256(data)
		if err != nil {
			return ErrInvalidSignature
		}
		ok, err := wolfcrypt.ECCVerifyP256(pk.ECDSAPoint, hash,
			gowolfssh.EncodeECDSASignatureDER(r, s))
		if err != nil || !ok {
			return ErrInvalidSignature
		}
	case AlgRSASHA256:
		if pk.Algorithm != AlgSSHRSA {
			return ErrInvalidSignature
		}
		ok, err := wolfcrypt.RSAVerifyPKCS1v15SHA256(pk.RSAModulus,
			pk.RSAExponent, data, sigBlob)
		if err != nil || !ok {
			return ErrInvalidSignature
		}
	default:
		// "ssh-rsa" sig (SHA-1) is intentionally refused.
		// OpenSSH 8.8+ has done the same since 2021; modern
		// clients negotiate rsa-sha2-256 against the same
		// on-disk ssh-rsa key.
		return ErrUnsupportedKeyType
	}
	return nil
}

// EncodeSSHAuthorizedKey renders a public key as one line in the
// OpenSSH authorized_keys format. Re-exported from gowolfssh for
// callers (tests, the eventual Phase 11 first-admin bootstrap)
// that need to synthesize key files without importing gowolfssh
// directly.
func EncodeSSHAuthorizedKey(algo string, keyBlob []byte, comment string) []byte {
	return gowolfssh.EncodeSSHAuthorizedKey(algo, keyBlob, comment)
}

// EncodeSSHEd25519AuthorizedKey is the common case: build an
// authorized_keys line for a raw 32-byte Ed25519 public key.
func EncodeSSHEd25519AuthorizedKey(pubKey []byte, comment string) []byte {
	return gowolfssh.EncodeSSHEd25519AuthorizedKey(pubKey, comment)
}

// EncodeSSHEd25519Signature wraps a 64-byte Ed25519 signature in
// the SSH wire envelope ("ssh-ed25519" + sig) that
// VerifySignature expects.
func EncodeSSHEd25519Signature(sig []byte) []byte {
	return gowolfssh.EncodeSSHEd25519Signature(sig)
}

// validateUsername rejects names that would escape the keys
// directory or denote a hidden/reserved entry.
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
