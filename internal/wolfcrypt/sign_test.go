package wolfcrypt_test

import (
	"bytes"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// TestEd25519GenAndSign exercises Ed25519GenKey + Ed25519Sign by
// round-tripping a signature through Ed25519Verify. This is the
// "wolfCrypt signs, wolfCrypt verifies" guarantee for Ed25519.
func TestEd25519GenAndSign(t *testing.T) {
	pub, priv, err := wolfcrypt.Ed25519GenKey()
	if err != nil {
		t.Fatalf("Ed25519GenKey: %v", err)
	}
	if len(pub) != 32 {
		t.Fatalf("public key = %d bytes, want 32", len(pub))
	}
	if len(priv) != 64 {
		t.Fatalf("private key = %d bytes, want 64", len(priv))
	}
	msg := []byte("phase 10.3 smoke test")
	sig, err := wolfcrypt.Ed25519Sign(priv, msg)
	if err != nil {
		t.Fatalf("Ed25519Sign: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("signature = %d bytes, want 64", len(sig))
	}
	ok, err := wolfcrypt.Ed25519Verify(pub, msg, sig)
	if err != nil {
		t.Fatalf("Ed25519Verify: %v", err)
	}
	if !ok {
		t.Fatal("Ed25519Verify returned false for a fresh sign + verify roundtrip")
	}

	// Tamper
	tampered := append([]byte{}, msg...)
	tampered[0] ^= 0x01
	if ok, _ := wolfcrypt.Ed25519Verify(pub, tampered, sig); ok {
		t.Fatal("tampered message verified; tamper detection broken")
	}

	// Two GenKey calls must produce different keys (catches a buggy RNG).
	pub2, _, err := wolfcrypt.Ed25519GenKey()
	if err != nil {
		t.Fatalf("Ed25519GenKey second call: %v", err)
	}
	if bytes.Equal(pub, pub2) {
		t.Fatal("two Ed25519GenKey calls returned identical public keys")
	}
}
