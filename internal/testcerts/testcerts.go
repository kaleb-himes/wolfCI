/* Package testcerts provides certificate helpers shared by tests
 * across wolfCI packages. It is not test-only (no _test.go suffix)
 * so any test package can import it.
 *
 * Phase 10.9 retired the stdlib crypto/* path (crypto/ecdsa,
 * crypto/elliptic, crypto/rand, crypto/x509, crypto/x509/pkix)
 * in favor of internal/wolfcrypt.MintCert. The DNS / IP SubjectAlt
 * Names that the original implementation set via x509.Certificate
 * are now encoded via internal/wolfcrypt's SAN helper and pushed
 * through go-wolfssl's certgen wrappers. The package's public API
 * (SelfSignedECDSA + NewMTLSChain + MTLSChain) is unchanged.
 */
package testcerts

import (
	"encoding/pem"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

/* SelfSignedECDSA returns a fresh ECDSA P-256 self-signed
 * certificate valid for one day against 127.0.0.1 and "localhost",
 * returned as PEM blocks ready to feed tlsutil.Config.
 *
 * Test helper only; callers must pass a testing.TB. Failures call
 * tb.Fatal so the test stops at the helper rather than propagating
 * a meaningless cert into a downstream assertion. */
func SelfSignedECDSA(tb testing.TB) (certPEM, keyPEM []byte) {
	tb.Helper()
	cert, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName:  "wolfci-test",
		DaysValid:   1,
		DNSNames:    []string{"localhost"},
		IPAddresses: []string{"127.0.0.1"},
		ExtKeyUsage: "serverAuth",
	}, nil)
	if err != nil {
		tb.Fatalf("testcerts: MintCert: %v", err)
	}
	return certPEM_(cert.CertDER), keyPEM_(cert.KeyDER)
}

/* MTLSChain holds a freshly-minted certificate chain suitable for
 * mTLS unit tests: a self-signed CA plus a server and an agent
 * (client) cert both signed by that CA. Every field is PEM. */
type MTLSChain struct {
	CACert     []byte
	ServerCert []byte
	ServerKey  []byte
	AgentCert  []byte
	AgentKey   []byte
}

/* NewMTLSChain mints a fresh CA and signs a server cert (with IP
 * SAN 127.0.0.1 and ServerAuth EKU) plus an agent cert (with
 * ClientAuth EKU). All certificates are ECDSA P-256 and valid for
 * one day. */
func NewMTLSChain(tb testing.TB) MTLSChain {
	tb.Helper()
	ca, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName: "wolfci-test-ca",
		DaysValid:  1,
		IsCA:       true,
	}, nil)
	if err != nil {
		tb.Fatalf("testcerts: CA MintCert: %v", err)
	}
	server, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName:  "wolfci-server",
		DaysValid:   1,
		DNSNames:    []string{"localhost"},
		IPAddresses: []string{"127.0.0.1"},
		ExtKeyUsage: "serverAuth",
	}, ca)
	if err != nil {
		tb.Fatalf("testcerts: server MintCert: %v", err)
	}
	agent, err := wolfcrypt.MintCert(wolfcrypt.CertConfig{
		CommonName:  "wolfci-agent-1",
		DaysValid:   1,
		ExtKeyUsage: "clientAuth",
	}, ca)
	if err != nil {
		tb.Fatalf("testcerts: agent MintCert: %v", err)
	}
	return MTLSChain{
		CACert:     certPEM_(ca.CertDER),
		ServerCert: certPEM_(server.CertDER),
		ServerKey:  keyPEM_(server.KeyDER),
		AgentCert:  certPEM_(agent.CertDER),
		AgentKey:   keyPEM_(agent.KeyDER),
	}
}

/* certPEM_ and keyPEM_ wrap a DER blob in the matching PEM block
 * type. PEM is wire format, not crypto, so encoding/pem stays. */
func certPEM_(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
}

func keyPEM_(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	})
}
