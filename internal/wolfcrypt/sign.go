package wolfcrypt

import (
	"errors"
	"fmt"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// Ed25519GenKey generates a fresh Ed25519 keypair via go-wolfssl.
// Returns the 32-byte public key and a 64-byte private key in the
// stdlib convention: seed (32) || public (32). Ed25519Sign accepts
// the 64-byte form directly.
func Ed25519GenKey() (publicKey, privateKey []byte, err error) {
	var rng gowolf.WC_RNG
	if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: Wc_InitRng: %d", rc)
	}
	defer gowolf.Wc_FreeRng(&rng)

	var key gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: Wc_ed25519_init: %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&key)

	if rc := gowolf.Wc_ed25519_make_key(&rng, 32, &key); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: Wc_ed25519_make_key: %d", rc)
	}

	pub := make([]byte, ed25519PublicKeySize)
	pubLen := ed25519PublicKeySize
	if rc := gowolf.Wc_ed25519_export_public(&key, pub, &pubLen); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: Wc_ed25519_export_public: %d", rc)
	}

	priv := make([]byte, 64)
	privSeedLen := 32
	if rc := gowolf.Wc_ed25519_export_private_only(&key, priv, &privSeedLen); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: Wc_ed25519_export_private_only: %d", rc)
	}
	copy(priv[32:], pub)
	return pub, priv, nil
}

// Ed25519Sign signs message with the 64-byte private key returned
// by Ed25519GenKey (seed || public). Output is the 64-byte
// Ed25519 signature.
func Ed25519Sign(privateKey, message []byte) ([]byte, error) {
	if len(privateKey) != 64 {
		return nil, errors.New("wolfcrypt.Ed25519Sign: private key must be 64 bytes (seed || pub)")
	}
	var key gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: Wc_ed25519_init: %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&key)

	seed := privateKey[:32]
	pub := privateKey[32:]
	if rc := gowolf.Wc_ed25519_import_private_key(seed, 32, pub, 32, &key); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: Wc_ed25519_import_private_key: %d", rc)
	}

	sig := make([]byte, 64)
	sigLen := 64
	if rc := gowolf.Wc_ed25519_sign_msg(message, len(message), sig, &sigLen, &key); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: Wc_ed25519_sign_msg: %d", rc)
	}
	return sig[:sigLen], nil
}
