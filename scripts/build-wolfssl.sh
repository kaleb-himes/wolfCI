#!/bin/sh
# scripts/build-wolfssl.sh - build the vendored wolfSSL submodule
# with the wolfCI server profile.
#
# Profile (CI server, non-FIPS, static):
#   - TLS 1.2 and 1.3 only (no SSLv3, no TLS 1.0, no TLS 1.1)
#   - AEAD: AES-GCM, ChaCha20-Poly1305
#   - Modern asymmetric: ECDH/ECDSA, Ed25519, Curve25519
#   - SNI, ALPN, session tickets
#   - Static library only; wolfCI ships as a single binary
#
# Output (relative to repo root):
#   build/wolfssl-install/lib/libwolfssl.a
#   build/wolfssl-install/include/wolfssl/...
#
# Set WOLFSSL_FIPS=1 to opt into the FIPS profile. The FIPS profile
# is not yet wired up (planned post-Phase 1); the script will exit
# with status 2 in that case.

set -eu

# Refuse FIPS before doing any work, so the toggle is observable
# without paying the build cost.
if [ "${WOLFSSL_FIPS:-0}" = "1" ]; then
    echo "build-wolfssl.sh: FIPS profile is not yet wired up (planned post-Phase 1)." >&2
    exit 2
fi

REPO_ROOT=$(pwd)
WOLFSSL_DIR="$REPO_ROOT/third_party/wolfssl"
INSTALL_PREFIX="$REPO_ROOT/build/wolfssl-install"

if [ ! -d "$WOLFSSL_DIR/.git" ] && [ ! -f "$WOLFSSL_DIR/.git" ]; then
    echo "build-wolfssl.sh: $WOLFSSL_DIR missing; run 'git submodule update --init' first" >&2
    exit 1
fi

mkdir -p "$INSTALL_PREFIX"

# Match Go's idea of GOOS/GOARCH so the resulting .a links cleanly
# against the wolfCI Go binary. Without this, an Apple Silicon host
# running a darwin/amd64 Go (common via Homebrew on Intel-era
# installs) ends up with an arm64 libwolfssl.a that fails to link.
TARGET_GOOS=${TARGET_GOOS:-$(go env GOOS)}
TARGET_GOARCH=${TARGET_GOARCH:-$(go env GOARCH)}

case "$TARGET_GOOS-$TARGET_GOARCH" in
    darwin-amd64)
        target_arch="x86_64"
        configure_host="x86_64-apple-darwin"
        ;;
    darwin-arm64)
        target_arch="arm64"
        configure_host="arm64-apple-darwin"
        ;;
    linux-amd64)
        target_arch=""
        configure_host=""
        ;;
    linux-arm64)
        target_arch=""
        configure_host="aarch64-linux-gnu"
        ;;
    *)
        echo "build-wolfssl.sh: unsupported target $TARGET_GOOS/$TARGET_GOARCH" >&2
        exit 1
        ;;
esac

cd "$WOLFSSL_DIR"

# Always regenerate the autotools state. Skipping when configure
# already exists turned out to be fragile: a partial autogen run
# leaves configure but no install-sh, which then fails the subsequent
# configure step in a confusing way. The autogen step is cheap.
./autogen.sh >/dev/null

# Wipe any prior build artifacts so a target-arch change does not
# leave stray object files from the previous arch.
make distclean >/dev/null 2>&1 || true
./autogen.sh >/dev/null

if [ "$TARGET_GOOS" = "darwin" ] && [ -n "$target_arch" ]; then
    export CFLAGS="-arch $target_arch ${CFLAGS:-}"
    export LDFLAGS="-arch $target_arch ${LDFLAGS:-}"
fi

configure_extra=""
if [ -n "$configure_host" ]; then
    configure_extra="--host=$configure_host"
fi

# shellcheck disable=SC2086
./configure \
    --prefix="$INSTALL_PREFIX" \
    $configure_extra \
    --enable-static \
    --disable-shared \
    --enable-tls13 \
    --disable-sslv3 \
    --disable-oldtls \
    --enable-aesgcm \
    --enable-chacha \
    --enable-poly1305 \
    --enable-ecc \
    --enable-curve25519 \
    --enable-ed25519 \
    --enable-sni \
    --enable-alpn \
    --enable-session-ticket \
    --enable-supportedcurves \
    --enable-keygen \
    --enable-certgen \
    --enable-certreq \
    --disable-examples \
    --disable-crypttests >/dev/null

jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)
make -j"$jobs" >/dev/null
make install >/dev/null

echo "build-wolfssl.sh: built and installed to $INSTALL_PREFIX"
