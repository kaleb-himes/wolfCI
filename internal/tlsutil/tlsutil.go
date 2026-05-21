// Package tlsutil exposes a wolfSSL-backed TLS net.Listener for the
// wolfCI server. The TLS handshake, record layer, and crypto are
// provided by wolfSSL via CGO; Go code only sees standard
// net.Conn and net.Listener interfaces.
//
// The wolfSSL static archive must be present at
// build/wolfssl-install/lib/libwolfssl.a before this package can be
// built. Run scripts/build-wolfssl.sh to produce it.
package tlsutil

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/ssl.h>
#include <stdlib.h>

// Callbacks implemented in Go via cgo //export. Declared here so
// the C wolfSSL_CTX_SetIORecv/Send installers can take their
// addresses.
extern int wolfci_io_recv(WOLFSSL* ssl, char* buf, int sz, void* ctx);
extern int wolfci_io_send(WOLFSSL* ssl, char* buf, int sz, void* ctx);

static void wolfci_setup_ctx_io(WOLFSSL_CTX* ctx) {
    wolfSSL_CTX_SetIORecv(ctx, wolfci_io_recv);
    wolfSSL_CTX_SetIOSend(ctx, wolfci_io_send);
}

static int wolfci_init(void) { return wolfSSL_Init(); }

static WOLFSSL_METHOD* wolfci_tls13_server_method(void) {
    return wolfTLSv1_3_server_method();
}
*/
import "C"

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

// Config controls how a wolfSSL TLS listener accepts connections.
type Config struct {
	// Certificate is the server's PEM-encoded certificate chain.
	Certificate []byte

	// Key is the server's PEM-encoded private key matching
	// Certificate.
	Key []byte

	// MinVersion is the minimum TLS protocol version, using the same
	// constants as crypto/tls (e.g. tls.VersionTLS13 = 0x0304). Zero
	// defaults to TLS 1.3. Only TLS 1.3 is implemented today;
	// crypto/tls.VersionTLS12 will be accepted in a follow-up.
	MinVersion uint16
}

// ErrNotImplemented is returned by APIs that have not yet been
// wired up. Callers should not depend on its exact text.
var ErrNotImplemented = errors.New("tlsutil: not implemented")

var (
	initOnce sync.Once
	initErr  error
)

func initWolfSSL() {
	initOnce.Do(func() {
		if rc := C.wolfci_init(); rc != C.WOLFSSL_SUCCESS {
			initErr = fmt.Errorf("tlsutil: wolfSSL_Init failed: rc=%d", int(rc))
		}
	})
}

// NewListener wraps inner so each accepted connection performs a
// wolfSSL TLS handshake against cfg. The returned net.Conn from
// Accept speaks TLS as configured.
func NewListener(inner net.Listener, cfg *Config) (net.Listener, error) {
	if inner == nil {
		return nil, errors.New("tlsutil: nil inner net.Listener")
	}
	if cfg == nil {
		return nil, errors.New("tlsutil: nil Config")
	}
	if len(cfg.Certificate) == 0 || len(cfg.Key) == 0 {
		return nil, errors.New("tlsutil: Config.Certificate and Config.Key are required")
	}
	if cfg.MinVersion != 0 && cfg.MinVersion != tls.VersionTLS13 {
		return nil, fmt.Errorf("tlsutil: only TLS 1.3 is implemented (got MinVersion 0x%04x)",
			cfg.MinVersion)
	}

	initWolfSSL()
	if initErr != nil {
		return nil, initErr
	}

	method := C.wolfci_tls13_server_method()
	if method == nil {
		return nil, errors.New("tlsutil: wolfTLSv1_3_server_method returned nil")
	}

	ctx := C.wolfSSL_CTX_new(method)
	if ctx == nil {
		return nil, errors.New("tlsutil: wolfSSL_CTX_new returned nil")
	}

	certPtr := (*C.uchar)(unsafe.Pointer(&cfg.Certificate[0]))
	if rc := C.wolfSSL_CTX_use_certificate_buffer(ctx,
		certPtr, C.long(len(cfg.Certificate)),
		C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
		C.wolfSSL_CTX_free(ctx)
		return nil, fmt.Errorf("tlsutil: wolfSSL_CTX_use_certificate_buffer rc=%d", int(rc))
	}

	keyPtr := (*C.uchar)(unsafe.Pointer(&cfg.Key[0]))
	if rc := C.wolfSSL_CTX_use_PrivateKey_buffer(ctx,
		keyPtr, C.long(len(cfg.Key)),
		C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
		C.wolfSSL_CTX_free(ctx)
		return nil, fmt.Errorf("tlsutil: wolfSSL_CTX_use_PrivateKey_buffer rc=%d", int(rc))
	}

	C.wolfci_setup_ctx_io(ctx)

	return &listener{inner: inner, ctx: ctx}, nil
}

