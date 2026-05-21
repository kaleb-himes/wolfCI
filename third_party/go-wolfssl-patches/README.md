# go-wolfssl local patches

Each `.patch` file in this directory is a `git diff` produced against
the SHA recorded in `third_party/go-wolfssl-version.txt`. The patches
are auto-applied by `scripts/test-go-wolfssl.sh` if it detects that
the submodule checkout is unmodified.

Why local patches at all: CLAUDE.md Hard Rule #11 prefers vendoring
upstream as-is. Each patch here is a workaround for an upstream bug
or an upstream feature we cannot use; the patch file's header
comment must spell out why the patch exists and what would let us
remove it.

## 0001-exclude-openssl-compat-files.patch

Adds `//go:build wolfci_use_openssl_compat` build-tag headers to
`x509.go` and `ssl.go` so those two files are excluded from default
compilations.

**Why:**
  - `x509.go` is a Go binding for wolfSSL's OpenSSL-compatibility
    X.509 surface (`#include <wolfssl/openssl/x509.h>` and the
    `WolfSSL_X509_*` / `X509_STORE_*` family). CLAUDE.md Hard Rule
    #12 forbids OpenSSL-compat APIs in wolfCI. wolfCI's X.509
    needs are met by `internal/wolfcrypt.MintCert`, which uses
    only the native `wc_MakeCert` / `wc_SignCert` API.
  - `ssl.go` references types defined in `x509.go`
    (`WOLFSSL_X509`, `WOLFSSL_X509_CHAIN`, etc.), so excluding
    `x509.go` would break the package without also excluding
    `ssl.go`. The TLS surface in `ssl.go` is also not needed:
    wolfCI's TLS path lives in `internal/tlsutil` (a CGO bridge
    over `wolfSSL_CTX_new` plus custom I/O callbacks).

**What would let us drop this patch:** the project owner (or a
go-wolfssl upstream maintainer) decides to split the OpenSSL-compat
files into a separate sub-package, OR adds an upstream build tag
that achieves the same exclusion. Until then this patch is the
minimum invasive workaround that satisfies Hard Rule #12.

## 0002-wolfci-cgo-directives.patch

Adds wolfCI-specific `#cgo CFLAGS` / `#cgo LDFLAGS` / `#cgo darwin
LDFLAGS` directives to `random.go`. The flags merge across every
.go file in the `wolfSSL` package per cgo's rules, so injecting
them into one file covers the whole package.

**Why:**
  - go-wolfssl's CGO preamble references `<wolfssl/options.h>` and
    other headers without specifying where they live; it assumes
    wolfSSL is installed on the system's default include + link
    paths. wolfCI vendors wolfSSL under `build/wolfssl-install/`,
    so the headers and libwolfssl.a are not where go-wolfssl
    expects them.
  - The naive workaround (export CGO_CFLAGS / CGO_LDFLAGS before
    `go test`) leaks the framework flags into non-cgo test
    binaries, which on macOS trips dyld's "missing LC_UUID load
    command" check and fails unrelated tests
    (internal/plugin, plugins/email-on-failure).

The injected flags use `${SRCDIR}/../../build/wolfssl-install/`,
which assumes go-wolfssl lives at `third_party/go-wolfssl/`
relative to the wolfCI repo root. That assumption is correct
for wolfCI and wrong for other consumers of go-wolfssl - which
is exactly why this is a local patch, not an upstreamable change.

**What would let us drop this patch:** wolfCI moves wolfSSL out
of `build/wolfssl-install/` and into a path go-wolfssl
recognizes (system include paths), OR go-wolfssl learns a
`WOLFSSL_INSTALL_PREFIX` env var that its `#cgo` directives can
consume.

## 0003-add-ed25519-wrappers.patch

Adds `ed25519.go` to go-wolfssl with Go wrappers for the
wolfCrypt Ed25519 C API:

  Wc_ed25519_init / _free / _make_key
  Wc_ed25519_import_public / _import_private_key
  Wc_ed25519_export_public / _export_private_only
  Wc_ed25519_sign_msg / _verify_msg

