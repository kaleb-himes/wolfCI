// Phase 10.6c smoke tests: exercise the wrappers we add to
// go-wolfssl piece-by-piece, before 10.6d swaps internal/wolfcrypt
// onto them. These tests stay around after the swap as regression
// catches if any wrapper signature drifts.
//
// Each test calls go-wolfssl directly (no internal/wolfcrypt
// indirection) so a wrapper-level bug fails here, not three
// layers up where the diagnosis is harder.
package wolfcrypt_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// TestGoWolfSSL_Ed25519_RFC8032_TC2 verifies the new go-wolfssl
// Ed25519 wrappers (patch 0003) against RFC 8032 Test 2: a 1-byte
// message signed with a fixed key, with a known-good signature.
//
// Drives: Wc_ed25519_init, Wc_ed25519_import_public,
// Wc_ed25519_verify_msg, Wc_ed25519_free.
func TestGoWolfSSL_Ed25519_RFC8032_TC2_Verify(t *testing.T) {
	pub, _ := hex.DecodeString(
		"3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
	)
	msg, _ := hex.DecodeString("72")
	sig, _ := hex.DecodeString(
		"92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da" +
			"085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
	)

	var key gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
		t.Fatalf("Wc_ed25519_init: %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&key)

	if rc := gowolf.Wc_ed25519_import_public(pub, len(pub), &key); rc != 0 {
		t.Fatalf("Wc_ed25519_import_public: %d", rc)
	}

	var result int
	if rc := gowolf.Wc_ed25519_verify_msg(sig, len(sig), msg, len(msg), &result, &key); rc != 0 {
		t.Fatalf("Wc_ed25519_verify_msg rc=%d", rc)
	}
	if result != 1 {
		t.Fatal("Wc_ed25519_verify_msg returned 0 (signature did not verify against RFC 8032 TC2 KAT)")
	}
}

// TestGoWolfSSL_Ed25519_RoundTrip verifies the full make_key + sign
// + verify path on a freshly generated key. Drives the rest of the
// new Ed25519 wrapper surface (make_key, export_*, sign_msg,
// import_private_key).
func TestGoWolfSSL_Ed25519_RoundTrip(t *testing.T) {
	var rng gowolf.WC_RNG
	if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
		t.Fatalf("Wc_InitRng: %d", rc)
	}
	defer gowolf.Wc_FreeRng(&rng)

	var key gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
		t.Fatalf("Wc_ed25519_init: %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&key)

	if rc := gowolf.Wc_ed25519_make_key(&rng, 32, &key); rc != 0 {
		t.Fatalf("Wc_ed25519_make_key: %d", rc)
	}

	pub := make([]byte, 32)
	pubLen := 32
	if rc := gowolf.Wc_ed25519_export_public(&key, pub, &pubLen); rc != 0 {
		t.Fatalf("Wc_ed25519_export_public: %d", rc)
	}
	if bytes.Equal(pub, make([]byte, 32)) {
		t.Fatal("exported public key is all-zero")
	}

	msg := []byte("phase 10.6c-i smoke test")
	sig := make([]byte, 64)
	sigLen := 64
	if rc := gowolf.Wc_ed25519_sign_msg(msg, len(msg), sig, &sigLen, &key); rc != 0 {
		t.Fatalf("Wc_ed25519_sign_msg: %d", rc)
	}

	// Re-import into a fresh key and verify, to also exercise
	// the import_public + verify path against a wolfCrypt-side
	// signature.
	var v gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&v); rc != 0 {
		t.Fatalf("Wc_ed25519_init(verify): %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&v)
	if rc := gowolf.Wc_ed25519_import_public(pub, len(pub), &v); rc != 0 {
		t.Fatalf("Wc_ed25519_import_public(verify): %d", rc)
	}
	var result int
	if rc := gowolf.Wc_ed25519_verify_msg(sig[:sigLen], sigLen, msg, len(msg), &result, &v); rc != 0 {
		t.Fatalf("Wc_ed25519_verify_msg: %d", rc)
	}
	if result != 1 {
		t.Fatal("verify_msg returned 0 for a fresh sign + verify roundtrip")
	}
}
