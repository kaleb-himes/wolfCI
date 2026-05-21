package auth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/kaleb-himes/wolfCI/internal/auth"
)

// TestKeyStore_VerifySignature is the gating test for PLAN.md
// tasks 3.1 (failing test) and 3.2 (implementation). It writes
// Alice's OpenSSH public key to disk, signs a challenge with
// Alice's private key, and asserts:
//   - Alice's signature verifies as Alice.
//   - A signature made by a different (unregistered) key fails
//     to verify as Alice.
//   - Lookup for an unknown user fails distinctly.
func TestKeyStore_VerifySignature(t *testing.T) {
	dir := t.TempDir()
	ks := auth.NewKeyStore(dir)

	aliceSigner := registerUser(t, dir, "alice")

	_, malloryPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("mallory GenerateKey: %v", err)
	}
	mallorySigner, err := ssh.NewSignerFromKey(malloryPriv)
	if err != nil {
		t.Fatalf("mallory NewSigner: %v", err)
	}

	challenge := []byte("wolfci-challenge-deadbeef")

	aliceSig, err := aliceSigner.Sign(rand.Reader, challenge)
	if err != nil {
		t.Fatalf("alice Sign: %v", err)
	}
	mallorySig, err := mallorySigner.Sign(rand.Reader, challenge)
	if err != nil {
		t.Fatalf("mallory Sign: %v", err)
	}

	if err := ks.VerifySignature("alice", challenge, aliceSig); err != nil {
		t.Fatalf("VerifySignature(alice, alice's sig): %v, want nil", err)
	}

	if err := ks.VerifySignature("alice", challenge, mallorySig); err == nil {
		t.Fatal("VerifySignature(alice, mallory's sig): nil, want error")
	}

	err = ks.VerifySignature("bob", challenge, aliceSig)
	if err == nil {
		t.Fatal("VerifySignature(bob, ...): nil, want error")
	}
}

// TestKeyStore_RejectsPathTraversal protects against a username
// that tries to escape the keys directory.
func TestKeyStore_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	ks := auth.NewKeyStore(dir)
	for _, bad := range []string{"", ".", "..", "../etc/passwd", "a/b", "a\\b"} {
		if err := ks.VerifySignature(bad, []byte("x"), &ssh.Signature{}); err == nil {
			t.Errorf("VerifySignature(%q, ...): nil, want error", bad)
		}
	}
}

// registerUser generates an Ed25519 keypair, writes the
// authorized-keys-format public key to <dir>/<user>.pub, and
// returns the matching ssh.Signer for the test to sign with.
func registerUser(tb testing.TB, dir, user string) ssh.Signer {
	tb.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("%s GenerateKey: %v", user, err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		tb.Fatalf("%s NewSigner: %v", user, err)
	}
	pubBytes := ssh.MarshalAuthorizedKey(signer.PublicKey())
	path := filepath.Join(dir, user+".pub")
	if err := os.WriteFile(path, pubBytes, 0o644); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
	return signer
}
