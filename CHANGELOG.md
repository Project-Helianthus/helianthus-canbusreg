# Changelog

## Unreleased

- `GrowattEnergyCoverage` states which storage quantities the common projection
  and the V1.04 projection set. Voltage, current, state of charge, the
  state-of-health value, and the charge-voltage, charge-current and
  discharge-current limits are supported on both. Remaining energy, usable
  capacity, nominal capacity and charge capacity in ampere-hours stay
  unsupported on both, because neither projection decodes them. The
  state-of-health valid bit and maximum cell temperature are supported only
  on the V1.04 projection. Frame decoding, scope, raw evidence and admission
  are unchanged. No physical qualification is claimed.

- The selected-profile replay and the PCS provider keep the extrema of the
  location-qualified standard record `330` under the retention key `core/330`,
  so its `maximum_index`, `minimum_index`, `maximum_cell` and `minimum_cell`
  appear as `core/330/<field>`. Before this change they shared the key `core`
  with the same names from `319`, and whichever record arrived later
  overwrote the other's retained values where a selection accepts both. The
  `319` extrema and every other core field keep their `core/<field>` names.
  The gateway adopts the new `330` names by re-pinning this revision; no
  alias or migration step is provided.
- `DecodePCSExtended` decodes the gateway-state `state` field of `1910` on
  route `ff`/`f4` only for the gateway selection. Other extended selections
  that reach the record keep `state` raw with an `Unknown` grade, as they
  already kept `downstream_address`. Admitted fields of the receive-only
  candidate are unchanged; no physical qualification is claimed.

- `NewSelection` refuses a PCS flavor asserted with the bank it does not pair
  with and returns `ErrSelectionBankPairing`: `pcs-standard` pairs with
  `ordinary` and `pcs-parallel` with `parallel`, as the
  [topology context values](https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-common-projection-v1.md#topology-context-values)
  state. Before this change a standard PCS selection with the parallel bank
  decoded the documented `326` frame with the parallel offset as a large
  negative slot current. `pcs-extended19` and LINK selections keep accepting
  either bank, because the table names no pairing for them. Endpoint, group
  and slot values are accepted as before; closing those vocabularies waits for
  a gateway change.
- The common projection gains `Scope`. `NewGrowattLowVoltageBMSCommonProjector`
  projects `unknown`; the new `NewGrowattLowVoltageBMSCommonProjectorFromSelection`
  projects `aggregate` for a selection with scope `aggregate` and `member` for
  scope `local`, so a hub-aggregated pack count is never presented as a
  single-BMS count. Frame decoding, raw evidence and admission are unchanged.
- The gateway's configuration already asserts paired values. The gateway adopts
  both changes by re-pinning this revision; its selection tests that vary the
  flavor or the bank alone through `NewSelection` move to paired values in the
  same re-pin. No alias or migration step is provided. This artifact-changing
  change requires renewed release evidence before the final bill of materials
  freeze; no physical qualification is claimed.

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
