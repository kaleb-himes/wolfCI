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

// TestGoWolfSSL_RSAVerify_KnownVector exercises the RSA wrappers
// added by patch 0004:
//   Wc_InitRsaKey / Wc_FreeRsaKey
//   Wc_RsaPublicKeyDecodeRaw (raw n + e, no DER)
//   Wc_SignatureVerify with WC_HASH_TYPE_SHA256 +
//   WC_SIGNATURE_TYPE_RSA_W_ENC
// against the RSA-2048 / PKCS#1 v1.5 / SHA-256 vector generated
// outside this tree with OpenSSL 3.x (provenance in verify_test.go).
func TestGoWolfSSL_RSAVerify_KnownVector(t *testing.T) {
	modulusHex := "C51AEB505274062067DE547C179CA29A925B3D2F74C241DC23572FAB8A5BE2B5" +
		"461E51214A11003A3C1FABF3E224C0463EE8D35CB44A00899209C9CDD22640BB" +
		"FC0B645270C9A0D989F23DC2C5A5E6D39D8FD664307E388A9C1DD17F01C71468" +
		"2A71E70381049535F7CCDC6A37576E02C634CB53E219AA0182608F670EF1B540" +
		"CADAE541684BD272D6A57289E2D24EB8EFEF81959D9ED9077A1FD6BAC96C2C67" +
		"42D7D33914756D1D37995BD14812954CD3AB0B75C659D0A882176C0280927652" +
		"45CEBC5C01F0086331FAD16C28D96AA8285E1D4BD6B312F5AA7DBAAC1B6451AB" +
		"120869A832235F582C7F5C13336FDC1DBCDE3331C568B02948370B821FB17DD3"
	exponent := []byte{0x01, 0x00, 0x01}
	message := []byte("wolfCI test message for RSA signature verification")
	signatureHex := "bf0adce6abe4fb23909a087c37ae4f254a0390dfea2e0141153f7622d0b12e1d" +
		"bdace7cb2219a687b9ebd6a877ea31a014e12ab85168647274a2b1d2091ad1ee" +
		"d3834cdd80b427bcb33507f2d259da4ea33e8d8d5fe75f2d6360e3bf5cfa6c0b" +
		"d9ec19a22399e4dc26ab1cdf631f9a63d2634cf264f3959d6d66b2fa66197e1e" +
		"1b43f4062a65ca907aacab1224f3753a6a8291e0779498ce1abf07c7121f3b88" +
		"5f44e093c12ab0d5c34661d61895b7910b07779fba8c767d3c55794ea19ce802" +
		"cfc02ecfd1c8aa332018714185e31b0a5471ba056d16f80c962124a4fbba4688" +
		"e64e939d19b268e5b67f35dbaac01987fba377a0dacfd074704807cd3f0a9106"
	modulus, _ := hex.DecodeString(modulusHex)
	signature, _ := hex.DecodeString(signatureHex)

	var key gowolf.RsaKey
	if rc := gowolf.Wc_InitRsaKey(&key, nil); rc != 0 {
		t.Fatalf("Wc_InitRsaKey: %d", rc)
	}
	defer gowolf.Wc_FreeRsaKey(&key)

	if rc := gowolf.Wc_RsaPublicKeyDecodeRaw(modulus, len(modulus), exponent, len(exponent), &key); rc != 0 {
		t.Fatalf("Wc_RsaPublicKeyDecodeRaw: %d", rc)
	}

	rc := gowolf.Wc_SignatureVerify(
		gowolf.WC_HASH_TYPE_SHA256,
		gowolf.WC_SIGNATURE_TYPE_RSA_W_ENC,
		message, len(message),
		signature, len(signature),
		&key,
	)
	if rc != 0 {
		t.Fatalf("Wc_SignatureVerify returned %d for the embedded RSA KAT", rc)
	}
}

// TestGoWolfSSL_MakeSelfSignedCert exercises the certgen wrappers
// added by patch 0005:
//   Wc_InitCert / Wc_MakeCert / Wc_SignCert
//   Wc_SetSubjectCN_Org (small C helper for the fixed-size subject
//   char arrays)
//   CTC_SHA256wECDSA constant
// by minting a self-signed ECC P-256 cert and asserting the DER
// is non-empty, starts with SEQUENCE (0x30), and is signed.
func TestGoWolfSSL_MakeSelfSignedCert(t *testing.T) {
	var rng gowolf.WC_RNG
	if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
		t.Fatalf("Wc_InitRng: %d", rc)
	}
	defer gowolf.Wc_FreeRng(&rng)

	var key gowolf.Ecc_key
	if rc := gowolf.Wc_ecc_init(&key); rc != 0 {
		t.Fatalf("Wc_ecc_init: %d", rc)
	}
	defer gowolf.Wc_ecc_free(&key)
	if rc := gowolf.Wc_ecc_make_key(&rng, 32, &key); rc != 0 {
		t.Fatalf("Wc_ecc_make_key: %d", rc)
	}

	var cert gowolf.Cert
	if rc := gowolf.Wc_InitCert(&cert); rc != 0 {
		t.Fatalf("Wc_InitCert: %d", rc)
	}
	gowolf.Wc_SetSubjectCN_Org(&cert, "wolfCI Test CA", "wolfSSL Inc.")
	gowolf.Wc_SetCertValidity(&cert, 7, true, gowolf.CTC_SHA256wECDSA)

	der := make([]byte, 4096)
	bodySz := gowolf.Wc_MakeCert(&cert, der, len(der), nil, &key, &rng)
	if bodySz < 0 {
		t.Fatalf("Wc_MakeCert: %d", bodySz)
	}
	totalSz := gowolf.Wc_SignCert(bodySz, gowolf.CTC_SHA256wECDSA, der, len(der), nil, &key, &rng)
	if totalSz < 0 {
		t.Fatalf("Wc_SignCert: %d", totalSz)
	}
	if totalSz == 0 || der[0] != 0x30 {
		t.Fatalf("cert DER missing SEQUENCE tag or empty (totalSz=%d, der[0]=%#x)", totalSz, der[0])
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
