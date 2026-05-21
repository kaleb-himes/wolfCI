// Package tlsutil exposes a wolfSSL-backed TLS net.Listener for the
// wolfCI server. The TLS handshake, record layer, and crypto are
// provided by wolfSSL via CGO; Go code only sees standard net.Conn
// and net.Listener interfaces.
//
// This file is a stub for PLAN.md task 1.3. Task 1.4 will replace
// the stub with the real CGO wrapper.
package tlsutil

import (
	"errors"
	"net"
)

// Config controls how a wolfSSL TLS listener accepts connections.
type Config struct {
	// Certificate is the server's PEM-encoded certificate chain.
	Certificate []byte

	// Key is the server's PEM-encoded private key matching Certificate.
	Key []byte

	// MinVersion is the minimum TLS protocol version, encoded the same
	// way crypto/tls encodes it (e.g. 0x0303 for TLS 1.2, 0x0304 for
	// TLS 1.3). A zero value defaults to TLS 1.3.
	MinVersion uint16
}

// NewListener wraps an inner net.Listener so each accepted connection
// performs a wolfSSL-backed TLS handshake against cfg before the
// returned net.Conn is handed to the caller.
//
// The real CGO implementation lands in PLAN.md task 1.4. Until then
// this stub returns ErrNotImplemented so the failing handshake test
// for task 1.3 makes the gap observable.
func NewListener(inner net.Listener, cfg *Config) (net.Listener, error) {
	_ = inner
	_ = cfg
	return nil, ErrNotImplemented
}

// ErrNotImplemented is returned by stub APIs that will be wired up in
// later phases. Callers should not depend on its exact text.
var ErrNotImplemented = errors.New("tlsutil: not implemented; see PLAN.md task 1.4")
