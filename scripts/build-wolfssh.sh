#!/bin/sh
# scripts/build-wolfssh.sh - build the vendored wolfssh submodule.
#
# Profile (wolfCI SSH-key validation, no client / server / sftp /
# scp / sshd / shell, static only):
#   - linked against the wolfSSL static lib at
#     build/wolfssl-install/ (so wolfssh consumes the wolfCrypt
#     primitives wolfCI already built).
#   - static library only; wolfCI ships as a single binary.
#   - examples / tests off (we do not run the wolfssh client or
#     server from this tree).
#
# Output (relative to repo root):
#   build/wolfssh-install/lib/libwolfssh.a
#   build/wolfssh-install/include/wolfssh/...

set -eu

REPO_ROOT=$(pwd)
WOLFSSH_DIR="$REPO_ROOT/third_party/wolfssh"
WOLFSSL_PREFIX="$REPO_ROOT/build/wolfssl-install"
INSTALL_PREFIX="$REPO_ROOT/build/wolfssh-install"

if [ ! -d "$WOLFSSH_DIR/.git" ] && [ ! -f "$WOLFSSH_DIR/.git" ]; then
    echo "build-wolfssh.sh: $WOLFSSH_DIR missing; run 'git submodule update --init' first" >&2
    exit 1
fi
if [ ! -f "$WOLFSSL_PREFIX/lib/libwolfssl.a" ]; then
    echo "build-wolfssh.sh: $WOLFSSL_PREFIX/lib/libwolfssl.a missing; run scripts/build-wolfssl.sh first" >&2
    exit 1
fi

mkdir -p "$INSTALL_PREFIX"

# Mirror build-wolfssl.sh's host/arch handling so a darwin/amd64
# Go on Apple Silicon hardware produces an x86_64 libwolfssh.a
# that links cleanly into the matching wolfCI binary.
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
        echo "build-wolfssh.sh: unsupported target $TARGET_GOOS/$TARGET_GOARCH" >&2
        exit 1
        ;;
esac

cd "$WOLFSSH_DIR"

./autogen.sh >/dev/null
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
    --with-wolfssl="$WOLFSSL_PREFIX" \
    $configure_extra \
    --enable-static \
    --disable-shared \
    --disable-examples >/dev/null

jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)
make -j"$jobs" >/dev/null
make install >/dev/null

echo "build-wolfssh.sh: built and installed to $INSTALL_PREFIX"
