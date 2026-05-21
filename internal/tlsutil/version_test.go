package tlsutil_test

/* Gates PLAN.md task 10.10: internal/tlsutil exposes its own TLS
 * protocol version constants so consumers (this package, the agent
 * client, wolfci-ctl) can stop importing crypto/tls just to name
 * a wire value. The wire numbers come from IETF (RFC 8446 etc.),
 * not from Go's stdlib.
 */

import (
    "net"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/testcerts"
    "github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

func TestVersionTLS13_WireValue(t *testing.T) {
    if got := tlsutil.VersionTLS13; got != 0x0304 {
        t.Fatalf("tlsutil.VersionTLS13 = 0x%04x, want 0x0304", got)
    }
}

func TestConfig_AcceptsLocalVersionTLS13(t *testing.T) {
    certPEM, keyPEM := testcerts.SelfSignedECDSA(t)

    inner, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("net.Listen: %v", err)
    }
    defer inner.Close()

    /* NewListener exercises validateBaseConfig; passing the local
     * constant must be accepted (zero would also be accepted as
     * the default; this proves the explicit value path).
     */
    ln, err := tlsutil.NewListener(inner, &tlsutil.Config{
        Certificate: certPEM,
        Key:         keyPEM,
        MinVersion:  tlsutil.VersionTLS13,
    })
    if err != nil {
        t.Fatalf("tlsutil.NewListener with local VersionTLS13: %v", err)
    }
    _ = ln.Close()
}
