package tlsutil

/* TLS protocol version constants, as defined on the wire by the
 * IETF (RFC 5246 for TLS 1.2, RFC 8446 for TLS 1.3). These exist
 * so wolfCI code does not have to import crypto/tls (which carries
 * a non-wolfCrypt cryptographic implementation) just to name a
 * wire value. Only TLS 1.3 is exercised by tlsutil today; the
 * older constants are kept for completeness and so callers can
 * reject explicit downgrades by name rather than by magic number.
 */
const (
    VersionTLS10 uint16 = 0x0301
    VersionTLS11 uint16 = 0x0302
    VersionTLS12 uint16 = 0x0303
    VersionTLS13 uint16 = 0x0304
)
