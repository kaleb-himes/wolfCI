package tlsutil_test

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/testcerts"
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
	certPEM, keyPEM := testcerts.SelfSignedECDSA(t)

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

