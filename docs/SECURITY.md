# wolfCI Security Model

This document captures the wolfCI security model as it exists today.
Update it in the same commit that changes the model.

## TLS

- All HTTP traffic to the wolfCI server is wrapped by wolfSSL via
  `internal/tlsutil` (CGO). No cleartext listener exists.
- Default and only currently-implemented protocol: TLS 1.3.
  `tlsutil.Config.MinVersion` will gain TLS 1.2 support later.
- Cipher suites are the wolfSSL defaults under the build profile in
  `scripts/build-wolfssl.sh`: TLS 1.2 and 1.3 with AES-GCM and
  ChaCha20-Poly1305 AEADs; ECDH/ECDSA, Curve25519, Ed25519 for
  asymmetric. SSLv3, TLS 1.0, and TLS 1.1 are disabled at the
  library level.
- The server certificate is loaded from PEM at server start. The
  bootstrap path (Phase 3.x continuing) creates one if none exists.

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
  bcrypt_cost: 12
  ```

- `bcrypt_cost` defaults to 12. Allowed range is the bcrypt
  library's `MinCost` (4) through `MaxCost` (31). Raise this as
  CPUs get faster; lower it only for tests.
- Password hashes are stored one per user at
  `<root>/<username>.bcrypt` with mode 0600.
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
3. Optional: delete config-files/auth/passwords/<username>.bcrypt
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
| `config-files/auth/config.yaml`               | 0644 | password_enabled, bcrypt_cost        |
| `config-files/auth/keys/<user>.pub`           | 0644 | OpenSSH public keys, one per user    |
| `config-files/auth/passwords/<user>.bcrypt`   | 0600 | bcrypt password hashes, one per user |
| `config-files/auth/matrix.yaml`               | 0644 | Role assignments and permissions     |
| `config-files/auth/bootstrap/wolfci_admin`    | 0600 | First-admin private key (delete after bootstrap) |
| `config-files/auth/bootstrap/token`           | 0600 | First-admin setup token (deleted on use) |

The parent `config-files/auth/` and its `passwords/` subdirectory
are created with mode 0700; the rest with mode 0755.

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
- Side-channel attacks against bcrypt or signature verification.

End of docs/SECURITY.md.
