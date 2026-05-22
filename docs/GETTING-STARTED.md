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
- An existing SSH keypair you control (BYOK; the server never
  generates user keys). If you do not have one already:
  `ssh-keygen -t ed25519 -C "you@example.com"`.
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

## 4. Author server.yaml (1 min)

`cmd/wolfci` reads its config from a single YAML file. Production
service units pass it via `--config /etc/wolfci/server.yaml`
(systemd) or `--config /usr/local/etc/wolfci/server.yaml`
(launchd). The minimal Linux layout:

```yaml
# /etc/wolfci/server.yaml
listen_addr: 0.0.0.0:8443
cert:        /etc/wolfci/tls/server.crt
key:         /etc/wolfci/tls/server.key
ca_cert:     /etc/wolfci/tls/ca.crt
work_dir:    /var/lib/wolfci
auth_dir:    /var/lib/wolfci/config-files/auth

# Optional. Defaults shown.
shutdown_drain_timeout: 30s
plugin_dir: /var/lib/wolfci/plugins
```

`cert`, `key`, and `ca_cert` are PEM-encoded paths. Mint the server
cert from any CA your operators trust; for an isolated test
deployment the same self-signed cert can serve as its own
`ca_cert`. `work_dir` and `auth_dir` are created on first start if
missing, but their parent directory must already exist with the
right ownership.

For local development you can skip the YAML and use the positional
quick mode, which defaults `work_dir` to `./work` and `auth_dir`
to `./auth`:

```sh
build/bin/$(go env GOOS)-$(go env GOARCH)/wolfci \
    127.0.0.1:8443 tests/certs/server-cert.pem tests/certs/server-key.pem
```

Quick mode is intended for `tests/basic-test-*.sh` and laptop
poking; use `--config` for anything you expect to keep running.

## 5. Install the service (1 min)

Pick the template that matches your OS. Both expect the server
binary at `/usr/local/bin/wolfci` and the config from step 4 at
the path the unit references.

### Linux (systemd)

```sh
sudo cp build/bin/linux-amd64/wolfci /usr/local/bin/
sudo useradd --system --home /var/lib/wolfci --shell /usr/sbin/nologin wolfci
sudo install -d -o wolfci -g wolfci /var/lib/wolfci
sudo install -d -m 0750 -o wolfci -g wolfci /etc/wolfci /etc/wolfci/tls
sudo install -m 0640 -o wolfci -g wolfci server.yaml /etc/wolfci/
sudo cp scripts/install/systemd/wolfci.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now wolfci
sudo journalctl -u wolfci -f
```

The unit's `ExecStart` already passes
`--config /etc/wolfci/server.yaml`; edit the path with a drop-in
override if you put the file elsewhere.

### macOS (launchd, development)

```sh
sudo cp build/bin/darwin-arm64/wolfci /usr/local/bin/
sudo install -d /usr/local/var/wolfci /usr/local/var/log /usr/local/etc/wolfci
sudo cp server.yaml /usr/local/etc/wolfci/
sudo cp scripts/install/launchd/com.wolfssl.wolfci.plist \
    /Library/LaunchDaemons/
sudo launchctl bootstrap system \
    /Library/LaunchDaemons/com.wolfssl.wolfci.plist
sudo tail -f /usr/local/var/log/wolfci.log
```

Both run as a dedicated unprivileged user and restart on failure.
The systemd unit grants `CAP_NET_BIND_SERVICE` so the server can
bind 443 without root, and sets `TimeoutStopSec=30s` so the
graceful shutdown drain matches the server's default
`shutdown_drain_timeout`.

## 6. Bootstrap the first admin (2 min)

wolfCI follows a BYOK (bring-your-own-key) model: the server never
generates user keypairs. On first start with no admin pubkey on
disk under `auth_dir/keys/`, the server mints a one-time token,
writes it to `auth_dir/bootstrap/token`, and prints the consumption
URL to stdout. Watch the journal (systemd) or log file (launchd);
you will see something like:

```
wolfci: first-admin setup URL:
  https://localhost:8443/setup?token=89cc95c0e3263485ae5c7bdf39b72e5b3cdc70a61cf6885dd0f68b12e5416e0a
```

Open the URL in a browser, paste the contents of your existing
SSH public key (`~/.ssh/id_ed25519.pub` or equivalent) into the
form, pick a username, and submit. The server:

1. Writes the verbatim pubkey to
   `auth_dir/keys/<username>.pub`.
2. Adds `<username>: admin` under `users:` in
   `config-files/auth/matrix.yaml`.
3. Renames `auth_dir/bootstrap/` to `auth_dir/bootstrap.consumed/`
   so the operator can audit the one-time token without leaving
   a usable copy on disk.

The token expires on first successful consumption, not by
wall-clock; a fresh install can sit idle between provisioning and
the operator's first login without the URL going stale.

## 7. Configure the CLI (1 min)

`wolfci-ctl` authenticates over the same wolfSSL TLS listener.
Mint a client cert (or have the admin mint one for you), then:

```sh
wolfci-ctl login \
    --server https://localhost:8443 \
    --cert ~/.config/wolfci/admin.crt \
    --key ~/.config/wolfci/admin.key \
    --ca-cert /etc/wolfci/tls/ca.crt
```

That writes `~/.config/wolfci/ctl.yaml` (override with
`WOLFCI_CTL_CONFIG` or `XDG_CONFIG_HOME`). Every subsequent
subcommand reads it.

## 8. Create a job (1 min)

Jobs live as YAML files under `<work_dir>/jobs/<name>/job.yaml`.
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

## 9. Run the build and tail the log (1 min)

```sh
wolfci-ctl job list
wolfci-ctl job run hello
# -> hello queued as build 1
#    tail with: wolfci-ctl build log hello 1
wolfci-ctl build log hello 1
```

A green build leaves `result.json` next to `log` under
`<work_dir>/builds/hello/1/`, with `status: success`.

That is the 10-minute path.

## Day-2 user management

Adding more users (operators, read-only auditors), revoking access,
and editing the role matrix is covered in `docs/SECURITY.md`. The
short version: each user's SSH pubkey goes into
`auth_dir/keys/<username>.pub` and their role into
`config-files/auth/matrix.yaml`. There is no `wolfci-ctl user add`
that calls a keygen; an existing admin pastes the new user's pubkey
(supplied out-of-band) into the matrix entry. BYOK applies to every
account, not just the first admin.

## Next steps

- Add nodes: install `wolfci-agent` on each worker (same binary
  set, see `scripts/install/`), point its `agent.yaml` at the
  server, and the Phase 5 router will start dispatching matching
  jobs to it. On-prem agents are preferred; the GCE provisioner
  only spawns cloud nodes when no on-prem agent matches.
- Plugins: drop a binary under
  `<work_dir>/plugins/installed/<name>/<name>` (or under
  `plugin_dir` if you overrode it in server.yaml) and the plugin
  host will load it on next server start. See `docs/PLUGINS.md`.
  The shipped `email-on-failure` plugin is a worked example.
- Security model: `docs/SECURITY.md` covers the TLS profile, the
  SSH-key + bcrypt auth paths, the role matrix, day-2 user
  management, and the threat model.

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
