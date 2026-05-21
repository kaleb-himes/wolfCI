#!/bin/sh
# scripts/build.sh - build wolfCI binaries.
#
# Produces three binaries linked against the vendored wolfSSL static
# library under build/wolfssl-install/:
#
#   build/bin/<goos>-<goarch>/wolfci
#   build/bin/<goos>-<goarch>/wolfci-agent
#   build/bin/<goos>-<goarch>/wolfci-ctl
#
# By default builds for the host platform. To target a different
# platform, set GOOS and GOARCH and make sure two prerequisites are
# in place:
#
#   1. scripts/build-wolfssl.sh has been run with matching
#      TARGET_GOOS / TARGET_GOARCH so the right libwolfssl.a sits
#      under build/wolfssl-install/lib/.
#   2. CC points at a CGO-capable cross compiler for the target.
#
# Phase 9.1 lists linux/amd64 as the release target and darwin/arm64
# as the development target. Recipes:
#
#   # darwin/arm64 development binary (from a darwin/arm64 host)
#   TARGET_GOOS=darwin TARGET_GOARCH=arm64 scripts/build-wolfssl.sh
#   GOOS=darwin GOARCH=arm64 scripts/build.sh
#
#   # linux/amd64 release binary (requires a static-capable
#   # cross-toolchain such as x86_64-linux-musl-gcc)
#   TARGET_GOOS=linux TARGET_GOARCH=amd64 scripts/build-wolfssl.sh
#   GOOS=linux GOARCH=amd64 CC=x86_64-linux-musl-gcc scripts/build.sh
#
# Set WOLFCI_BUILD_VERSION to stamp a release tag into the
# wolfci-ctl "version" output via -ldflags -X. Defaults to "dev".

set -eu

cd "$(dirname "$0")/.."

REPO_ROOT=$(pwd)
WOLFSSL_INSTALL="$REPO_ROOT/build/wolfssl-install"

if [ ! -f "$WOLFSSL_INSTALL/lib/libwolfssl.a" ]; then
    echo "build.sh: $WOLFSSL_INSTALL/lib/libwolfssl.a not found." >&2
    echo "build.sh: run scripts/build-wolfssl.sh first." >&2
    exit 1
fi

GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}
export GOOS GOARCH

BINDIR="$REPO_ROOT/build/bin/$GOOS-$GOARCH"
mkdir -p "$BINDIR"

VERSION=${WOLFCI_BUILD_VERSION:-dev}

# wolfSSL is consumed via CGO. Pin the vendored static lib so the
# build does not silently link the system wolfSSL (if any).
export CGO_ENABLED=1
export CGO_CFLAGS="-I$WOLFSSL_INSTALL/include ${CGO_CFLAGS:-}"
export CGO_LDFLAGS="-L$WOLFSSL_INSTALL/lib ${CGO_LDFLAGS:-}"

# -s -w strip the symbol table and DWARF; -X main.version stamps the
# release tag (silently ignored by binaries that have no such
# symbol, so the same flag set covers wolfci, wolfci-agent, and
# wolfci-ctl). -trimpath removes the build path prefix so two boxes
# building the same source tree produce closer-to-identical output.
LDFLAGS="-s -w -X main.version=$VERSION"

build_one() {
    src_dir=$1
    bin_name=$2
    out_path="$BINDIR/$bin_name"
    echo "build.sh: $GOOS/$GOARCH -> $out_path"
    go build -trimpath -ldflags "$LDFLAGS" -o "$out_path" "./$src_dir"
}

build_one cmd/wolfci        wolfci
build_one cmd/wolfci-agent  wolfci-agent
build_one cmd/wolfci-ctl    wolfci-ctl

echo "build.sh: $GOOS/$GOARCH binaries in $BINDIR"
