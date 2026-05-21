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
