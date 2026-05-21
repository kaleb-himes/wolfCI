// Package tlsutil exposes wolfSSL-backed TLS net.Listener and
// net.Conn primitives for the wolfCI server and its agents. The
// TLS handshake, record layer, and crypto are provided by
// wolfSSL via CGO; Go code only sees standard net.Conn and
// net.Listener interfaces.
//
// The wolfSSL static archive must be present at
// build/wolfssl-install/lib/libwolfssl.a before this package can
// be built. Run scripts/build-wolfssl.sh to produce it.
//
// TLS 1.3 is the only protocol version currently exposed in
// Go-land. mTLS is opt-in via the Config.ClientCAs (server side)
// and Config.RootCAs (client side) fields.
package tlsutil

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/ssl.h>
#include <stdlib.h>

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

static WOLFSSL_METHOD* wolfci_tls13_client_method(void) {
    return wolfTLSv1_3_client_method();
}

static void wolfci_ctx_require_client_certs(WOLFSSL_CTX* ctx) {
    wolfSSL_CTX_set_verify(ctx,
        WOLFSSL_VERIFY_PEER | WOLFSSL_VERIFY_FAIL_IF_NO_PEER_CERT, NULL);
}

static void wolfci_ctx_verify_server(WOLFSSL_CTX* ctx) {
    wolfSSL_CTX_set_verify(ctx, WOLFSSL_VERIFY_PEER, NULL);
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

// Config controls how a wolfSSL TLS endpoint completes its
// handshake. Some fields apply only to one side:
//
//   server only: ClientCAs (required for mTLS server verify)
//   client only: RootCAs, ServerName
//   both:        Certificate, Key, MinVersion
type Config struct {
	// Certificate is this endpoint's PEM-encoded certificate
	// chain. Required.
	Certificate []byte

	// Key is this endpoint's PEM-encoded private key matching
	// Certificate. Required.
	Key []byte

	// MinVersion is the minimum TLS protocol version, using the
	// same constants as crypto/tls (e.g. tls.VersionTLS13). Zero
	// defaults to TLS 1.3. Only TLS 1.3 is implemented today.
	MinVersion uint16

	// ClientCAs (server side) is a PEM-encoded bundle of CA
	// certificates used to verify client certificates. If
	// non-empty, the listener requires every accepted connection
	// to present a client cert signed by one of these CAs;
	// otherwise the handshake fails. mTLS is opt-in.
	ClientCAs []byte

	// RootCAs (client side) is a PEM-encoded bundle of CA
	// certificates used by Dial to verify the server's
	// certificate. Required by Dial unless tests intentionally
	// disable verification (not currently exposed).
	RootCAs []byte

	// ServerName (client side) is reserved for future SNI use.
	// Currently unused.
	ServerName string
}

// ErrNotImplemented is reserved for APIs that have not yet been
// wired up.
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

// NewListener wraps inner so each accepted connection performs
// a wolfSSL TLS handshake against cfg. The returned net.Conn
// from Accept speaks TLS as configured.
//
// If cfg.ClientCAs is non-empty the listener turns on mTLS: it
// will require every incoming connection to present a client
// certificate signed by one of those CAs.
func NewListener(inner net.Listener, cfg *Config) (net.Listener, error) {
	if inner == nil {
		return nil, errors.New("tlsutil: nil inner net.Listener")
	}
	if err := validateBaseConfig(cfg); err != nil {
		return nil, err
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
	if err := loadCertAndKey(ctx, cfg); err != nil {
		C.wolfSSL_CTX_free(ctx)
		return nil, err
	}
	if len(cfg.ClientCAs) > 0 {
		caPtr := (*C.uchar)(unsafe.Pointer(&cfg.ClientCAs[0]))
		if rc := C.wolfSSL_CTX_load_verify_buffer(ctx,
			caPtr, C.long(len(cfg.ClientCAs)),
			C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
			C.wolfSSL_CTX_free(ctx)
			return nil, fmt.Errorf("tlsutil: load ClientCAs rc=%d", int(rc))
		}
		C.wolfci_ctx_require_client_certs(ctx)
	}
	C.wolfci_setup_ctx_io(ctx)
	return &listener{inner: inner, ctx: ctx}, nil
}

// Dial opens a wolfSSL TLS client connection to address over
// network ("tcp", "tcp4", "tcp6"). The server's certificate is
// verified against cfg.RootCAs; the client presents
// cfg.Certificate + cfg.Key to support mTLS.
//
// The returned net.Conn is owned by the caller; Close releases
// the per-connection WOLFSSL and the underlying TCP socket.
func Dial(network, address string, cfg *Config) (net.Conn, error) {
	if err := validateBaseConfig(cfg); err != nil {
		return nil, err
	}
	if len(cfg.RootCAs) == 0 {
		return nil, errors.New("tlsutil.Dial: Config.RootCAs is required")
	}
	initWolfSSL()
	if initErr != nil {
		return nil, initErr
	}

	method := C.wolfci_tls13_client_method()
	if method == nil {
		return nil, errors.New("tlsutil.Dial: wolfTLSv1_3_client_method returned nil")
	}
	ctx := C.wolfSSL_CTX_new(method)
	if ctx == nil {
		return nil, errors.New("tlsutil.Dial: wolfSSL_CTX_new returned nil")
	}
	if err := loadCertAndKey(ctx, cfg); err != nil {
		C.wolfSSL_CTX_free(ctx)
		return nil, err
	}
	rootPtr := (*C.uchar)(unsafe.Pointer(&cfg.RootCAs[0]))
	if rc := C.wolfSSL_CTX_load_verify_buffer(ctx,
		rootPtr, C.long(len(cfg.RootCAs)),
		C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
		C.wolfSSL_CTX_free(ctx)
		return nil, fmt.Errorf("tlsutil.Dial: load RootCAs rc=%d", int(rc))
	}
	C.wolfci_ctx_verify_server(ctx)
	C.wolfci_setup_ctx_io(ctx)

	raw, err := net.Dial(network, address)
	if err != nil {
		C.wolfSSL_CTX_free(ctx)
		return nil, fmt.Errorf("tlsutil.Dial: tcp dial: %w", err)
	}

	ssl := C.wolfSSL_new(ctx)
	if ssl == nil {
		raw.Close()
		C.wolfSSL_CTX_free(ctx)
		return nil, errors.New("tlsutil.Dial: wolfSSL_new returned nil")
	}
	h := cgo.NewHandle(raw)
	hPtr := unsafe.Pointer(uintptr(h))
	C.wolfSSL_SetIOReadCtx(ssl, hPtr)
	C.wolfSSL_SetIOWriteCtx(ssl, hPtr)

	if rc := C.wolfSSL_connect(ssl); rc != C.WOLFSSL_SUCCESS {
		ec := int(C.wolfSSL_get_error(ssl, rc))
		C.wolfSSL_free(ssl)
		h.Delete()
		raw.Close()
		C.wolfSSL_CTX_free(ctx)
		return nil, fmt.Errorf("tlsutil.Dial: wolfSSL_connect rc=%d ec=%d", int(rc), ec)
	}

	return &conn{inner: raw, ssl: ssl, handle: h, ownCtx: ctx}, nil
}

func validateBaseConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("tlsutil: nil Config")
	}
	if len(cfg.Certificate) == 0 || len(cfg.Key) == 0 {
		return errors.New("tlsutil: Config.Certificate and Config.Key are required")
	}
	if cfg.MinVersion != 0 && cfg.MinVersion != tls.VersionTLS13 {
		return fmt.Errorf("tlsutil: only TLS 1.3 is implemented (got MinVersion 0x%04x)",
			cfg.MinVersion)
	}
	return nil
}

