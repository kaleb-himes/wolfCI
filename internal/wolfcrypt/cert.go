package wolfcrypt

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/wolfcrypt/ecc.h>
#include <wolfssl/wolfcrypt/asn.h>
#include <wolfssl/wolfcrypt/asn_public.h>
#include <wolfssl/wolfcrypt/hash.h>
#include <wolfssl/wolfcrypt/random.h>
#include <string.h>
#include <stdlib.h>

// wolfci_cert_set_subject copies the printable fields of a CertName
// from C strings. Doing this in C avoids fiddly Go-to-C string slice
// copies into the fixed-size char arrays in Cert.subject.
static void wolfci_cert_set_subject(Cert* cert, const char* cn, const char* org) {
    if (cn && cn[0]) {
        strncpy(cert->subject.commonName, cn, CTC_NAME_SIZE - 1);
        cert->subject.commonName[CTC_NAME_SIZE - 1] = '\0';
        cert->subject.commonNameEnc = CTC_UTF8;
    }
    if (org && org[0]) {
        strncpy(cert->subject.org, org, CTC_NAME_SIZE - 1);
        cert->subject.org[CTC_NAME_SIZE - 1] = '\0';
        cert->subject.orgEnc = CTC_UTF8;
    }
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// CertConfig is the input to MintCert. CommonName and DaysValid
// are required.
//
// DNSNames and IPAddresses are accepted but currently ignored:
// wolfCrypt's wc_SetAltNames* functions take pre-encoded DER, not
// the string form Go callers tend to want. 10.5 (testcerts
// replacement) lands a helper that encodes the SAN extension DER
// from these slices and threads it through here.
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
// PubSEC1 is convenient for callers that want to verify a signature
// produced by this cert's signer via wolfcrypt.ECCVerifyP256
// without re-parsing the cert.
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

	// RNG.
	var rng C.WC_RNG
	if rc := C.wc_InitRng(&rng); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_InitRng: %d", int(rc))
	}
	defer C.wc_FreeRng(&rng)

	// New keypair for this cert.
	var newKey C.ecc_key
	if rc := C.wc_ecc_init(&newKey); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_ecc_init(newKey): %d", int(rc))
	}
	defer C.wc_ecc_free(&newKey)

	if rc := C.wc_ecc_make_key(&rng, 32, &newKey); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_ecc_make_key: %d", int(rc))
	}

	// Export the new public key as SEC1 uncompressed (0x04 || X || Y, 65 bytes for P-256).
	pubBuf := make([]byte, 128)
	pubLen := C.word32(len(pubBuf))
	if rc := C.wc_ecc_export_x963(
		&newKey,
		(*C.byte)(unsafe.Pointer(&pubBuf[0])),
		&pubLen,
	); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_ecc_export_x963: %d", int(rc))
	}
	pubSEC1 := append([]byte{}, pubBuf[:pubLen]...)

	// Export the private key as SEC1 ECPrivateKey DER.
	keyBuf := make([]byte, 256)
	keyDerSz := C.wc_EccKeyToDer(
		&newKey,
		(*C.byte)(unsafe.Pointer(&keyBuf[0])),
		C.word32(len(keyBuf)),
	)
	if keyDerSz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_EccKeyToDer: %d", int(keyDerSz))
	}
	keyDER := append([]byte{}, keyBuf[:keyDerSz]...)

	// Build Cert struct.
	var cert C.Cert
	if rc := C.wc_InitCert(&cert); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_InitCert: %d", int(rc))
	}

	cn := C.CString(cfg.CommonName)
	defer C.free(unsafe.Pointer(cn))
	var org *C.char
	if cfg.Organization != "" {
		org = C.CString(cfg.Organization)
		defer C.free(unsafe.Pointer(org))
	}
	C.wolfci_cert_set_subject(&cert, cn, org)

	cert.daysValid = C.int(cfg.DaysValid)
	cert.sigType = C.CTC_SHA256wECDSA
	if cfg.IsCA {
		cert.isCA = 1
	}

	// EKU + SAN if requested.
	if cfg.ExtKeyUsage != "" {
		eku := C.CString(cfg.ExtKeyUsage)
		defer C.free(unsafe.Pointer(eku))
		if rc := C.wc_SetExtKeyUsage(&cert, eku); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: wc_SetExtKeyUsage(%q): %d", cfg.ExtKeyUsage, int(rc))
		}
	}
	// Choose signing key + issuer.
	var signKey *C.ecc_key
	if signer == nil {
		signKey = &newKey // self-signed
	} else {
		if rc := C.wc_SetIssuerBuffer(
			&cert,
			(*C.byte)(unsafe.Pointer(&signer.CertDER[0])),
			C.int(len(signer.CertDER)),
		); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: wc_SetIssuerBuffer: %d", int(rc))
		}
		var caKey C.ecc_key
		if rc := C.wc_ecc_init(&caKey); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: wc_ecc_init(caKey): %d", int(rc))
		}
		defer C.wc_ecc_free(&caKey)
		var idx C.word32 = 0
		rc := C.wc_EccPrivateKeyDecode(
			(*C.byte)(unsafe.Pointer(&signer.KeyDER[0])),
			&idx,
			&caKey,
			C.word32(len(signer.KeyDER)),
		)
		if rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.MintCert: wc_EccPrivateKeyDecode(signer): %d", int(rc))
		}
		signKey = &caKey
	}

	// Make + sign.
	derBuf := make([]byte, 4096)
	bodySz := C.wc_MakeCert(
		&cert,
		(*C.byte)(unsafe.Pointer(&derBuf[0])),
		C.word32(len(derBuf)),
		nil,
		&newKey,
		&rng,
	)
	if bodySz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_MakeCert: %d", int(bodySz))
	}
	totalSz := C.wc_SignCert(
		bodySz,
		C.int(cert.sigType),
		(*C.byte)(unsafe.Pointer(&derBuf[0])),
		C.word32(len(derBuf)),
		nil,
		signKey,
		&rng,
	)
	if totalSz < 0 {
		return nil, fmt.Errorf("wolfcrypt.MintCert: wc_SignCert: %d", int(totalSz))
	}
	certDER := append([]byte{}, derBuf[:totalSz]...)

	return &Cert{
		CertDER: certDER,
		KeyDER:  keyDER,
		PubSEC1: pubSEC1,
	}, nil
}