type listener struct {
	inner  net.Listener
	ctx    *C.WOLFSSL_CTX
	mu     sync.Mutex
	closed bool
}

func (l *listener) Accept() (net.Conn, error) {
	raw, err := l.inner.Accept()
	if err != nil {
		return nil, err
	}

	ssl := C.wolfSSL_new(l.ctx)
	if ssl == nil {
		raw.Close()
		return nil, errors.New("tlsutil: wolfSSL_new returned nil")
	}

	h := cgo.NewHandle(raw)
	hPtr := unsafe.Pointer(uintptr(h))
	C.wolfSSL_SetIOReadCtx(ssl, hPtr)
	C.wolfSSL_SetIOWriteCtx(ssl, hPtr)

	if rc := C.wolfSSL_accept(ssl); rc != C.WOLFSSL_SUCCESS {
		ec := int(C.wolfSSL_get_error(ssl, rc))
		C.wolfSSL_free(ssl)
		h.Delete()
		raw.Close()
		return nil, fmt.Errorf("tlsutil: wolfSSL_accept failed rc=%d ec=%d", int(rc), ec)
	}

	return &conn{inner: raw, ssl: ssl, handle: h}, nil
}

func (l *listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.ctx != nil {
		C.wolfSSL_CTX_free(l.ctx)
		l.ctx = nil
	}
	return l.inner.Close()
}

func (l *listener) Addr() net.Addr { return l.inner.Addr() }

type conn struct {
	inner  net.Conn
	ssl    *C.WOLFSSL
	handle cgo.Handle
	mu     sync.Mutex
	closed bool
}

func (c *conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := C.wolfSSL_read(c.ssl, unsafe.Pointer(&p[0]), C.int(len(p)))
	if n > 0 {
		return int(n), nil
	}
	ec := int(C.wolfSSL_get_error(c.ssl, n))
	if ec == 0 {
		return 0, io.EOF
	}
	return 0, fmt.Errorf("tlsutil: wolfSSL_read rc=%d ec=%d", int(n), ec)
}

func (c *conn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := C.wolfSSL_write(c.ssl, unsafe.Pointer(&p[0]), C.int(len(p)))
	if n > 0 {
		return int(n), nil
	}
	ec := int(C.wolfSSL_get_error(c.ssl, n))
	return 0, fmt.Errorf("tlsutil: wolfSSL_write rc=%d ec=%d", int(n), ec)
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.ssl != nil {
		C.wolfSSL_shutdown(c.ssl)
		C.wolfSSL_free(c.ssl)
		c.ssl = nil
	}
	if c.handle != 0 {
		c.handle.Delete()
		c.handle = 0
	}
	return c.inner.Close()
}

func (c *conn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *conn) RemoteAddr() net.Addr               { return c.inner.RemoteAddr() }
func (c *conn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }

//export wolfci_io_recv
func wolfci_io_recv(ssl *C.WOLFSSL, buf *C.char, sz C.int, ctxPtr unsafe.Pointer) C.int {
	if sz <= 0 {
		return 0
	}
	h := cgo.Handle(uintptr(ctxPtr))
	nc, ok := h.Value().(net.Conn)
	if !ok {
		return C.WOLFSSL_CBIO_ERR_GENERAL
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(sz))
	n, err := nc.Read(dst)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return C.WOLFSSL_CBIO_ERR_CONN_CLOSE
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return C.WOLFSSL_CBIO_ERR_TIMEOUT
		}
		if n == 0 {
			return C.WOLFSSL_CBIO_ERR_GENERAL
		}
	}
	return C.int(n)
}

//export wolfci_io_send
func wolfci_io_send(ssl *C.WOLFSSL, buf *C.char, sz C.int, ctxPtr unsafe.Pointer) C.int {
	if sz <= 0 {
		return 0
	}
	h := cgo.Handle(uintptr(ctxPtr))
	nc, ok := h.Value().(net.Conn)
	if !ok {
		return C.WOLFSSL_CBIO_ERR_GENERAL
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(sz))
	n, err := nc.Write(src)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return C.WOLFSSL_CBIO_ERR_TIMEOUT
		}
		if n == 0 {
			return C.WOLFSSL_CBIO_ERR_GENERAL
		}
	}
	return C.int(n)
}
