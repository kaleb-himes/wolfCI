# Getting Started with wolfCI

A 10-minute path from "git clone" to "first green build".

Tested on macOS (Apple Silicon and Intel) and Linux (amd64). The
agent and CLI run on the same platforms. Windows is on the backlog.

## What you need

- Go 1.18 or newer (https://go.dev/dl/).
- A C toolchain (clang on macOS, gcc on Linux) with autoconf,
  automake, libtool. macOS: `xcode-select --install` plus
  `brew install autoconf automake libtool`. Debian/Ubuntu:
  `apt install build-essential autoconf automake libtool pkg-config`.
- git.
- About 10 minutes.

Cross-building? You also need a CGO-capable cross compiler for
each target. The recipe at the end of this doc covers it.

## 1. Clone the source (1 min)

```sh
git clone https://github.com/kaleb-himes/wolfCI.git
cd wolfCI
git submodule update --init --recursive
```

The `git submodule` step pulls the vendored wolfSSL under
`third_party/wolfssl/`, pinned to the tag recorded in
`third_party/wolfssl-version.txt` (currently v5.9.1-stable).

## 2. Build wolfSSL (3 min, one-time)

```sh
scripts/build-wolfssl.sh
```

This compiles wolfSSL with the wolfCI server profile (TLS 1.2 + 1.3,
modern AEAD ciphers, ECDH/ECDSA, no SSLv3, no old TLS, static lib
only) and installs to `build/wolfssl-install/`. You only need to
re-run this when the wolfSSL pin changes.

## 3. Build the wolfCI binaries (30 sec)

```sh
scripts/build.sh
```

Output lands under `build/bin/<goos>-<goarch>/`:

- `wolfci` - the server
- `wolfci-agent` - the executor that runs on each worker node
- `wolfci-ctl` - the admin CLI

Stamp a release tag by setting `WOLFCI_BUILD_VERSION`:

```sh
WOLFCI_BUILD_VERSION=v0.1.0 scripts/build.sh
build/bin/$(go env GOOS)-$(go env GOARCH)/wolfci-ctl version
```

## 4. Install the service (1 min)

Pick the template that matches your OS. Both expect the server
binary at `/usr/local/bin/wolfci`, so copy it first.

### Linux (systemd)

```sh
sudo cp build/bin/linux-amd64/wolfci /usr/local/bin/
sudo useradd --system --home /var/lib/wolfci --shell /usr/sbin/nologin wolfci
sudo install -d -o wolfci -g wolfci /var/lib/wolfci
sudo cp scripts/install/systemd/wolfci.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now wolfci
sudo journalctl -u wolfci -f
```

### macOS (launchd, development)

```sh
sudo cp build/bin/darwin-arm64/wolfci /usr/local/bin/
sudo install -d /usr/local/var/wolfci /usr/local/var/log
sudo cp scripts/install/launchd/com.wolfssl.wolfci.plist \
    /Library/LaunchDaemons/
sudo launchctl bootstrap system \
    /Library/LaunchDaemons/com.wolfssl.wolfci.plist
sudo tail -f /usr/local/var/log/wolfci.log
```

Both run as a dedicated unprivileged user and restart on failure.
The systemd unit grants `CAP_NET_BIND_SERVICE` so the server can
bind 443 without root.

## 5. Bootstrap the first admin (2 min)

On first start with no users on disk, the server generates an
SSH keypair, prints the public key path plus a one-time token URL
to stdout, and waits. Watch the journal (systemd) or log file
(launchd); you will see something like:

```
wolfci: no users present. visit the one-time token URL below to
create the first admin account:
    https://localhost:8443/bootstrap?token=...
SSH public key written to /var/lib/wolfci/config-files/auth/keys/admin.pub
```

Open the URL in a browser, create the admin account, save the
credentials. The token is consumed and cannot be reused. The role
matrix at `config-files/auth/matrix.yaml` already wires the
admin user to the admin role.

## 6. Configure the CLI (1 min)

`wolfci-ctl` authenticates via wolfSSL mTLS - it presents a client
cert whose CN the server maps back to `config-files/auth/matrix.yaml`.
Mint a client cert (or have the admin mint one for you), then:

```sh
wolfci-ctl login \
    --server https://localhost:8443 \
    --cert ~/.config/wolfci/admin.crt \
    --key ~/.config/wolfci/admin.key \
    --ca-cert /var/lib/wolfci/config-files/server-ca.crt
```

That writes `~/.config/wolfci/ctl.yaml` (override with
`WOLFCI_CTL_CONFIG` or `XDG_CONFIG_HOME`). Every subsequent
subcommand reads it.

## 7. Create a job (1 min)

Jobs live as YAML files under `/var/lib/wolfci/jobs/<name>/job.yaml`.
Drop a simple one in:

```yaml
# /var/lib/wolfci/jobs/hello/job.yaml
name: hello
description: First green build
steps:
  - shell: echo "hello, wolfCI"
  - shell: uname -a
```

The web UI at `/jobs/new` is the easier path once the admin
account exists. The form posts to the same storage layer.

## 8. Run the build and tail the log (1 min)

```sh
wolfci-ctl job list
wolfci-ctl job run hello
# -> hello queued as build 1
#    tail with: wolfci-ctl build log hello 1
wolfci-ctl build log hello 1
```

A green build leaves `result.json` next to `log.live` under
`/var/lib/wolfci/builds/hello/1/`, with `status: success`.

That is the 10-minute path.

## Next steps

- Add nodes: install `wolfci-agent` on each worker (same binary
  set, see `scripts/install/`), point its `agent.yaml` at the
  server, and the Phase 5 router will start dispatching matching
  jobs to it. On-prem agents are preferred; the GCE provisioner
  only spawns cloud nodes when no on-prem agent matches.
- Plugins: drop a binary under
  `/var/lib/wolfci/plugins/installed/<name>/<name>` and the
  plugin host will load it on next server start. See
  `docs/PLUGINS.md`. The shipped `email-on-failure` plugin is a
  worked example.
- Security model: `docs/SECURITY.md` covers the TLS profile,
  the SSH-key + bcrypt auth paths, the role matrix, and the
  threat model.

## Cross-builds

To produce a Linux release binary from a macOS dev box, install a
static-capable cross compiler (`brew install
FiloSottile/musl-cross/musl-cross`), then:

```sh
TARGET_GOOS=linux TARGET_GOARCH=amd64 scripts/build-wolfssl.sh
GOOS=linux GOARCH=amd64 CC=x86_64-linux-musl-gcc scripts/build.sh
```

To go the other way (darwin/arm64 from a darwin/amd64 host), the
host Xcode toolchain already handles the arch swap:

```sh
TARGET_GOOS=darwin TARGET_GOARCH=arm64 scripts/build-wolfssl.sh
GOOS=darwin GOARCH=arm64 scripts/build.sh
```

End of GETTING-STARTED.md.
