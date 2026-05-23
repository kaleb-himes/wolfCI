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

Polish queue - job-edit form / raw view UI polish before Phase 19.

## Polish queue (do after phase 18 closes, before phase 19 opens)

The job-view page at /jobs/<name> is the reference for what
"professional, clean, properly aligned" looks like in this
codebase. The configure / edit views fall short of that bar
and need a polish pass that brings them in line with
CLAUDE.md rule 14.

- [ ] P1 Polish the job-edit view (YAML / raw mode).
        URL: /jobs/<name>/edit?view=raw . Today the textarea,
        the save/cancel controls, the breadcrumb, and the
        view-toggle are arranged without consistent padding,
        column alignment, or section grouping; the result
        reads as haphazard against the clean read-only
        /jobs/<name> page. Match the read-only page's
        spacing rhythm, group the editor and its controls
        into a single bordered card with a sticky action bar
        at the bottom, give the textarea a fixed
        monospace-width that aligns to the page's content
        column, and surface validation errors (parse failure,
        unknown field) inline above the editor in a
        clearly-separated alert section rather than as raw
        text bumped against other elements. Tested by
        loading the page in a real browser AND taking a
        before/after screenshot for the commit message.
- [ ] P2 Polish the job-edit view (form mode).
        URL: /jobs/<name>/edit?view=form . Same alignment /
        grouping / control-choice rules from CLAUDE.md
        rule 14: every named field is a labelled row,
        checkboxes/radios for on-off, drop-downs for
        bounded enumerations (retention strategy,
        agent-label kind, etc.), text fields equal width
        within their column, sections separated by
        fieldsets or cards. The current form crams
        unrelated controls together and reads cluttered;
        the target is the same visual rhythm as the
        read-only job view. Same testing protocol as P1.

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
