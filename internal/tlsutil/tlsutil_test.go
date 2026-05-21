package tlsutil_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestListener_TLS13Handshake is the gating test for PLAN.md task
// 1.3 (red) and 1.4 (green). It builds a wolfSSL-backed listener,
// connects to it with the Go stdlib crypto/tls client, and asserts
// that a TLS 1.3 handshake completes and a single byte round-trips.
//
// The test fails today because tlsutil.NewListener returns
// ErrNotImplemented; task 1.4 replaces that stub with the real CGO
// wrapper.
func TestListener_TLS13Handshake(t *testing.T) {
	certPEM, keyPEM := selfSignedCert(t)

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer inner.Close()

	ln, err := tlsutil.NewListener(inner, &tlsutil.Config{
		Certificate: certPEM,
		Key:         keyPEM,
		MinVersion:  tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("tlsutil.NewListener: %v", err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 1)
		if _, err := conn.Read(buf); err != nil {
			serverErr <- err
			return
		}
		if _, err := conn.Write(buf); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	client, err := tls.Dial("tcp", inner.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	defer client.Close()

	if got := client.ConnectionState().Version; got != tls.VersionTLS13 {
		t.Fatalf("negotiated TLS version = 0x%04x, want TLS 1.3 (0x%04x)",
			got, tls.VersionTLS13)
	}

	if _, err := client.Write([]byte{42}); err != nil {
		t.Fatalf("client write: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if buf[0] != 42 {
		t.Fatalf("echo mismatch: got %d, want 42", buf[0])
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

// selfSignedCert produces a fresh ECDSA P-256 self-signed certificate
// valid for 127.0.0.1, returned as PEM blocks. Test-only.
func selfSignedCert(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "wolfci-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("x509.MarshalECPrivateKey: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