Plus the type alias `Ed25519_key = C.struct_ed25519_key`. The
style mirrors go-wolfssl's existing curve25519.go: `Wc_*` Go
function names, `[]byte` for buffers, `BAD_FUNC_ARG` on
empty-input checks, and a `#ifndef HAVE_ED25519` fallback stub
block so the file still compiles on a wolfSSL build without
Ed25519 support.

**Why:** internal/wolfcrypt's Ed25519 path (sign.go +
verify.go's Ed25519Verify) is still hand-rolled CGO as of
Phase 10.6b. CLAUDE.md Hard Rule #11 (extended on 2026-05-21)
says missing go-wolfssl wrappers should be ADDED to the
vendored copy and tracked as patches, not hand-rolled in
wolfCI's tree. This patch is the Ed25519 piece of that work;
patch 0004 covers RSA verify, patch 0005 covers cert
generation. 10.6d then rewires internal/wolfcrypt to use
these wrappers and deletes the corresponding hand-rolled CGO.

**Gate:** `internal/wolfcrypt/gowolf_smoke_test.go` ships
`TestGoWolfSSL_Ed25519_RFC8032_TC2_Verify` (KAT verify) and
`TestGoWolfSSL_Ed25519_RoundTrip` (make_key + sign + verify
round-trip) which exercise every function in the patch.

**What would let us drop this patch:** the project owner files
the upstream PR (carrying this file as-is) at
https://github.com/wolfSSL/go-wolfssl and it merges + a release
tag lands. The submodule pointer advances to that tag and this
patch falls out of the directory.

## 0004-add-rsa-verify-wrappers.patch

Adds `rsa_verify.go` with the RSA + signature-verify wrappers
needed for SSH ssh-rsa / rsa-sha2-256 signature checks:

  Wc_InitRsaKey             wc_InitRsaKey
  Wc_FreeRsaKey             wc_FreeRsaKey
  Wc_RsaPublicKeyDecodeRaw  wc_RsaPublicKeyDecodeRaw  (raw n + e
                            from the SSH wire format, no DER)
  Wc_SignatureVerify        wc_SignatureVerify        (generic
                            verifier, handles RSA + ECC)

Plus the type alias `RsaKey = C.struct_RsaKey` and four
constants exposed as Go ints: `WC_HASH_TYPE_SHA256`,
`WC_SIGNATURE_TYPE_RSA_W_ENC`, `WC_SIGNATURE_TYPE_RSA`,
`WC_SIGNATURE_TYPE_ECC`.

`Wc_SignatureVerify` takes the key as an `interface{}` (rather
than a raw `unsafe.Pointer`) and switches on the concrete type:
`*RsaKey` for the RSA sig types, `*Ecc_key` for ECC. Cgo's
`unsafe.Sizeof(*k)` produces the byte length wc_SignatureVerify
expects in its trailing keyLen parameter. Calling with any
other key type returns `BAD_FUNC_ARG`.

CGO preamble includes `<wolfssl/wolfcrypt/ecc.h>` even though
this file is the RSA file; otherwise cgo treats
`C.struct_ecc_key` as incomplete in this compilation unit and
the switch case above refuses to compile.

**Gate:**
`internal/wolfcrypt/gowolf_smoke_test.go`'s
`TestGoWolfSSL_RSAVerify_KnownVector` drives the four wrappers
against the same OpenSSL-generated RSA-2048 / PKCS#1 v1.5 /
SHA-256 vector verify_test.go uses for the hand-rolled
RSAVerifyPKCS1v15SHA256 path.

**What would let us drop this patch:** same as 0003 - upstream
PR merges and tags.

## 0005-add-certgen-wrappers.patch

Adds `certgen.go` with the X.509 cert-generation wrappers:

  Wc_InitCert              wc_InitCert
  Wc_SetSubjectCN_Org      sets Cert.subject.commonName +
                           Cert.subject.org through a C-side
                           helper (the fields are fixed-size
                           char arrays inside the Cert struct,
                           which is fiddly to assign to from
                           Go directly).
  Wc_SetCertValidity       sets Cert.daysValid + Cert.isCA +
                           Cert.sigType through a C-side
                           helper for the same reason.
  Wc_SetIssuerBuffer       wc_SetIssuerBuffer
  Wc_SetExtKeyUsage        wc_SetExtKeyUsage
  Wc_MakeCert              wc_MakeCert
  Wc_SignCert              wc_SignCert

