// Phase 10.1c: X.509 cert minting via wolfCrypt.
//
// Tests confirm MintCert produces non-empty DER + key + SEC1 public
// key for both the self-signed and CA-signed paths, and that a CA
// can verify a leaf cert it signed using wolfcrypt.ECCVerifyP256
// over wolfcrypt.SHA256 of the tbsCertificate.
//
// No non-wolfCrypt crypto runs here: the tiny DER walker only
// reads tag+length headers (wire format, no algorithms).
package wolfcrypt_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// TestSHA256_NIST pins the wolfCrypt SHA-256 wrapper to the NIST
// canonical KAT: SHA-256("abc").
func TestSHA256_NIST(t *testing.T) {
	want, _ := hex.DecodeString(
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	)
	got, err := wolfcrypt.SHA256([]byte("abc"))
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("SHA256(\"abc\")\n got = %x\nwant = %x", got, want)
	}
}

// TestSHA256_Empty pins SHA-256 of the empty string.
func TestSHA256_Empty(t *testing.T) {
	want, _ := hex.DecodeString(
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	)
	got, err := wolfcrypt.SHA256(nil)
	if err != nil {
		t.Fatalf("SHA256(nil): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("SHA256(nil)\n got = %x\nwant = %x", got, want)
	}
}

// TestMintCert_SelfSignedCA confirms the self-signed path returns
// a non-empty DER bundle and that the cert DER is structurally a
// SEQUENCE.
func TestMintCert_SelfSignedCA(t *testing.T) {
	ca, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName:   "wolfCI Test CA",
		Organization: "wolfSSL Inc.",
		DaysValid:    7,
		IsCA:         true,
	}, nil)
	if err != nil {
		t.Fatalf("MintCert(CA): %v", err)
	}
	if len(ca.CertDER) == 0 || ca.CertDER[0] != 0x30 {
		t.Fatal("CA cert DER missing SEQUENCE tag")
	}
	if len(ca.KeyDER) == 0 {
		t.Fatal("CA key DER is empty")
	}
	if len(ca.PubSEC1) != 65 || ca.PubSEC1[0] != 0x04 {
		t.Fatalf("CA pub SEC1 should be 65 bytes starting with 0x04, got %d bytes starting %#x",
			len(ca.PubSEC1), ca.PubSEC1[0])
	}
}

// TestMintCert_LeafSignedByCA confirms the CA-signed path works
// and produces a leaf cert distinct from the CA cert.
func TestMintCert_LeafSignedByCA(t *testing.T) {
	ca, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName: "wolfCI Test CA",
		DaysValid:  7,
		IsCA:       true,
	}, nil)
	if err != nil {
		t.Fatalf("MintCert(CA): %v", err)
	}

	leaf, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName:  "localhost",
		DaysValid:   1,
		DNSNames:    []string{"localhost"},
		IPAddresses: []string{"127.0.0.1"},
		ExtKeyUsage: "serverAuth",
	}, ca)
	if err != nil {
		t.Fatalf("MintCert(leaf): %v", err)
	}
	if len(leaf.CertDER) == 0 || leaf.CertDER[0] != 0x30 {
		t.Fatal("leaf cert DER missing SEQUENCE tag")
	}
	if bytes.Equal(leaf.CertDER, ca.CertDER) {
		t.Fatal("leaf cert DER is identical to CA cert DER")
	}
	if bytes.Equal(leaf.KeyDER, ca.KeyDER) {
		t.Fatal("leaf key DER is identical to CA key DER")
	}
}

