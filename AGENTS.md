# helianthus-canbusreg Contributor Guide

## Purpose and Ownership

This repository owns CAN product and profile qualification, bounded read-only
raw records, and profile-level semantic results. It does not own SocketCAN or
other transport I/O, generic CAN framing, universal cross-protocol semantics,
consumer APIs, or vendor control.

Qualification is default-denied. Keep native identifiers and raw records
available, distinguish candidate from qualified results, and preserve
last-known-good fields when a partial read fails. Discovery and qualification
must use explicit bounded allowlists and retries. Do not add writes, active
probing, interface configuration, or live-device actions.

## Documentation

The canonical public CAN contract destination is
https://github.com/Project-Helianthus/helianthus-docs-canbus. Product-specific
claims need public URLs and explicit qualification status.

## Workflow

- Use one scoped issue, one `issue/<id>-<slug>` branch from current `main`, and
  one linked PR. Do not alter another contributor's branch.
- Use RED-first tests when behavior, qualification, persistence, recovery,
  concurrency, or safety changes; documentation-only work is exempt.
- Before push, run `GOWORK=off go test ./...` and `GOWORK=off go vet ./...`.
- State applicable documentation, transport, conformance, and smoke gates in
  the PR. T01..T88 belongs to the transport owner, not this registry.
- Resolve P0-P2 findings and obtain a fresh exact-HEAD no-blocker review.
- Squash merge only after all applicable checks and gates are green. Do not
  merge during implementation work unless the operator explicitly requests it.

Do not add plan hashes, authority tokens, attestations, executable workflow
state, credentials, local paths, or private-lab dependencies.