func loadCertAndKey(ctx *C.WOLFSSL_CTX, cfg *Config) error {
	certPtr := (*C.uchar)(unsafe.Pointer(&cfg.Certificate[0]))
	if rc := C.wolfSSL_CTX_use_certificate_buffer(ctx,
		certPtr, C.long(len(cfg.Certificate)),
		C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
		return fmt.Errorf("tlsutil: use_certificate_buffer rc=%d", int(rc))
	}
	keyPtr := (*C.uchar)(unsafe.Pointer(&cfg.Key[0]))
	if rc := C.wolfSSL_CTX_use_PrivateKey_buffer(ctx,
		keyPtr, C.long(len(cfg.Key)),
		C.SSL_FILETYPE_PEM); rc != C.WOLFSSL_SUCCESS {
		return fmt.Errorf("tlsutil: use_PrivateKey_buffer rc=%d", int(rc))
	}
	return nil
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
	ownCtx *C.WOLFSSL_CTX // non-nil for client-dialed conns; freed in Close
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
	if c.ownCtx != nil {
		C.wolfSSL_CTX_free(c.ownCtx)
		c.ownCtx = nil
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
func wolfci_io_recv(ssl *C.WOLFSSL, buf *C.char, sz C.int, ctxPtr unsafe.Pointer) (ret C.int) {
	// Recover from a panic inside cgo.Handle.Value, which fires if
	// the handle was Delete'd between when wolfSSL_read started in
	// C and when it called back into Go. This happens when a peer
	// (e.g. the gRPC HTTP/2 reader goroutine) holds a *conn whose
	// Close ran while a Read was already in flight. Returning a
	// generic CBIO error lets wolfSSL propagate a clean read error
	// to the caller instead of crashing the process.
	defer func() {
		if r := recover(); r != nil {
			ret = C.WOLFSSL_CBIO_ERR_GENERAL
		}
	}()
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
func wolfci_io_send(ssl *C.WOLFSSL, buf *C.char, sz C.int, ctxPtr unsafe.Pointer) (ret C.int) {
	defer func() {
		if r := recover(); r != nil {
			ret = C.WOLFSSL_CBIO_ERR_GENERAL
		}
	}()
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
