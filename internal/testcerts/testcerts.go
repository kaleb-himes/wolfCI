// Package testcerts provides certificate helpers shared by tests
// across wolfCI packages. It is not test-only (no _test.go suffix)
// so any test package can import it.
package testcerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// SelfSignedECDSA returns a fresh ECDSA P-256 self-signed
// certificate valid for one hour against 127.0.0.1 and "localhost",
// returned as PEM blocks ready to feed tlsutil.Config.
//
// Test helper only; callers must pass a testing.TB. Failures call
// tb.Fatal so the test stops at the helper rather than propagating
// a meaningless cert into a downstream assertion.
func SelfSignedECDSA(tb testing.TB) (certPEM, keyPEM []byte) {
	tb.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("testcerts: ecdsa.GenerateKey: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wolfci-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		tb.Fatalf("testcerts: x509.CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		tb.Fatalf("testcerts: x509.MarshalECPrivateKey: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
