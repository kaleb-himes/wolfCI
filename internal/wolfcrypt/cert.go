package wolfcrypt

import (
	"errors"
	"fmt"
	"net"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// CertConfig is the input to MintCert. CommonName and DaysValid
// are required.
//
// DNSNames and IPAddresses are encoded into the X.509
// SubjectAltName extension and threaded through
// gowolf.Wc_SetAltNamesBuffer. The encoder builds the
// SEQUENCE OF GeneralName ASN.1 DER from the Go slices; DNS
// names are tagged [2] and IP addresses (parsed as IPv4 or IPv6)
// are tagged [7] per RFC 5280.
type CertConfig struct {
	CommonName   string
	Organization string
	DNSNames     []string
	IPAddresses  []string
	DaysValid    int
	IsCA         bool
	ExtKeyUsage  string // "serverAuth", "clientAuth", or ""
}

// Cert is the output of MintCert: the X.509 cert in DER, the
// matching ECC private key in DER (SEC1 ECPrivateKey), and the
// public key in SEC1 uncompressed form (0x04 || X || Y, 65 bytes).
type Cert struct {
	CertDER []byte
	KeyDER  []byte
	PubSEC1 []byte
}

// SHA256 returns the SHA-256 digest of data via go-wolfssl.
func SHA256(data []byte) ([]byte, error) {
	out := make([]byte, sha256DigestSize)
	if rc := gowolf.Wc_Sha256Hash(data, len(data), out); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.SHA256: Wc_Sha256Hash: %d", rc)
	}
	return out, nil
}

// MintCert generates a fresh ECC P-256 keypair, builds an X.509
// certificate per cfg, and signs it. If signer is nil the cert is
// self-signed (typical CA case). If signer is non-nil the cert is
// signed by signer's CA key with signer's subject becoming the
// issuer (typical leaf case).
func MintCert(cfg CertConfig, signer *Cert) (*Cert, error) {
	if cfg.CommonName == "" {
		return nil, errors.New("wolfcrypt.MintCert: CommonName is required")
	}
	if cfg.DaysValid <= 0 {
		return nil, errors.New("wolfcrypt.MintCert: DaysValid must be positive")
	}

	var rng gowolf.WC_RNG
	if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_InitRng: %d", rc)
	}
	defer gowolf.Wc_FreeRng(&rng)

	// New keypair for this cert.
	var newKey gowolf.Ecc_key
	if rc := gowolf.Wc_ecc_init(&newKey); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_ecc_init(newKey): %d", rc)
	}
	defer gowolf.Wc_ecc_free(&newKey)
	if rc := gowolf.Wc_ecc_make_key(&rng, 32, &newKey); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_ecc_make_key: %d", rc)
	}

	// Export public key as SEC1 uncompressed.
	pubBuf := make([]byte, 128)
	pubLen := len(pubBuf)
	if rc := gowolf.Wc_ecc_export_x963_ex(&newKey, pubBuf, &pubLen, 0); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_ecc_export_x963_ex: %d", rc)
	}
	pubSEC1 := append([]byte{}, pubBuf[:pubLen]...)

	// Export private key as SEC1 ECPrivateKey DER.
	keyBuf := make([]byte, 256)
	keyDerSz := gowolf.Wc_EccKeyToDer(&newKey, keyBuf)
	if keyDerSz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_EccKeyToDer: %d", keyDerSz)
	}
	keyDER := append([]byte{}, keyBuf[:keyDerSz]...)

	// Build the Cert struct.
	var cert gowolf.Cert
	if rc := gowolf.Wc_InitCert(&cert); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_InitCert: %d", rc)
	}
	gowolf.Wc_SetSubjectCN_Org(&cert, cfg.CommonName, cfg.Organization)
	gowolf.Wc_SetCertValidity(&cert, cfg.DaysValid, cfg.IsCA, gowolf.CTC_SHA256wECDSA)

	if cfg.ExtKeyUsage != "" {
		if rc := gowolf.Wc_SetExtKeyUsage(&cert, cfg.ExtKeyUsage); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_SetExtKeyUsage(%q): %d", cfg.ExtKeyUsage, rc)
		}
	}

	if len(cfg.DNSNames) > 0 || len(cfg.IPAddresses) > 0 {
		san, err := encodeSANExtensionDER(cfg.DNSNames, cfg.IPAddresses)
		if err != nil {
			return nil, fmt.Errorf("wolfcrypt.MintCert: encode SAN: %w", err)
		}
		if rc := gowolf.Wc_SetAltNamesBuffer(&cert, san, len(san)); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_SetAltNamesBuffer: %d", rc)
		}
	}

	// Decode the signing key (self-signed uses the new key; CA-signed decodes signer.KeyDER).
	var signKey *gowolf.Ecc_key
	if signer == nil {
		signKey = &newKey
	} else {
		if rc := gowolf.Wc_SetIssuerBuffer(&cert, signer.CertDER, len(signer.CertDER)); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_SetIssuerBuffer: %d", rc)
		}
		var caKey gowolf.Ecc_key
		if rc := gowolf.Wc_ecc_init(&caKey); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_ecc_init(caKey): %d", rc)
		}
		defer gowolf.Wc_ecc_free(&caKey)
		idx := 0
		if rc := gowolf.Wc_EccPrivateKeyDecode(signer.KeyDER, &idx, &caKey, len(signer.KeyDER)); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_EccPrivateKeyDecode(signer): %d", rc)
		}
		signKey = &caKey
	}

	derBuf := make([]byte, 4096)
	bodySz := gowolf.Wc_MakeCert(&cert, derBuf, len(derBuf), nil, &newKey, &rng)
	if bodySz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_MakeCert: %d", bodySz)
	}
	totalSz := gowolf.Wc_SignCert(bodySz, gowolf.CTC_SHA256wECDSA, derBuf, len(derBuf), nil, signKey, &rng)
	if totalSz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: Wc_SignCert: %d", totalSz)
	}
	certDER := append([]byte{}, derBuf[:totalSz]...)

	return &Cert{
		CertDER: certDER,
		KeyDER:  keyDER,
		PubSEC1: pubSEC1,
	}, nil
}

