# helianthus-canbusreg Contributor Guide

## Purpose and Ownership

- This repository is the CAN driver. It owns CAN product and profile knowledge,
  its mapping to SemReg in both directions through the SemReg driver contract,
  whose public packages it may import: `semreg/v1`, `semreg/v1/driver`,
  `semreg/v1/roweval`, `semreg/v1/catalog` and `semreg/v1/operation`, and the
  encoders of the frames that the documentation defines as commands (Gree VRF
  included). It decides what is sent and how often, and hands frames to the
  publication API of `helianthus-canbus` (OPERATOR item 11; decisions D1, D2).
- It does not own SocketCAN or other interface setup, transport I/O or generic
  CAN framing (OPERATOR item 11, Commandment 4), host code or consumer APIs
  (rule 3). `scripts/tests/driver_imports_test.py` checks the imports.
- Support is declared from the specification and tested representatively. The
  level of verification is recorded; it never blocks definition, implementation
  or publication. The operator's own hardware session on the operator's
  documented hardware, while it has not been run, blocks only the release review
  (rule 12, OPERATOR item 31, D14). Native identifiers and raw records stay
  available; last-known-good fields are kept when a partial read fails.
- A transmission on a real installation during development or test needs the
  operator's word at that moment (rule 16).

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
