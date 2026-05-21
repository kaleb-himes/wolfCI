// Phase 10.1b: signature-verify primitives.
//
// All test vectors below are hard-coded from published / externally-
// produced sources so the wolfCI test code itself does not invoke
// any non-wolfCrypt crypto at runtime:
//
//   Ed25519:   RFC 8032 Section 7.1 Test 2 (Ed25519 KAT)
//   ECDSA-256: RFC 6979 Section A.2.5 ("sample" + SHA-256)
//   RSA:       generated outside this source tree with OpenSSL 3.x
//              (2048-bit key, PKCS#1 v1.5, SHA-256). Provenance
//              comment on the test itself.
package wolfcrypt_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// mustHex decodes a hex string or fails the test. ASCII only - no
// stdlib crypto.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	return b
}

// derEncodeECDSA builds an ASN.1 DER SEQUENCE of two INTEGERs from
// raw big-endian r and s. This is byte assembly, not crypto, so it
// is fine to live in test code. We need it because the wolfCrypt
// wc_ecc_verify_hash API takes signatures in this DER form, but
// RFC 6979 publishes r and s as raw integers.
func derEncodeECDSA(t *testing.T, r, s []byte) []byte {
	t.Helper()
	encodeInt := func(b []byte) []byte {
		// Strip leading zeros.
		for len(b) > 1 && b[0] == 0x00 {
			b = b[1:]
		}
		// If high bit of the first byte is set, prepend 0x00 so
		// the INTEGER stays positive in ASN.1 (which is signed).
		if b[0]&0x80 != 0 {
			b = append([]byte{0x00}, b...)
		}
		out := []byte{0x02, byte(len(b))}
		return append(out, b...)
	}
	rEnc := encodeInt(r)
	sEnc := encodeInt(s)
	body := append(rEnc, sEnc...)
	if len(body) > 0xff {
		t.Fatalf("derEncodeECDSA: body too large for short-form length")
	}
	return append([]byte{0x30, byte(len(body))}, body...)
}

// TestEd25519Verify_RFC8032_TC2 is the canonical Ed25519 KAT (RFC
// 8032 Section 7.1, "Test 2"): a 1-byte message signed with a
// fixed key.
func TestEd25519Verify_RFC8032_TC2(t *testing.T) {
	pub := mustHex(t, "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c")
	msg := mustHex(t, "72")
	sig := mustHex(t,
		"92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da"+
			"085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
	)
	ok, err := wolfcrypt.Ed25519Verify(pub, msg, sig)
	if err != nil {
		t.Fatalf("Ed25519Verify: %v", err)
	}
	if !ok {
		t.Fatal("Ed25519Verify returned false for RFC 8032 TC2 KAT")
	}
}

// TestEd25519Verify_TamperedMsg flips a byte in the message; a
// wrapper that always returns true would still report a match,
// which is the failure mode this test is designed to catch.
func TestEd25519Verify_TamperedMsg(t *testing.T) {
	pub := mustHex(t, "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c")
	sig := mustHex(t,
		"92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da"+
			"085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
	)
	// Flip 0x72 -> 0x73.
	ok, err := wolfcrypt.Ed25519Verify(pub, []byte{0x73}, sig)
	if err != nil {
		t.Fatalf("Ed25519Verify on tampered msg returned err: %v (want ok=false, nil)", err)
	}
	if ok {
		t.Fatal("Ed25519Verify accepted a tampered message; refusing is the whole point")
	}
}

// TestEd25519Verify_BadKeyLen rejects an obviously malformed public
// key (wrong length).
func TestEd25519Verify_BadKeyLen(t *testing.T) {
	pub := []byte{0x01, 0x02, 0x03}
	msg := []byte("anything")
	sig := bytes.Repeat([]byte{0}, 64)
	if _, err := wolfcrypt.Ed25519Verify(pub, msg, sig); err == nil {
		t.Fatal("Ed25519Verify accepted a 3-byte public key; want error")
	}
}

// TestECCVerifyP256_RFC6979 is the canonical ECDSA-P256-SHA256 KAT
// from RFC 6979 Section A.2.5 ("sample"). The hash of "sample"
// under SHA-256 is included in the RFC; the test embeds it so we
// do not need to hash here.
func TestECCVerifyP256_RFC6979(t *testing.T) {
	// SEC1 uncompressed: 0x04 || X || Y
	pub := mustHex(t,
		"04"+
			"60FED4BA255A9D31C961EB74C6356D68C049B8923B61FA6CE669622E60F29FB6"+
			"7903FE1008B8BC99A41AE9E95628BC64F2F1B20C2D7E9F5177A3C294D4462299",
	)
	hash := mustHex(t,
		"af2bdbe1aa9b6ec1e2ade1d694f41fc71a831d0268e9891562113d8a62add1bf",
	)
	r := mustHex(t, "EFD48B2AACB6A8FD1140DD9CD45E81D69D2C877B56AAF991C34D0EA84EAF3716")
	s := mustHex(t, "F7CB1C942D657C41D436C7A1B6E29F65F3E900DBB9AFF4064DC4AB2F843ACDA8")
	sig := derEncodeECDSA(t, r, s)

	ok, err := wolfcrypt.ECCVerifyP256(pub, hash, sig)
	if err != nil {
		t.Fatalf("ECCVerifyP256: %v", err)
	}
	if !ok {
		t.Fatal("ECCVerifyP256 returned false for RFC 6979 KAT")
	}
}

