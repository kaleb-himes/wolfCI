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

// MTLSChain holds a freshly-minted certificate chain suitable for
// mTLS unit tests: a self-signed CA plus a server and an agent
// (client) cert both signed by that CA. Every field is PEM.
type MTLSChain struct {
	CACert      []byte
	ServerCert  []byte
	ServerKey   []byte
	AgentCert   []byte
	AgentKey    []byte
}

// NewMTLSChain mints a fresh CA and signs a server cert (with IP
// SAN 127.0.0.1 and ServerAuth EKU) plus an agent cert (with
// ClientAuth EKU). All certificates are ECDSA P-256 and valid
// for one hour.
func NewMTLSChain(tb testing.TB) MTLSChain {
	tb.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("testcerts: CA GenerateKey: %v", err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wolfci-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		tb.Fatalf("testcerts: CA CreateCertificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		tb.Fatalf("testcerts: parse CA: %v", err)
	}

	chain := MTLSChain{
		CACert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}

	chain.ServerCert, chain.ServerKey = signLeaf(tb, caCert, caKey, leafSpec{
		serial:      big.NewInt(2),
		commonName:  "wolfci-server",
		ips:         []net.IP{net.ParseIP("127.0.0.1")},
		dnsNames:    []string{"localhost"},
		extKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	chain.AgentCert, chain.AgentKey = signLeaf(tb, caCert, caKey, leafSpec{
		serial:      big.NewInt(3),
		commonName:  "wolfci-agent-1",
		extKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return chain
}

type leafSpec struct {
	serial      *big.Int
	commonName  string
	ips         []net.IP
	dnsNames    []string
	extKeyUsage []x509.ExtKeyUsage
}

func signLeaf(tb testing.TB, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, spec leafSpec) (certPEM, keyPEM []byte) {
	tb.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("testcerts: leaf GenerateKey (%s): %v", spec.commonName, err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          spec.serial,
		Subject:               pkix.Name{CommonName: spec.commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           spec.extKeyUsage,
		IPAddresses:           spec.ips,
		DNSNames:              spec.dnsNames,
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		tb.Fatalf("testcerts: sign leaf %s: %v", spec.commonName, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		tb.Fatalf("testcerts: leaf MarshalECPrivateKey (%s): %v", spec.commonName, err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
