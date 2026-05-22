# wolfCI Security Model

This document captures the wolfCI security model as it exists today.
Update it in the same commit that changes the model.

## TLS

- All HTTP traffic to the wolfCI server is wrapped by wolfSSL via
  `internal/tlsutil` (CGO). No cleartext listener exists.
- Default and only currently-implemented protocol: TLS 1.3.
  `tlsutil.Config.MinVersion` uses local wire constants
  (`tlsutil.VersionTLS13` etc., defined in
  `internal/tlsutil/version.go`); the `crypto/tls` package is not
  imported by wolfCI source code (Phase 10.10). TLS 1.2 support
  is on the backlog and would land in `tlsutil` rather than reach
  back into the stdlib.
- Cipher suites are the wolfSSL defaults under the build profile in
  `scripts/build-wolfssl.sh`: AES-GCM and ChaCha20-Poly1305 AEADs;
  ECDH/ECDSA, Curve25519, Ed25519 for asymmetric. SSLv3, TLS 1.0,
  and TLS 1.1 are disabled at the library level
  (`--disable-sslv3`, `--disable-oldtls`).
- The server certificate is loaded from PEM at server start. The
  bootstrap path (Phase 3.x continuing) creates one if none exists.

## Cryptography source

wolfCI uses wolfCrypt (wolfSSL's cryptography component) and only
wolfCrypt for every cryptographic operation in its own source tree.
This rule comes from CLAUDE.md mission goal #5 and was made literal
on 2026-05-21: "ZERO cryptography ... zero, none, nadda
cryptography unless it's from wolfCrypt in this solution." Phase 10
rerouted every primitive in the auth stack through wolfCrypt.

### The wolfCrypt-only rule

- No `crypto/*`, no `golang.org/x/crypto/*`, no third-party Go
  cryptography library is imported by wolfCI's own packages.
- The audit boundary collapses to one C library
  (`third_party/wolfssl`) plus its Go binding
  (`third_party/go-wolfssl`).
- `internal/wolfcrypt` is the only package allowed to talk to
  wolfCrypt directly. Every other wolfCI package consumes
  `internal/wolfcrypt`'s typed Go API (`RandBytes`, `HMACSHA256`,
  `PBKDF2HMACSHA256`, `SHA256`, `Ed25519GenKey`, `Ed25519Sign`,
  `Ed25519Verify`, `ECCVerifyP256`, `RSAVerifyPKCS1v15SHA256`,
  `MintCert`). Adding a new primitive means adding it here first.
- OpenSSL-compatibility APIs are forbidden by CLAUDE.md Hard Rule
  #12: no `--enable-opensslextra`, no `EVP_*` / `X509_*` /
  `SSL_CTX_*` / `BIO_*` calls. Only the native `wc_*` / `wolfSSL_*`
  surface is used.
- The gate is grep-able:
  `grep -rE '"crypto/|"golang.org/x/crypto/' internal/ cmd/` must
  return either nothing or only `cryptobyte` (see the next
  section). Tests in `internal/tlsutil/tlsutil_test.go` and
  `internal/agent/client_test.go` intentionally still import
  `crypto/tls` as a third-party TLS client to verify wolfSSL
  interop; that is a deliberate interop check, not wolfCI calling
  stdlib crypto in production.

### The ask-first rule (CLAUDE.md Hard Rule #11)

Before adding any crypto-adjacent dependency or hand-rolling
crypto-adjacent code (SSH wire parsers, TLS bindings, OAuth flows,
JWT, X.509 helpers, language wrappers for crypto libs, network
protocols that ride on top of crypto), the contributor asks the
project owner first. wolfSSL almost certainly has it. Known
in-tree projects:

| Need                              | wolfSSL project          | Vendored at                            |
| --------------------------------- | ------------------------ | -------------------------------------- |
| Go bindings for wolfCrypt + TLS   | `wolfSSL/go-wolfssl`     | `third_party/go-wolfssl/`              |
| SSH server + client (C)           | `wolfSSL/wolfssh`        | `third_party/wolfssh/`                 |
| SSH wire helpers from Go          | `go-wolfssl/wolfssh/`    | `third_party/go-wolfssl/wolfssh/`      |

When the vendored wolfSSL project is missing a wrapper wolfCI
needs, the wrapper is added to the vendored copy as a numbered
patch under `third_party/<name>-patches/`; `scripts/test-<name>.sh`
re-applies every patch on a clean submodule worktree so fresh
clones just work. See `third_party/go-wolfssl-patches/README.md`
for the current patch set and each patch's upstream-fix drop
condition.

