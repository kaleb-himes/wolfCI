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
// Phase 10.3 dropped the golang.org/x/crypto/ssh dependency:
// the wire-format parser below is hand-rolled per RFC 4253
// Section 6.6, and every cryptographic verify call goes through
// internal/wolfcrypt.
package auth

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// SSH algorithm names (RFC 4253 Section 6.6 + RFC 8332).
const (
	AlgSSHEd25519       = "ssh-ed25519"
	AlgECDSAP256SHA256  = "ecdsa-sha2-nistp256"
	AlgSSHRSA           = "ssh-rsa"      // signature algo: SHA-1 (legacy, refused below)
	AlgRSASHA256        = "rsa-sha2-256" // signature algo: SHA-256 (the one we accept)
)

// PublicKey is a parsed SSH public key. Exactly one of the
// algorithm-specific field groups is populated; check Algorithm
// before reading the others.
type PublicKey struct {
	Algorithm string

	// AlgSSHEd25519.
	Ed25519 []byte // 32 bytes

	// AlgECDSAP256SHA256.
	ECDSAPoint []byte // SEC1 uncompressed, 65 bytes (0x04 || X || Y)

	// AlgSSHRSA.
	RSAModulus  []byte
	RSAExponent []byte
}

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
	pk, err := parseAuthorizedKey(data)
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

	sigAlgo, sigBlob, err := parseSSHSignature(sigSSHWire)
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
		r, rest, err := readSSHMpint(sigBlob)
		if err != nil {
			return ErrInvalidSignature
		}
		s, rest, err := readSSHMpint(rest)
		if err != nil || len(rest) != 0 {
			return ErrInvalidSignature
		}
		hash, err := wolfcrypt.SHA256(data)
		if err != nil {
			return ErrInvalidSignature
		}
		ok, err := wolfcrypt.ECCVerifyP256(pk.ECDSAPoint, hash, encodeECDSASignatureDER(r, s))
		if err != nil || !ok {
			return ErrInvalidSignature
		}
	case AlgRSASHA256:
		if pk.Algorithm != AlgSSHRSA {
			return ErrInvalidSignature
		}
		ok, err := wolfcrypt.RSAVerifyPKCS1v15SHA256(pk.RSAModulus, pk.RSAExponent, data, sigBlob)
		if err != nil || !ok {
			return ErrInvalidSignature
		}
	default:
		// "ssh-rsa" sig (SHA-1) is intentionally refused. OpenSSH
		// 8.8+ has done the same since 2021; clients negotiating a
		// modern algorithm send rsa-sha2-256 or rsa-sha2-512
		// against the same on-disk ssh-rsa key.
		return ErrUnsupportedKeyType
	}
	return nil
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

// -- SSH wire format helpers (no crypto) ------------------------

// parseAuthorizedKey parses one line of the OpenSSH authorized_keys
// format: "<algo> <base64-blob> [comment]\n".
func parseAuthorizedKey(data []byte) (*PublicKey, error) {
	// Take the first non-blank, non-comment line.
	var line string
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		line = trimmed
		break
	}
	if line == "" {
		return nil, errors.New("authorized_keys: no key data")
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return nil, errors.New("authorized_keys: missing key blob")
	}
	algo := parts[0]
	blob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("authorized_keys: base64 decode: %w", err)
	}
	return parseSSHWirePublicKey(algo, blob)
}

// parseSSHWirePublicKey decodes the per-algorithm blob inside an
// SSH wire-format public key. expectedAlgo must match the
// algorithm name encoded inside the blob; this catches a
// mismatch between the authorized_keys prefix and the embedded
// algorithm string.
func parseSSHWirePublicKey(expectedAlgo string, blob []byte) (*PublicKey, error) {
	actual, rest, err := readSSHString(blob)
	if err != nil {
		return nil, fmt.Errorf("ssh public key: %w", err)
	}
	if string(actual) != expectedAlgo {
		return nil, fmt.Errorf("ssh public key: algo mismatch (file says %q, blob says %q)",
			expectedAlgo, string(actual))
	}
	switch expectedAlgo {
	case AlgSSHEd25519:
		key, rest, err := readSSHString(rest)
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("ssh-ed25519: key must be 32 bytes, got %d", len(key))
		}
		if len(rest) != 0 {
			return nil, errors.New("ssh-ed25519: trailing bytes")
		}
		return &PublicKey{Algorithm: AlgSSHEd25519, Ed25519: append([]byte{}, key...)}, nil
	case AlgECDSAP256SHA256:
		curve, rest, err := readSSHString(rest)
		if err != nil {
			return nil, err
		}
		if string(curve) != "nistp256" {
			return nil, fmt.Errorf("ecdsa-sha2-nistp256: curve identifier = %q, want nistp256",
				string(curve))
		}
		point, rest, err := readSSHString(rest)
		if err != nil {
			return nil, err
		}
		if len(point) != 65 || point[0] != 0x04 {
			return nil, errors.New("ecdsa-sha2-nistp256: expected 65-byte uncompressed point")
		}
		if len(rest) != 0 {
			return nil, errors.New("ecdsa-sha2-nistp256: trailing bytes")
		}
		return &PublicKey{Algorithm: AlgECDSAP256SHA256, ECDSAPoint: append([]byte{}, point...)}, nil
	case AlgSSHRSA:
		e, rest, err := readSSHMpint(rest)
		if err != nil {
			return nil, err
		}
		n, rest, err := readSSHMpint(rest)
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, errors.New("ssh-rsa: trailing bytes")
		}
		return &PublicKey{
			Algorithm:   AlgSSHRSA,
			RSAModulus:  n,
			RSAExponent: e,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedKeyType, expectedAlgo)
	}
}

