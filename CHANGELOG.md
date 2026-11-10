# Changelog

## Unreleased

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