### Transitive dependency: `golang.org/x/crypto/cryptobyte`

`internal/nodes/gce` imports the Google Cloud SDK
(`google.golang.org/api/compute/v1`), which transitively imports
`golang.org/x/crypto/cryptobyte` via `google/s2a-go`. cryptobyte
is a byte builder/parser utility (length-prefixed wire decode,
ASN.1 walker) used by s2a-go for handshake serialization; it
contains no cryptographic primitives. The dependency is a known
boundary, not a violation of the wolfCrypt-only rule. Dropping
it would require rewriting `internal/nodes/gce` against a non-
Google Cloud client; that is on the backlog for after Phase 11.

### wolfSSL build profile -> wolfCrypt primitive map

The wolfSSL static archive at `build/wolfssl-install/lib/libwolfssl.a`
is built by `scripts/build-wolfssl.sh` with the configure flags
listed below. `scripts/test-build-wolfssl.sh` re-asserts each
flag's presence in the build script so a profile regression cannot
silently break the auth stack.

TLS / handshake surface (consumed by `internal/tlsutil`):

| Feature                       | Configure flag             |
| ----------------------------- | -------------------------- |
| TLS 1.3 server + client       | `--enable-tls13`           |
| TLS 1.0/1.1 OFF               | `--disable-oldtls`         |
| SSLv3 OFF                     | `--disable-sslv3`          |
| AES-GCM cipher suite          | `--enable-aesgcm`          |
| ChaCha20 cipher               | `--enable-chacha`          |
| Poly1305 MAC (paired w/ChaCha)| `--enable-poly1305`        |
| TLS 1.3 curve negotiation     | `--enable-supportedcurves` |
| Curve25519 key agreement      | `--enable-curve25519`      |
| Ed25519 signatures            | `--enable-ed25519`         |
| ECC (P-256 etc.)              | `--enable-ecc`             |
| SNI (server-side hostname)    | `--enable-sni`             |
| ALPN (protocol negotiation)   | `--enable-alpn`            |
| Session resumption tickets    | `--enable-session-ticket`  |

wolfCrypt primitives consumed by the auth stack:

| `internal/wolfcrypt` API     | Used for                                    | Configure flag(s)                                   |
| ---------------------------- | ------------------------------------------- | --------------------------------------------------- |
| `RandBytes`                  | salts, session tokens                       | default (RNG always built)                          |
| `HMACSHA256`                 | constant-time compare, KDF inner            | default (HMAC + SHA-256 always built)               |
| `PBKDF2HMACSHA256`           | password hashing (replaces bcrypt)          | `--enable-pwdbased`                                 |
| `SHA256`                     | hashing tbsCertificate, generic digests     | default (SHA-256 always built)                      |
| `Ed25519GenKey`, `Sign`,     | first-admin bootstrap key, SSH auth         | `--enable-ed25519`                                  |
| `Ed25519Verify`              |                                             |                                                     |
| `ECCVerifyP256`              | SSH ecdsa-sha2-nistp256 auth                | `--enable-ecc`                                      |
| `RSAVerifyPKCS1v15SHA256`    | SSH ssh-rsa / rsa-sha2-256 auth             | default (RSA always built)                          |
| `MintCert`                   | test certs, server cert bootstrap           | `--enable-keygen`, `--enable-certgen`,              |
|                              |                                             | `--enable-certext`, `--enable-certreq`,             |
|                              |                                             | `-DWOLFSSL_ALT_NAMES` (CPPFLAGS, no `--enable-*`)   |

`-DWOLFSSL_ALT_NAMES` is set via `CPPFLAGS` rather than a
`--enable-*` flag because wolfSSL only auto-enables alt-names for
`--enable-jni` and `--enable-lighty`, neither of which we want.
`scripts/test-build-wolfssl.sh` gates this CPPFLAGS define
directly.

Hardening flags (also gated by `scripts/test-build-wolfssl.sh`):

| Flag                | Effect                                                          |
| ------------------- | --------------------------------------------------------------- |
| `--enable-static`   | Static library only; wolfCI ships as one binary.                |
| `--disable-shared`  | No shared object; nothing on disk to swap at runtime.           |
| `--disable-sslv3`   | Refuse SSLv3 at the library level.                              |
| `--disable-oldtls`  | Refuse TLS 1.0 / 1.1 at the library level.                      |

## Authentication

Two authentication mechanisms are supported. SSH public-key auth
is the preferred mechanism; password auth is optional and disabled
by default.

