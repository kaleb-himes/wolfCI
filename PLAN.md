# PLAN.md - wolfCI Working Plan

This is the durable, machine-readable plan for building wolfCI. It is
the source of truth for what to do next. PLAN.md contains ONLY
unfinished work; completed work lives in PLAN.historic.

Every /loop iteration:

1. Finds the first unchecked task under "## Current Phase".
2. Completes it (TDD: failing test first, then implementation).
3. MOVES the completed task (entire bullet, including its
   sub-bullets) from PLAN.md to PLAN.historic, appending to the
   matching Phase section there (creating it if absent).
4. When the active phase has no unchecked top-level tasks left,
   moves the phase header and any remaining phase-level context to
   PLAN.historic and advances "## Current Phase" to the next phase
   that still has open work.
5. Commits, merges to main, rebuilds, pushes.

Format conventions:

- `[ ]` open, `[~]` blocked or punted (with a note on the same
  line explaining why and what unblocks it). `[x]` is reserved
  for sub-bullets that exist as historical context for an OPEN
  parent item (e.g. completed sub-checkpoints of an open task).
  Free-standing `[x]` items do not appear in PLAN.md; they move
  to PLAN.historic the moment they are finished.
- Each task names a test file or describes the failing test that
  gates the implementation.
- ASCII only. No emdash. No fancy quotes.
- Sub-bullets are allowed for detail but the top-level numbered line
  is the unit of progress.

## Current Phase

Awaiting operator feedback - Phase 18 + Phase 19 + the
Polish queue (P1 + P2) have all shipped. Visual sign-off
of the polish work needs a real-browser screenshot per
CLAUDE.md rule 14; the operator will collect any feedback
during testing and we will fold it back into PLAN.md as
new items.

## Backlog (not in main flow)

Items that came up but are not on the critical path. Promote into a
phase when they become relevant.

- Persistent metrics and a built-in Prometheus exporter.
- Multi-master HA (one process per install for v1).
- Windows agent support (Linux + macOS first).
- LDAP, SAML, OIDC SSO (after core auth lands).
- Quiet the macOS linker warnings about wolfSSL objects targeting a
  newer macOS than the Go stdlib link target. Likely fix: set
  -mmacosx-version-min in scripts/build-wolfssl.sh CFLAGS so both
  sides agree on a deployment target.
- Per-conn read/write serialization in internal/tlsutil. wolfSSL
  is not safe for concurrent wolfSSL_read + wolfSSL_write on the
  same WOLFSSL*; today the package assumes the caller serializes
  (which is fine for one reader / one writer net.Conn use, but
  should be made explicit).
- GCE startup-script fill-in: install wolfci-agent on the booted
  VM, distribute cert material (GCE Secret Manager preferred),
  drop config-files/agent.yaml with the spawn-time agent_id and
  label, start the agent under systemd. Until this lands the
  GCE Provisioner CREATES VMs but the VMs do not actually join
  the wolfCI cluster.
- FileLogSink in internal/agentsvc that persists LogChunks under
  builds/<job>/<n>/log.live so the Phase 6 UI can tail an actual
  file on disk. Today the server-side LogSink is just an
  interface and a capturing in-memory implementation lives in
  the test.
- GHPRB webhook receiver (HMAC-validated) as an alternative to
  per-job polling. Phase 18 ships polling only; webhooks come
  later when wolfCI has a stable public ingress story.
- Plugin steps not covered in Phase 18: docker.image() (with
  .pull / .inside), copyArtifacts (Copy Artifact plugin), junit,
  emailext, lock, timestamps. Promote into a phase when a
  Jenkinsfile we need to run actually uses them.
- Pipeline post {} blocks (always / success / failure / unstable
  / cleanup). The master-job does not use post {}; downstream
  files do.
- Pipeline parameters {} block (string / choice / boolean /
  password). Phase 18 passes parameters via the build step's
  `parameters:` argument only. Promote when needed.
- Boolean label expressions in agent { label '...' }. Phase 5
  decided these are backlog; the master-job uses
  "master_linux_group || linux-cloud-node" which Phase 18 must
  handle as the simplest form. Full Jenkins-style expressions
  (linux && tpm && !(arm)) come later.

End of PLAN.md.