Plus the type alias `Cert = C.struct_Cert` and two signing
constants exposed as Go ints: `CTC_SHA256wECDSA` and
`CTC_SHA256wRSA`.

The two C-side static helpers
(`wolfci_cert_set_subject_cn_org`, `wolfci_cert_set_validity`)
live in the CGO preamble of this file - they exist so the
public Go API does not have to know the layout of `Cert.subject`
or do its own strncpy + zero-termination dance against
`CTC_NAME_SIZE`. Upstream would presumably name these
`wc_SetCertSubjectCN`, etc. We can rename in the PR.

**Why:** internal/wolfcrypt's MintCert is the last bit of
hand-rolled CGO in our tree. 10.6d uses these wrappers to
delete the `import "C"` block in internal/wolfcrypt/cert.go.

**Gate:** `internal/wolfcrypt/gowolf_smoke_test.go`'s
`TestGoWolfSSL_MakeSelfSignedCert` mints a self-signed ECC
P-256 CA via the new wrappers and asserts the resulting DER
is non-empty and starts with the SEQUENCE tag.

**What would let us drop this patch:** same as 0003 / 0004 -
upstream PR merges + a release tag lands.

## 0006-add-wolfssh-subpackage.patch

Adds the wolfssh sub-package at
`third_party/go-wolfssl/wolfssh/wolfssh.go`. This is the first
sibling sub-package under the root `github.com/wolfssl/go-wolfssl`
module; future wolfMQTT / wolfBoot / wolfTPM / wolfHSM bindings
follow the same layout per CLAUDE.md Hard Rule #11.

Contents:

  Package wolfssh
    FORMAT_ASN1 / FORMAT_PEM / FORMAT_RAW / FORMAT_SSH /
      FORMAT_OPENSSH         Go ints mirroring the
                             WOLFSSH_FORMAT_* enum.
    WolfSSH_ReadPublicKey_buffer(in, format) ->
      (keyBlob, algo, rc)    Calls wc_SSH_ReadPublicKey_buffer
                             with a NULL heap, copies the wolfssh-
                             allocated payload into a Go []byte,
                             returns the wire-format key blob and
                             the algorithm name. cOutType is
                             intentionally NOT freed (it points
                             into wolfssh's static IdToName table).
    Err(rc) -> error         Convenience converter.

CGO setup:

  #cgo CFLAGS:  -I${SRCDIR}/../../../build/wolfssh-install/include
                -I${SRCDIR}/../../../build/wolfssl-install/include
  #cgo LDFLAGS: -L${SRCDIR}/../../../build/wolfssh-install/lib -lwolfssh
                -L${SRCDIR}/../../../build/wolfssl-install/lib -lwolfssl
  #cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

The triple `../../../` is one more `..` than the root package's
CGO directives (patch 0002): the sub-package is one directory
deeper.

**Why:** wolfCI's authorized_keys parser in
`internal/auth/sshkey.go` is wire-format only, but per CLAUDE.md
Hard Rule #11 we prefer wolfSSL ecosystem code over hand-rolled
parsing. Putting wolfssh under `go-wolfssl/wolfssh/` rather than
inside the root package means users who do not need SSH parsing
do not get libwolfssh.a pulled into their build; cgo applies
LDFLAGS only for compiled files. Sub-packages also keep wolfSSL
ecosystem Go bindings consolidated in one repository for the
upstream PR.

**Gate:** `internal/authssh/gowolfssh_smoke_test.go` ships
`TestGoWolfSSH_ReadPublicKey_Callable` (proves the wrapper
compiles, links against libwolfssh.a, and returns without
crashing) and `TestGoWolfSSH_FormatConstants` (pins the
FORMAT_* enum values).

**What would let us drop this patch:** when the
kaleb-himes/go-wolfssl PR carrying patches 0003-0006 merges
upstream and a release tag lands, the submodule pointer advances
and this patch drops from `third_party/go-wolfssl-patches/`.