### SSH public-key auth

- One OpenSSH-format public key per user, stored at
  `config-files/auth/keys/<username>.pub` (mode 0644).
- The username is the basename of the file without `.pub`.
- The server presents a challenge; the client signs it with the
  matching private key; `internal/auth.KeyStore.VerifySignature`
  validates the signature against the stored public key.
- Username validation rejects empty strings, `.`, `..`, names
  starting with a dot, and any name containing a path separator
  or nul byte. Path traversal attempts are caught before the
  filesystem is touched.

### Username + password auth (optional)

- Disabled by default. Enable via `config-files/auth/config.yaml`:

  ```yaml
  password_enabled: true
  pbkdf2_iterations: 600000
  pbkdf2_salt_bytes: 16
  ```

- `pbkdf2_iterations` defaults to 600000 (OWASP 2023 guidance for
  PBKDF2-HMAC-SHA-256). Allowed range is 100 through 10000000.
  Raise this as CPUs get faster; lower it only for tests.
- `pbkdf2_salt_bytes` defaults to 16. Allowed range is 8 through
  64.
- Password hashes are stored one per user at
  `<root>/<username>.pbkdf2` with mode 0600. The file format is
  versioned with a `wolfci-pbkdf2-v1` sentinel; the verifier
  refuses anything else, which catches a leftover bcrypt blob
  from a pre-Phase-10 install.
- Hashing routes through `internal/wolfcrypt.PBKDF2HMACSHA256`,
  which calls wolfCrypt's `wc_PBKDF2` with `WC_SHA256`. The salt
  is drawn from `internal/wolfcrypt.RandBytes` (wolfCrypt RNG).
- Verification uses the HMAC-both-sides constant-time pattern: a
  fresh 32-byte HMAC key is generated from `RandBytes`, both the
  stored and candidate keys are HMAC'd under it, and the HMAC
  outputs are byte-compared. A timing leak in the byte loop only
  reveals HMAC outputs over an unpredictable key, not the
  password hash.
- `internal/auth.PasswordStore.VerifyPassword` short-circuits with
  `ErrPasswordAuthDisabled` when the toggle is off; the hash file
  is not even opened.

### First-admin bootstrap

On first server start, if no users exist on disk, the server:

1. Generates an SSH keypair (private key written to
   `config-files/auth/bootstrap/wolfci_admin`, mode 0600).
2. Prints a one-time-token URL to stdout (also written to
   `config-files/auth/bootstrap/token` mode 0600).
3. Waits for the admin to consume the token via the setup
   endpoint, which:
   - registers the bootstrap public key as
     `config-files/auth/keys/<admin-username>.pub`,
   - assigns the user the `admin` role in
     `config-files/auth/matrix.yaml`,
   - invalidates the token.

After the bootstrap completes, the bootstrap directory should be
removed by the operator. (This step is enforced by Phase 3.x
follow-up rather than the current build.)

### User lifecycle

wolfCI has NO self-signup endpoint. There is no "create account"
form, no email invite flow. Every user after the bootstrap admin
exists because an admin explicitly added them.

Adding a user (admin action):

1. Obtain the user's OpenSSH public key out of band.
2. Write it to config-files/auth/keys/<username>.pub (mode 0644).
3. Add an entry to config-files/auth/matrix.yaml under "users:"
   mapping that username to a role (admin, developer, or viewer).

Removing a user (admin action):

1. Delete config-files/auth/keys/<username>.pub.
2. Delete the user's entry from config-files/auth/matrix.yaml.
3. Optional: delete config-files/auth/passwords/<username>.pbkdf2
   if password auth was enabled and the user had a password.

Changing a user's role:

1. Edit their entry in config-files/auth/matrix.yaml. No restart
   required; the matrix is read on every authorization check.

Promoting/demoting admins:

1. Same as "changing a role" - move the user between admin /
   developer / viewer in matrix.yaml. Only admins can edit
   matrix.yaml in practice (file permissions + Phase 6 UI gate).

The Phase 6 web UI exposes these operations as admin-only
endpoints so the operator does not have to shell into the host
for every onboarding/offboarding. The file-based model is the
ground truth; the UI is a convenience layer that writes the same
files.

## Authorization

`internal/authz` implements a Jenkins-style role-based matrix.

### Roles