// TestECCVerifyP256_TamperedHash catches a wrapper that ignores the
// hash and always returns true.
func TestECCVerifyP256_TamperedHash(t *testing.T) {
	pub := mustHex(t,
		"04"+
			"60FED4BA255A9D31C961EB74C6356D68C049B8923B61FA6CE669622E60F29FB6"+
			"7903FE1008B8BC99A41AE9E95628BC64F2F1B20C2D7E9F5177A3C294D4462299",
	)
	hash := mustHex(t,
		"af2bdbe1aa9b6ec1e2ade1d694f41fc71a831d0268e9891562113d8a62add1c0", // last byte flipped
	)
	r := mustHex(t, "EFD48B2AACB6A8FD1140DD9CD45E81D69D2C877B56AAF991C34D0EA84EAF3716")
	s := mustHex(t, "F7CB1C942D657C41D436C7A1B6E29F65F3E900DBB9AFF4064DC4AB2F843ACDA8")
	sig := derEncodeECDSA(t, r, s)

	ok, err := wolfcrypt.ECCVerifyP256(pub, hash, sig)
	if err != nil {
		t.Fatalf("ECCVerifyP256 on tampered hash returned err: %v (want ok=false, nil)", err)
	}
	if ok {
		t.Fatal("ECCVerifyP256 accepted a tampered hash")
	}
}

// rsaTestVector is an RSA-2048 PKCS#1 v1.5 SHA-256 signature
// produced outside this source tree with:
//
//	openssl genrsa -out priv.pem 2048
//	openssl dgst -sha256 -sign priv.pem -out sig.bin msg.bin
//	openssl rsa  -in priv.pem -modulus -noout
//
// The modulus and exponent below are the RSA public key matching
// that private key; the signature is the PKCS#1 v1.5 + SHA-256
// signature over the message bytes "wolfCI test message for RSA
// signature verification" (ASCII, no trailing newline).
//
// Only wolfCrypt code touches these values at runtime.
var (
	rsaModulusHex = "C51AEB505274062067DE547C179CA29A925B3D2F74C241DC23572FAB8A5BE2B5" +
		"461E51214A11003A3C1FABF3E224C0463EE8D35CB44A00899209C9CDD22640BB" +
		"FC0B645270C9A0D989F23DC2C5A5E6D39D8FD664307E388A9C1DD17F01C71468" +
		"2A71E70381049535F7CCDC6A37576E02C634CB53E219AA0182608F670EF1B540" +
		"CADAE541684BD272D6A57289E2D24EB8EFEF81959D9ED9077A1FD6BAC96C2C67" +
		"42D7D33914756D1D37995BD14812954CD3AB0B75C659D0A882176C0280927652" +
		"45CEBC5C01F0086331FAD16C28D96AA8285E1D4BD6B312F5AA7DBAAC1B6451AB" +
		"120869A832235F582C7F5C13336FDC1DBCDE3331C568B02948370B821FB17DD3"

	rsaExponent = []byte{0x01, 0x00, 0x01} // 65537

	rsaMessage = []byte("wolfCI test message for RSA signature verification")

	rsaSignatureHex = "bf0adce6abe4fb23909a087c37ae4f254a0390dfea2e0141153f7622d0b12e1d" +
		"bdace7cb2219a687b9ebd6a877ea31a014e12ab85168647274a2b1d2091ad1ee" +
		"d3834cdd80b427bcb33507f2d259da4ea33e8d8d5fe75f2d6360e3bf5cfa6c0b" +
		"d9ec19a22399e4dc26ab1cdf631f9a63d2634cf264f3959d6d66b2fa66197e1e" +
		"1b43f4062a65ca907aacab1224f3753a6a8291e0779498ce1abf07c7121f3b88" +
		"5f44e093c12ab0d5c34661d61895b7910b07779fba8c767d3c55794ea19ce802" +
		"cfc02ecfd1c8aa332018714185e31b0a5471ba056d16f80c962124a4fbba4688" +
		"e64e939d19b268e5b67f35dbaac01987fba377a0dacfd074704807cd3f0a9106"
)

func TestRSAVerifyPKCS1v15SHA256_KnownVector(t *testing.T) {
	modulus := mustHex(t, rsaModulusHex)
	signature := mustHex(t, rsaSignatureHex)

	ok, err := wolfcrypt.RSAVerifyPKCS1v15SHA256(modulus, rsaExponent, rsaMessage, signature)
	if err != nil {
		t.Fatalf("RSAVerifyPKCS1v15SHA256: %v", err)
	}
	if !ok {
		t.Fatal("RSAVerifyPKCS1v15SHA256 returned false for the embedded vector")
	}
}

func TestRSAVerifyPKCS1v15SHA256_TamperedMessage(t *testing.T) {
	modulus := mustHex(t, rsaModulusHex)
	signature := mustHex(t, rsaSignatureHex)
	tampered := append([]byte{}, rsaMessage...)
	tampered[0] ^= 0x01

	ok, err := wolfcrypt.RSAVerifyPKCS1v15SHA256(modulus, rsaExponent, tampered, signature)
	if err != nil {
		t.Fatalf("RSAVerifyPKCS1v15SHA256 on tampered msg returned err: %v (want ok=false, nil)", err)
	}
	if ok {
		t.Fatal("RSAVerifyPKCS1v15SHA256 accepted a tampered message")
	}
}

func TestRSAVerifyPKCS1v15SHA256_TamperedSignature(t *testing.T) {
	modulus := mustHex(t, rsaModulusHex)
	signature := mustHex(t, rsaSignatureHex)
	tampered := append([]byte{}, signature...)
	tampered[len(tampered)-1] ^= 0x01

	ok, err := wolfcrypt.RSAVerifyPKCS1v15SHA256(modulus, rsaExponent, rsaMessage, tampered)
	if err != nil {
		// PKCS#1 v1.5 unpadding can legitimately error on a bad
		// signature; treat any error as a clean reject for the
		// purposes of this test.
		return
	}
	if ok {
		t.Fatal("RSAVerifyPKCS1v15SHA256 accepted a tampered signature")
	}
}
