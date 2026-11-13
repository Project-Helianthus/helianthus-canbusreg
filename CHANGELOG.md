# Changelog

## Unreleased

- The release-11 slot-current decoder of `DecodePCSSlots` applies the
  non-parallel offset for Bank `ordinary`, the value the gateway's PCS
  configuration asserts for non-parallel profiles. Before this change it
  required `standard`, so the ordinary-bank slot current stayed raw.
  `NewSelection` accepts only the banks `ordinary` and `parallel` and rejects
  every other value with `explicit bank ordinary or parallel required`; no
  alias for `standard` is provided. The gateway already asserts these two
  values and adopts the fix by re-pinning this revision. This artifact-changing
  change requires renewed release evidence before the final bill of materials
  freeze; no physical qualification is claimed.

- Rename the remaining work-item-prefixed exported identifiers to behavior names:
  `CXSelection` to `Selection`, `NewCXSelection` to `NewSelection`,
  `CXNativeField` to `NativeField`, `CXNativeResult` to `NativeResult`,
  `CXDecodeLINKTelemetry` to `DecodeLINKTelemetry`, `NewCX15OpaqueStore` to
  `NewOpaqueStore` and `NewCX16SelectedProfile` to `NewSelectedProfile`. The
  embedded `NativeResult` field of the decoded result types follows the type
  name. `python3 scripts/rename_provider_names.py` reproduces the rename. No
  alias, forwarding declaration or migration is provided; consumers adopt the new
  names when they pin this revision. Wire values, error texts, native evidence,
  candidate grades, admission and partial last-known-good behavior are
  unchanged. This artifact-changing class M change requires renewed release
  evidence before the final bill of materials freeze.

- Document root package ownership and exported API contracts, including candidate
  grades, fail-closed admission, lifecycle and retained evidence. Declaration
  comments are artifact-changing and require renewed release evidence before
  the final bill of materials freeze and coordinated gateway adoption.

- Rename `O08 requires explicit V1.04 selection` to `Growatt BMS provider requires explicit V1.04 selection`.
- Rename `O08 requires a valid interface` to `Growatt BMS provider requires a valid interface`.
- Rename `O08 requires a source and a successor generation` to `Growatt BMS provider requires a source and a successor generation`.

- Adopt `temperature_maximum` and `temperature_minimum` in the named fields of
  Growatt LINK, standard PCS and release-11 parallel PCS results, following the
  [public naming contract](https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/architecture/naming-contract.md).
  Consumers must adopt the same keys for field states and ages in the coordinated
  pre-1.0 wave. No alias, duplicate field, fallback or migration step is provided.
- Keep extended PCS temperature codes and unresolved raw fields provisional.
  Native identities, raw evidence, candidate grades, units, scale, admission and
  partial last-known-good behavior retain their existing contracts.
- Record the emitted shape and consumer obligations in [the naming inventory](naming-inventory.json)
  and [consumer inventory](naming-consumers.json). This artifact-changing class S
  change requires renewed affected security, hardware and candidate evidence
  before release. Synthetic tests establish no physical qualification.

Provider identifiers use behavior names for selection, replay, LINK, PCS and BMS providers. The offline rename is reproducible with `python3 scripts/rename_provider_names.py`. Native records, qualification and retained partial state are unchanged. Gateway adoption is coordinated in Project-Helianthus/helianthus-ebusgateway#1476.