/*
 * encodeSANExtensionDER builds the SubjectAltName extension's
 * SEQUENCE OF GeneralName ASN.1 DER from DNS names and IP
 * address strings. Wire format only - no crypto.
 *
 * Per RFC 5280:
 *   GeneralName ::= CHOICE {
 *       ...
 *       dNSName    [2] IA5String,
 *       ...
 *       iPAddress  [7] OCTET STRING (4 bytes for IPv4, 16 for IPv6)
 *   }
 */
func encodeSANExtensionDER(dnsNames []string,
	ipAddresses []string) ([]byte, error) {
	var body []byte
	for _, d := range dnsNames {
		if d == "" {
			continue
		}
		body = appendASN1Contextual(body, 2, []byte(d))
	}
	for _, s := range ipAddresses {
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf(
				"SAN: %q is not a valid IP address", s)
		}
		if v4 := ip.To4(); v4 != nil {
			body = appendASN1Contextual(body, 7, v4)
		} else {
			body = appendASN1Contextual(body, 7, ip.To16())
		}
	}
	return appendASN1Sequence(body), nil
}

/*
 * appendASN1Contextual writes a primitive context-specific tag
 * (class=10, tag number `tag`) carrying `value`. The tag byte is
 * 0x80 | tag for the primitive form.
 */
func appendASN1Contextual(out []byte, tag byte, value []byte) []byte {
	out = append(out, 0x80|tag)
	out = appendASN1Length(out, len(value))
	out = append(out, value...)
	return out
}

func appendASN1Sequence(body []byte) []byte {
	out := []byte{0x30}
	out = appendASN1Length(out, len(body))
	return append(out, body...)
}

func appendASN1Length(out []byte, n int) []byte {
	if n < 0x80 {
		return append(out, byte(n))
	}
	/* Long form: 0x80|<num length bytes> then BE length bytes. */
	var lenBuf [4]byte
	count := 0
	for tmp := n; tmp > 0; tmp >>= 8 {
		lenBuf[count] = byte(tmp & 0xff)
		count++
	}
	out = append(out, 0x80|byte(count))
	for i := count - 1; i >= 0; i-- {
		out = append(out, lenBuf[i])
	}
	return out
}
