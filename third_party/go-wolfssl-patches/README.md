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
