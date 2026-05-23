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

## 0005-add-certgen-wrappers.patch (UPDATED in Phase 10.9)

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
is non-empty and starts with the SEQUENCE tag. Phase 10.9
grew testcerts.go on top of these wrappers; the wider mTLS
test surface across wolfCI is the de-facto integration gate.

Phase 10.9 additions to this patch:

  Wc_SetAltNamesBuffer     Go wrapper that copies a
                           SubjectAltName extension body
                           (SEQUENCE OF GeneralName, no outer
                           extension wrapper) into
                           Cert.altNames.

  wolfci_cert_set_altnames C-side static helper backing the
                           above. The bundled
                           wc_SetAltNamesBuffer in wolfSSL is
                           mis-named for cert-build use cases:
                           it parses a FULL certificate DER
                           and copies the SANs out of THAT,
                           rather than accepting a SAN
                           extension blob we just encoded
                           from Go slices. This helper does
                           the memcpy + altNamesSz assignment
                           directly. Required because wolfCI
                           encodes the SAN extension in pure
                           Go (DNS [2] / IP [7] per RFC 5280)
                           and needs to push the bytes into
                           the Cert struct in one shot.

Requires the wolfSSL build to define WOLFSSL_ALT_NAMES;
scripts/build-wolfssl.sh passes that via CPPFLAGS and
scripts/test-build-wolfssl.sh gates the define.

**What would let us drop this patch:** same as 0003 / 0004 -
upstream PR merges + a release tag lands. The Phase 10.9
additions might prompt a discussion with wolfSSL maintainers
about whether the upstream wc_SetAltNamesBuffer should grow
a sibling that takes a raw extension blob (matching what
build-time callers actually need).

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

## 0007-add-wolfssh-sshwire-parser.patch

Adds `third_party/go-wolfssl/wolfssh/sshwire.go`, a pure-Go (no
CGO) SSH wire-format parser that handles the OpenSSH
authorized_keys text format and the RFC 4253 wire envelope
formats:

  ParseAuthorizedKey(data)          parse "<algo> <base64> [comment]"
  ParseSSHWirePublicKey(algo, blob) parse the per-algo wire body
  ParseSSHSignature(b)              parse "string algo || string blob"
  ReadSSHString / ReadSSHMpint      wire primitive helpers
  EncodeSSHString                   inverse of ReadSSHString
  EncodeSSHAuthorizedKey            render an authorized_keys line
  EncodeSSHEd25519AuthorizedKey     ed25519-specific shortcut
  EncodeSSHEd25519Signature         signature envelope shortcut
  EncodeECDSASignatureDER           SSH (r, s) -> DER for wolfCrypt
  PublicKey struct                  parsed public key with algo-
                                    specific fields (Ed25519,
                                    ECDSAPoint, RSAModulus,
                                    RSAExponent)
  Alg* constants                    "ssh-ed25519",
                                    "ecdsa-sha2-nistp256",
                                    "ssh-rsa", "rsa-sha2-256"

Three SSH key algorithms are supported: ssh-ed25519,
ecdsa-sha2-nistp256, and ssh-rsa.

**Why:** wolfssh's C-side `wolfSSH_ReadPublicKey_buffer` cannot
identify an authorized_keys-format public key today: its
`IdentifyOpenSshKey` requires the `openssl-key-v1` magic that
only exists in OpenSSH PRIVATE key files, not in authorized_keys
public-key blobs (which are the SSH wire format
`string(algo) || string(pubkey)` after base64 decode). Until
wolfssh upstream fixes this, the Go binding fills the gap.

The implementation is pure Go because all of it is wire-format
parsing - no cryptography. The wolfssh sub-package's CGO bridge
(in wolfssh.go from patch 0006) is unaffected; a caller that
imports only the sshwire pieces pays no cgo cost.

**Gate:** existing internal/auth tests in wolfCI exercise the
parser end-to-end: TestKeyStore_VerifySignature drives a real
wolfcrypt-generated Ed25519 key through
gowolfssh.ParseAuthorizedKey then verifies a signature via
internal/wolfcrypt.Ed25519Verify. The same chain covers
ParseSSHSignature, ReadSSHMpint, EncodeECDSASignatureDER, and
the encoder helpers.

**What would let us drop this patch:** wolfssh upstream fixes
`IdentifyOpenSshKey` to recognize the SSH wire format for
authorized_keys public keys, OR adds a new
`wolfSSH_ReadAuthorizedKey_buffer` function that does this
directly. Then the parsing helpers in this file become thin
wrappers over the wolfssh C calls, and eventually drop out
entirely.