// TestMintCert_LeafSignatureVerifiesAgainstCA ties Phase 10.1b and
// 10.1c together: a leaf cert produced by wolfCrypt-side signing
// must verify against the CA's public key via
// wolfcrypt.ECCVerifyP256 over wolfcrypt.SHA256 of the
// tbsCertificate.
func TestMintCert_LeafSignatureVerifiesAgainstCA(t *testing.T) {
	ca, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName: "wolfCI Test CA",
		DaysValid:  7,
		IsCA:       true,
	}, nil)
	if err != nil {
		t.Fatalf("MintCert(CA): %v", err)
	}
	leaf, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName: "test-leaf",
		DaysValid:  1,
	}, ca)
	if err != nil {
		t.Fatalf("MintCert(leaf): %v", err)
	}

	tbs, sig, err := extractTBSAndSig(leaf.CertDER)
	if err != nil {
		t.Fatalf("extractTBSAndSig: %v", err)
	}
	hash, err := wolfcrypt.SHA256(tbs)
	if err != nil {
		t.Fatalf("SHA256(tbs): %v", err)
	}
	ok, err := wolfcrypt.ECCVerifyP256(ca.PubSEC1, hash, sig)
	if err != nil {
		t.Fatalf("ECCVerifyP256: %v", err)
	}
	if !ok {
		t.Fatal("leaf cert signature did not verify against CA public key")
	}

	tampered := append([]byte{}, tbs...)
	tampered[len(tampered)-1] ^= 0x01
	hash2, _ := wolfcrypt.SHA256(tampered)
	if ok, _ := wolfcrypt.ECCVerifyP256(ca.PubSEC1, hash2, sig); ok {
		t.Fatal("tampered tbsCertificate verified; tamper detection broken")
	}
}

// extractTBSAndSig walks an X.509 certificate DER and returns the
// tbsCertificate bytes (the part that was signed) and the raw
// signature bytes (the BIT STRING contents after the unused-bits
// byte). Wire-format parsing - no crypto.
//
// Certificate ::= SEQUENCE {
//     tbsCertificate     SEQUENCE { ... },
//     signatureAlgorithm SEQUENCE { ... },
//     signatureValue     BIT STRING
// }
func extractTBSAndSig(cert []byte) (tbs, sig []byte, err error) {
	if len(cert) == 0 || cert[0] != 0x30 {
		return nil, nil, fmt.Errorf("cert: outer tag not SEQUENCE")
	}
	outerBodyStart, _, err := derContent(cert, 0)
	if err != nil {
		return nil, nil, err
	}

	tbsStart := outerBodyStart
	tbsEnd, err := derNext(cert, tbsStart)
	if err != nil {
		return nil, nil, fmt.Errorf("tbsCertificate: %w", err)
	}
	tbs = cert[tbsStart:tbsEnd]

	algEnd, err := derNext(cert, tbsEnd)
	if err != nil {
		return nil, nil, fmt.Errorf("signatureAlgorithm: %w", err)
	}

	if cert[algEnd] != 0x03 {
		return nil, nil, fmt.Errorf("signatureValue: expected BIT STRING (0x03), got %#x", cert[algEnd])
	}
	sigBodyStart, sigBodyEnd, err := derContent(cert, algEnd)
	if err != nil {
		return nil, nil, err
	}
	if sigBodyStart >= sigBodyEnd || cert[sigBodyStart] != 0x00 {
		return nil, nil, fmt.Errorf("signatureValue: expected 0 unused bits")
	}
	sig = cert[sigBodyStart+1 : sigBodyEnd]
	return tbs, sig, nil
}

func derContent(b []byte, off int) (bodyStart, bodyEnd int, err error) {
	if off+1 >= len(b) {
		return 0, 0, fmt.Errorf("DER: truncated at length byte")
	}
	length := int(b[off+1])
	bodyStart = off + 2
	if length&0x80 != 0 {
		nLen := length & 0x7f
		if nLen == 0 || nLen > 4 {
			return 0, 0, fmt.Errorf("DER: unsupported length form %#x", length)
		}
		if bodyStart+nLen > len(b) {
			return 0, 0, fmt.Errorf("DER: truncated multi-byte length")
		}
		length = 0
		for i := 0; i < nLen; i++ {
			length = (length << 8) | int(b[bodyStart+i])
		}
		bodyStart += nLen
	}
	bodyEnd = bodyStart + length
	if bodyEnd > len(b) {
		return 0, 0, fmt.Errorf("DER: TLV body extends past buffer")
	}
	return bodyStart, bodyEnd, nil
}

func derNext(b []byte, off int) (int, error) {
	_, end, err := derContent(b, off)
	return end, err
}