// parseSSHSignature decodes the SSH wire-format signature envelope:
//
//	string  signature-algorithm-name
//	string  signature-blob
func parseSSHSignature(b []byte) (algo string, sigBlob []byte, err error) {
	algoBytes, rest, err := readSSHString(b)
	if err != nil {
		return "", nil, err
	}
	blob, rest, err := readSSHString(rest)
	if err != nil {
		return "", nil, err
	}
	if len(rest) != 0 {
		return "", nil, errors.New("ssh signature: trailing bytes")
	}
	return string(algoBytes), append([]byte{}, blob...), nil
}

// readSSHString reads a length-prefixed string (uint32 BE length +
// content bytes).
func readSSHString(b []byte) (content, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, errors.New("ssh wire: short header")
	}
	n := binary.BigEndian.Uint32(b[:4])
	if 4+int(n) > len(b) {
		return nil, nil, errors.New("ssh wire: truncated string")
	}
	return b[4 : 4+int(n)], b[4+int(n):], nil
}

// readSSHMpint reads an SSH mpint: a length-prefixed two's-
// complement big integer. Strips any leading 0x00 sign byte so
// wolfCrypt's RSA / ECDSA decoders receive unsigned big-endian.
func readSSHMpint(b []byte) (intBytes, rest []byte, err error) {
	s, rest, err := readSSHString(b)
	if err != nil {
		return nil, nil, err
	}
	for len(s) > 0 && s[0] == 0x00 {
		s = s[1:]
	}
	return append([]byte{}, s...), rest, nil
}

// encodeSSHString writes a length-prefixed string to a byte slice.
func encodeSSHString(s []byte) []byte {
	out := make([]byte, 4+len(s))
	binary.BigEndian.PutUint32(out[:4], uint32(len(s)))
	copy(out[4:], s)
	return out
}

// encodeECDSASignatureDER builds the ASN.1 DER SEQUENCE { r, s }
// that wolfCrypt's wc_ecc_verify_hash expects, from raw big-endian
// r and s integers (as returned by readSSHMpint). Byte assembly
// only - no crypto.
func encodeECDSASignatureDER(r, s []byte) []byte {
	encodeInt := func(b []byte) []byte {
		for len(b) > 1 && b[0] == 0x00 {
			b = b[1:]
		}
		if len(b) == 0 {
			b = []byte{0}
		}
		if b[0]&0x80 != 0 {
			b = append([]byte{0x00}, b...)
		}
		out := []byte{0x02, byte(len(b))}
		return append(out, b...)
	}
	rEnc := encodeInt(r)
	sEnc := encodeInt(s)
	body := append(rEnc, sEnc...)
	return append([]byte{0x30, byte(len(body))}, body...)
}

// EncodeSSHAuthorizedKey renders a public key as one line in the
// OpenSSH authorized_keys format: "<algo> <base64-blob> [comment]".
// Exported so test fixtures (and Phase 11 first-admin bootstrap)
// can synthesize key files without re-implementing the wire
// encoder.
func EncodeSSHAuthorizedKey(algo string, keyBlob []byte, comment string) []byte {
	wire := append(encodeSSHString([]byte(algo)), encodeSSHString(keyBlob)...)
	b64 := base64.StdEncoding.EncodeToString(wire)
	line := algo + " " + b64
	if comment != "" {
		line += " " + comment
	}
	line += "\n"
	return []byte(line)
}

// EncodeSSHEd25519AuthorizedKey is the common case: build an
// authorized_keys line for a raw 32-byte Ed25519 public key.
func EncodeSSHEd25519AuthorizedKey(pubKey []byte, comment string) []byte {
	return EncodeSSHAuthorizedKey(AlgSSHEd25519, pubKey, comment)
}

// EncodeSSHEd25519Signature wraps a 64-byte Ed25519 signature in
// the SSH wire envelope ("ssh-ed25519" + sig) that
// VerifySignature expects.
func EncodeSSHEd25519Signature(sig []byte) []byte {
	return append(encodeSSHString([]byte(AlgSSHEd25519)), encodeSSHString(sig)...)
}