## 0008-add-wolfssh-agent.patch

Adds two files to the wolfssh sub-package:

  `wolfssh/agent.go`      - pure-Go SSH agent-protocol
                            implementation (no CGO).
  `wolfssh/agent_test.go` - end-to-end gate that mints an
                            Ed25519 keypair via the wolfCrypt
                            wrappers in the root go-wolfssl
                            package, runs an
                            AGENTC_REQUEST_IDENTITIES round
                            trip, signs a challenge through
                            an AGENTC_SIGN_REQUEST, then
                            verifies the resulting signature
                            with wolfCrypt's Ed25519 verifier.

Contents:

  Constants AgentMsg* mirror the MSGID_AGENT_* enum in
  `third_party/wolfssh/wolfssh/agent.h` byte for byte. Today
  we ship the (REQUEST_IDENTITIES, SIGN_REQUEST) request half
  + (IDENTITIES_ANSWER, SIGN_RESPONSE, FAILURE) response half;
  ADD_IDENTITY / REMOVE_IDENTITY / LOCK / UNLOCK / EXTENSION
  return AgentMsgFailure so a misbehaving client cannot hang
  the agent.

  AgentSigner - the per-identity signing primitive type. The
  caller passes a closure that wraps whichever wolfCrypt
  routine matches the identity's algorithm (Wc_ed25519_sign_msg
  for ssh-ed25519, Wc_SignatureGenerate for RSA / ECDSA, ...);
  agent.go itself stays algorithm-agnostic so the file pays
  no CGO cost.

  AgentIdentity - one entry: KeyBlob (the algorithm-specific
  body of the SSH wire public key), Algorithm (e.g.
  "ssh-ed25519"), Comment (the "user@host" string the agent
  reports), Sign (the AgentSigner closure).

  Agent + NewAgent / AddIdentity / Identities / RemoveAll /
  HandleMessage / HandleFramedMessage. The agent is
  goroutine-safe (sync.RWMutex) so parallel branches of a
  wolfCI build can share one. HandleMessage takes the
  unframed message body and returns the unframed response
  body; HandleFramedMessage wraps both ends in the
  uint32-big-endian length prefix net.Conn-style transports
  use.

  Wire shape follows draft-miller-ssh-agent section 4 and
  matches what wolfssh's C-side agent.c produces:

    SSH_AGENT_IDENTITIES_ANSWER:
      byte    SSH_AGENT_IDENTITIES_ANSWER
      uint32  num_keys
      repeated num_keys times:
        string key_blob       (string(algo) || body)
        string comment

    SSH_AGENT_SIGN_RESPONSE:
      byte    SSH_AGENT_SIGN_RESPONSE
      string  signature_blob  (string(algo) || string(sig))

**Why:** PLAN.md 18.19 requires a wolfssh-backed SSH agent so
the 18.20 `sshagent` step (master-job's `git clone git@...`
via SSH) does not have to fall through to OpenSSH's ssh-agent
binary. CLAUDE.md Hard Rule #11 says missing wolfssh wrappers
go into the vendored copy via a numbered patch. wolfssh's
C-side agent state machine (agent.c, behind WOLFSSH_AGENT)
implements the same protocol but does not expose a stable
C ABI we can bridge to Go without a churn-prone CGO shim; a
pure-Go protocol layer on top of the wolfCrypt signing
primitives (already wired via patch 0003) ships the same
wire surface today and shrinks to a CGO bridge later when
wolfssh upstream exposes the state machine.

**Gate:** `internal/authssh/gowolfssh_agent_smoke_test.go`
ships `TestGoWolfSSH_AgentAddListSign_Callable` (mints an
ssh-ed25519 identity, drives AGENTC_REQUEST_IDENTITIES +
AGENTC_SIGN_REQUEST through the agent, verifies the
resulting signature with the wolfCrypt verifier) so the
wrapper stays gated from wolfCI's test suite even if the
in-submodule tests are skipped on a fresh clone. The
in-submodule TestAgent_AddListSign +
TestAgent_SignRequestUnknownKeyFails +
TestAgent_HandleFramedMessage_RoundTrip cover the same
ground at the wrapper layer.

**What would let us drop this patch:** the project owner files
the upstream PR carrying patches 0003-0008 at
https://github.com/wolfSSL/go-wolfssl and the wolfssh agent
file lands plus a release tag is cut. OR wolfssh upstream
exposes a stable C-ABI agent state machine through
agent.h, at which point this file shrinks to a thin CGO
bridge.