| Role      | Permissions                                                                              |
| --------- | ---------------------------------------------------------------------------------------- |
| admin     | `*` (every permission)                                                                   |
| developer | `jobs.read`, `jobs.build`, `builds.read`, `builds.cancel`, `nodes.read`                  |
| viewer    | `jobs.read`, `builds.read`, `nodes.read`                                                 |

There is no anonymous role. Every action requires an authenticated
user.

### Permissions

Dotted, namespaced strings. The current set:

- `jobs.read`, `jobs.build`, `jobs.configure`
- `builds.read`, `builds.cancel`
- `nodes.read`, `nodes.configure`
- `*` (wildcard; admin only)

Add new permissions as new resources land; existing roles need an
explicit update to grant them.

### Matrix file

`config-files/auth/matrix.yaml`:

```yaml
users:
  alice: admin
  bob: developer
  carol: viewer
roles:
  admin:
    - "*"
  developer:
    - jobs.read
    - jobs.build
    - builds.read
    - builds.cancel
    - nodes.read
  viewer:
    - jobs.read
    - builds.read
    - nodes.read
```

Users absent from the `users` map are denied. A user mapped to a
role absent from the `roles` map is also denied.

## Files and modes

Summary of on-disk paths touched by the auth layer:

| Path                                          | Mode | Purpose                              |
| --------------------------------------------- | ---- | ------------------------------------ |
| `config-files/auth/config.yaml`               | 0644 | password_enabled, pbkdf2 parameters  |
| `config-files/auth/keys/<user>.pub`           | 0644 | OpenSSH public keys, one per user    |
| `config-files/auth/passwords/<user>.pbkdf2`   | 0600 | PBKDF2-HMAC-SHA-256 password hashes  |
| `config-files/auth/matrix.yaml`               | 0644 | Role assignments and permissions     |
| `config-files/auth/bootstrap/wolfci_admin`    | 0600 | First-admin private key (delete after bootstrap) |
| `config-files/auth/bootstrap/token`           | 0600 | First-admin setup token (deleted on use) |

The parent `config-files/auth/` and its `passwords/` subdirectory
are created with mode 0700; the rest with mode 0755.

## Node heartbeats (Phase 12)

Every agent periodically sends an `AgentMessage.Heartbeat`
carrying a NodeStatus on its existing Connect stream. The
NodeStatus rides the same wolfSSL mTLS-terminated gRPC channel
the AgentService already uses for JobAssignment / LogChunk /
BuildComplete; there is no new port, no new auth surface, no
new certificate. An agent that fails the mTLS handshake never
gets to emit a heartbeat, and an unregistered agent_id is
rejected by the server's Connect handler before any message
loops.

The fields NodeStatus carries (architecture, free disk / swap /
temp, host uptime, wall clock, Go version, agent version) are
self-reported by the agent and treated as advisory. They feed
the operator's view of the fleet at /nodes; nothing in the
scheduler dispatches based on them today, so a misbehaving
agent that lies about its free disk only confuses the UI.

### nodes.configure permission

The /nodes/&lt;id&gt; detail page exposes a Take-offline /
Bring-online toggle. The endpoint flips an in-memory disabled
flag the scheduler checks before dispatching, so an operator
can drain a node without taking the wolfCI server down.

The intended gate is the `nodes.configure` permission from the
authz matrix; the permission already exists in
`internal/authz/matrix.go` and the admin role grants it. The
current handler enforces session-only authentication and
documents the matrix-driven gate as the follow-up that turns
this into a privileged action. Until then, any user with a
valid wolfCI session can flip the toggle; restrict the session
issuance surface (password auth disable, SSH key gating) to
match your trust level.

## Threat model

In scope:

- Network attackers attempting to MITM HTTPS or downgrade TLS.
  Mitigated by wolfSSL with TLS 1.3 only and modern cipher suites.
- Unauthenticated users attempting to read or modify wolfCI state.
  Mitigated by the no-anonymous-role rule.
- Authenticated users attempting to perform actions outside their
  role's permissions. Mitigated by the authz matrix.
- Path-traversal attempts via crafted usernames. Mitigated by
  `validateUsername`.

Out of scope (today):

- Compromise of the host running wolfCI (root or equivalent). An
  attacker with disk access can read every file in
  `config-files/auth/` and assume any identity.
- Cryptographic attacks on wolfSSL itself. We rely on upstream
  wolfSSL hardening and our build profile to disable insecure
  primitives.
- Side-channel attacks against PBKDF2 or signature verification.
  The HMAC-both-sides verify pattern is defense in depth; we do
  not claim it makes the byte compare itself constant-time.

End of docs/SECURITY.md.
