package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/auth"
	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// TestKeyStore_VerifySignature: Alice's signature verifies as
// Alice; a signature made by a different key does not; an unknown
// user is distinct. Same contract as the original
// x/crypto/ssh-backed test; Phase 10.3 swapped the entire stack
// to internal/wolfcrypt + a hand-rolled SSH wire parser.
func TestKeyStore_VerifySignature(t *testing.T) {
	dir := t.TempDir()
	ks := auth.NewKeyStore(dir)

	alicePub, alicePriv := registerEd25519User(t, dir, "alice")
	_, malloryPriv := newEd25519Keypair(t)

	challenge := []byte("wolfci-challenge-deadbeef")

	aliceSig, err := wolfcrypt.Ed25519Sign(alicePriv, challenge)
	if err != nil {
		t.Fatalf("alice Sign: %v", err)
	}
	mallorySig, err := wolfcrypt.Ed25519Sign(malloryPriv, challenge)
	if err != nil {
		t.Fatalf("mallory Sign: %v", err)
	}

	aliceSigSSH := auth.EncodeSSHEd25519Signature(aliceSig)
	mallorySigSSH := auth.EncodeSSHEd25519Signature(mallorySig)

	if err := ks.VerifySignature("alice", challenge, aliceSigSSH); err != nil {
		t.Fatalf("VerifySignature(alice, alice's sig): %v, want nil", err)
	}
	if err := ks.VerifySignature("alice", challenge, mallorySigSSH); err == nil {
		t.Fatal("VerifySignature(alice, mallory's sig): nil, want error")
	}
	if err := ks.VerifySignature("bob", challenge, aliceSigSSH); err == nil {
		t.Fatal("VerifySignature(bob, ...): nil, want error")
	}

	// Tampered message should reject too.
	tampered := append([]byte{}, challenge...)
	tampered[0] ^= 0x01
	if err := ks.VerifySignature("alice", tampered, aliceSigSSH); err == nil {
		t.Fatal("VerifySignature on tampered message: nil, want error")
	}

	_ = alicePub
}

// TestKeyStore_RejectsPathTraversal protects against a username
// that tries to escape the keys directory.
func TestKeyStore_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	ks := auth.NewKeyStore(dir)
	bogusSig := auth.EncodeSSHEd25519Signature(make([]byte, 64))
	for _, bad := range []string{"", ".", "..", "../etc/passwd", "a/b", "a\\b"} {
		if err := ks.VerifySignature(bad, []byte("x"), bogusSig); err == nil {
			t.Errorf("VerifySignature(%q, ...): nil, want error", bad)
		}
	}
}

// TestKeyStore_RejectsUnknownAlgo refuses a signature whose
// algorithm string is not one of the supported set.
func TestKeyStore_RejectsUnknownAlgo(t *testing.T) {
	dir := t.TempDir()
	ks := auth.NewKeyStore(dir)
	registerEd25519User(t, dir, "alice")

	// Build a signature envelope with a bogus algorithm name.
	bogus := auth.EncodeSSHAuthorizedKey("nope-not-a-real-algo", make([]byte, 64), "")
	// EncodeSSHAuthorizedKey produces the authorized_keys text form,
	// not the raw wire signature envelope - so just hand-roll a wire
	// blob with the bogus algo. Easier path: take a real ed25519 sig
	// envelope and overwrite the algo prefix.
	_ = bogus
	// Construct {string "nope" || string <64 zero bytes>} manually.
	algo := []byte("nope")
	blob := make([]byte, 64)
	sig := append(
		append([]byte{0, 0, 0, byte(len(algo))}, algo...),
		append([]byte{0, 0, 0, byte(len(blob))}, blob...)...,
	)
	if err := ks.VerifySignature("alice", []byte("x"), sig); err == nil {
		t.Fatal("VerifySignature accepted unknown algo")
	}
}

// registerEd25519User generates an Ed25519 keypair via wolfcrypt,
// writes alice.pub in the OpenSSH authorized_keys format, and
// returns the (pub, priv) pair for the test to sign with.
func registerEd25519User(tb testing.TB, dir, user string) (pub, priv []byte) {
	tb.Helper()
	pub, priv = newEd25519Keypair(tb)
	authLine := auth.EncodeSSHEd25519AuthorizedKey(pub, user+"@test")
	if err := os.WriteFile(filepath.Join(dir, user+".pub"), authLine, 0o644); err != nil {
		tb.Fatalf("write %s: %v", user, err)
	}
	return pub, priv
}

func newEd25519Keypair(tb testing.TB) (pub, priv []byte) {
	tb.Helper()
	pub, priv, err := wolfcrypt.Ed25519GenKey()
	if err != nil {
		tb.Fatalf("Ed25519GenKey: %v", err)
	}
	return pub, priv
}
