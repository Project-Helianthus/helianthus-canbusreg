---
status: "accepted source-specific read-only mapping gate for the low-voltage BMS family over CAN; physical qualification remains false and no transmission is granted"
---

# Low-voltage BMS CAN storage mapping gate

This accepted, source-specific gate states how the CAN profiles of the
low-voltage BMS family map onto `helianthus.pack.storage` at `1.1.0`. It is
not a device support claim and not a physical qualification. Its
machine-readable companion is
[the mapping record](growatt-bms-can-storage-v1.json).

The six profiles are `v1_04`, `pcs_standard`, `pcs_extended`, `pcs_gauge`,
`pcs_parallel` and `link`. The caller selects one. A profile does not select
another profile, and an identifier match does not. Admission is fail-closed.
A partial last-known-good value stays only until identity or admission fails.
Missing evidence is withheld. It is never replaced with zero.

No bus request, device write or field installation is part of this gate.
`outbound_allowed` is false. The gate publishes no consumer binding. MCP
compatibility and GraphQL parity are unchanged because this gate adds no
consumer surface.

## Conversions

Canonical units are SI. A native engineering value keeps its native unit in
the evidence. The canonical value is that engineering value times a factor
plus an offset.

| Native unit | Canonical unit | Rule |
| --- | --- | --- |
| percent | `unit.ratio` | percent / 100 |
| degree Celsius | `unit.kelvin` | degree Celsius + 273.15 |
| kilowatt-hour | `unit.joule` | kilowatt-hour times 3 600 000 |
| ampere-hour | `unit.ampere_hour` | ampere-hour unchanged |
| volt | `unit.volt` | identity |
| ampere | `unit.ampere` | identity |
| millivolt | `unit.volt` | millivolt / 1000 |

No row of these six profiles carries a kilowatt-hour quantity. The
kilowatt-hour rule is stated so a later native energy field cannot be given
another factor. An ampere-hour estimate is not a lifetime charge counter and
is not derived into joule. Those rows are `not_admitted`.

A conversion that is not the identity, and is not an unchanged ampere-hour or
an unchanged count, declares a reversible `unit` loss. Pack current keeps the
native sign convention as a reversible `provenance` loss.

## Disposition

`mapped` names a fact that `helianthus.pack.storage` at `1.1.0` already
defines. The dimension is the fact's catalog dimension: pack, cell or, where
the catalog says so, another subject. A mapped pack fact is emitted. A mapped
cell fact is stated and is not emitted, because this gate does not publish a
cell resource.

`waiting` names a package E element that the pack does not define yet. The
field is not emitted. The element ids are:

| Concern | Element | Dimension |
| --- | --- | --- |
| voltage limit | `storage.limit.charge_voltage` | interface |
| current limit | `storage.limit.charge_current` | interface |
| current limit | `storage.limit.discharge_current` | interface |
| cycle count | `storage.state.cycle_count` | pack |
| per-cell voltage | `storage.measurement.cell_voltage` | cell |
| chemistry | `storage.attribute.chemistry` | pack |
| insulation resistance | `storage.measurement.insulation_resistance` | pack |

Insulation resistance is named so the wait has an element id. None of these
six profiles carries an insulation-resistance field.

`not_admitted` is a native quantity that is neither an accepted pack fact nor
one of those package E elements. The reason is on the row. The native unit
still follows the conversion table when the unit is known.

Grades are `Proven`, `Hypothesis` and `Unknown`. A grade qualifies the row.
It does not qualify a device.

## Dimensions and qualifiers

The dimensions are pack, module, cell and interface. Bank and slot are
qualifiers, not dimensions. A parallel row carries `bank=caller_resolved_bank`
because the frame does not select the bank. A stack row carries
`slot=stack_address`. A module row carries `slot=module_source`. A per-cell
row also carries `slot=cell_index`, and a temperature slot carries
`slot=sensor_index`.

## Resources

The gate states the pack resource asked for by section 15 of the
[graph contract](../graph.md). The pack is anchored by an operator label.
The catalog lists no identification scheme for this family, so no pack token
is recorded as stable. Its stability is Unknown.

Every emitted fact has the battery pack as its subject. The gate publishes no
battery, module, cell, string, interface or storage system, and it states no
relation. Every Storage relation stays unknown, not absent. A second view of
the pack joins only by an operator declaration. The profile and the interface
name are provenance selectors. The interface name is never an anchor and never
identity evidence. The gate states no plane membership.

| Storage fact | Subject kind | Measurement point |
| --- | --- | --- |
| `storage.pack.voltage` | `resource.battery_pack` | `pack_terminals` |
| `storage.pack.current` | `resource.battery_pack` | `pack_terminals` |
| `storage.state.soc` | `resource.battery_pack` | `pack_as_a_whole` |
| `storage.state.soh` | `resource.battery_pack` | `pack_as_a_whole` |

Cell extrema use `storage.cell.voltage_maximum` and
`storage.cell.voltage_minimum` on the cell dimension. They are mapped and not
emitted. Per-cell voltage waits for `storage.measurement.cell_voltage`.

## Profiles

## v1_04

Selector `growatt.bms.low_voltage.can.v1_04`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `charge_voltage_limit` | `0x311` | `storage.limit.charge_voltage` | `interface` | none | `identity` | `waiting` | Proven |
| `charge_current_limit` | `0x311` | `storage.limit.charge_current` | `interface` | none | `identity` | `waiting` | Proven |
| `discharge_current_limit` | `0x311` | `storage.limit.discharge_current` | `interface` | none | `identity` | `waiting` | Proven |
| `pack_voltage` | `0x313` | `storage.pack.voltage` | `pack` | none | `identity` | `mapped` | Proven |
| `pack_current` | `0x313` | `storage.pack.current` | `pack` | none | `identity` | `mapped` | Proven |
| `maximum_cell_temperature` | `0x313` | `storage.temperature.cell` | `cell` | none | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `state_of_charge` | `0x313` | `storage.state.soc` | `pack` | none | `percent_over_100` | `mapped` | Proven |
| `state_of_health` | `0x313` | `storage.state.soh` | `pack` | none | `percent_over_100` | `mapped` | Proven |
| `remaining_capacity` | `0x314` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | none | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `full_charge_capacity` | `0x314` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | none | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `cell_voltage_spread` | `0x314` | `spread_is_not_a_cell_voltage` | `cell` | none | `millivolt_to_volt` | `not_admitted` | Proven |
| `cycle_count` | `0x314` | `storage.state.cycle_count` | `pack` | none | `count_unchanged` | `waiting` | Proven |
| `cell_voltage` | `0x315` | `storage.measurement.cell_voltage` | `cell` | slot=cell_index | `millivolt_to_volt` | `waiting` | Proven |
| `maximum_cell_voltage` | `0x319` | `storage.cell.voltage_maximum` | `cell` | none | `millivolt_to_volt` | `mapped` | Proven |
| `minimum_cell_voltage` | `0x319` | `storage.cell.voltage_minimum` | `cell` | none | `millivolt_to_volt` | `mapped` | Proven |
| `chemistry` | `0x319` | `storage.attribute.chemistry` | `pack` | none | `none` | `waiting` | Proven |

## pcs_standard

Selector `growatt.bms.low_voltage.can.pcs.standard`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `charge_voltage_limit` | `0x311` | `storage.limit.charge_voltage` | `interface` | none | `identity` | `waiting` | Proven |
| `charge_current_limit` | `0x311` | `storage.limit.charge_current` | `interface` | none | `identity` | `waiting` | Hypothesis |
| `discharge_current_limit` | `0x311` | `storage.limit.discharge_current` | `interface` | none | `identity` | `waiting` | Hypothesis |
| `pack_voltage` | `0x313` | `storage.pack.voltage` | `pack` | none | `identity` | `mapped` | Proven |
| `output_port_voltage` | `0x313` | `output_port_is_not_pack_voltage` | `pack` | none | `identity` | `not_admitted` | Proven |
| `pack_current` | `0x313` | `storage.pack.current` | `pack` | none | `identity` | `mapped` | Proven |
| `reported_temperature` | `0x313` | `storage.temperature.cell` | `cell` | none | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `state_of_charge` | `0x313` | `storage.state.soc` | `pack` | none | `percent_over_100` | `mapped` | Proven |
| `state_of_health` | `0x313` | `storage.state.soh` | `pack` | none | `percent_over_100` | `mapped` | Proven |
| `remaining_capacity` | `0x314` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | none | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `full_charge_capacity` | `0x314` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | none | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `cell_voltage_spread` | `0x314` | `spread_is_not_a_cell_voltage` | `cell` | none | `millivolt_to_volt` | `not_admitted` | Proven |
| `cycle_count` | `0x314` | `storage.state.cycle_count` | `pack` | none | `count_unchanged` | `waiting` | Proven |
| `maximum_cell_voltage` | `0x319` | `storage.cell.voltage_maximum` | `cell` | none | `millivolt_to_volt` | `mapped` | Proven |
| `minimum_cell_voltage` | `0x319` | `storage.cell.voltage_minimum` | `cell` | none | `millivolt_to_volt` | `mapped` | Proven |
| `temperature_maximum` | `0x322` | `storage.temperature.cell` | `cell` | none | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `temperature_minimum` | `0x322` | `storage.temperature.cell` | `cell` | none | `celsius_plus_273_15` | `mapped` | Hypothesis |

## pcs_extended

Selector `growatt.bms.low_voltage.can.pcs.extended`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `pack_voltage` | `0x1902` | `storage.pack.voltage` | `pack` | slot=stack_address | `identity` | `mapped` | Proven |
| `port_voltage` | `0x1902` | `port_voltage_is_not_pack_voltage` | `pack` | slot=stack_address | `identity` | `not_admitted` | Proven |
| `pack_current` | `0x1902` | `storage.pack.current` | `pack` | slot=stack_address | `identity` | `mapped` | Proven |
| `state_of_charge` | `0x1902` | `storage.state.soc` | `pack` | slot=stack_address | `percent_over_100` | `mapped` | Proven |
| `limit_word_0` | `0x1903` | `limit_quantity_unassigned` | `interface` | slot=stack_address | `unassigned` | `not_admitted` | Unknown |
| `limit_word_1` | `0x1903` | `limit_quantity_unassigned` | `interface` | slot=stack_address | `unassigned` | `not_admitted` | Unknown |
| `full_charge_capacity` | `0x1904` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | slot=stack_address | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `remaining_capacity` | `0x1904` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | slot=stack_address | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `cycle_count` | `0x1904` | `storage.state.cycle_count` | `pack` | slot=stack_address | `count_unchanged` | `waiting` | Proven |
| `maximum_cell_voltage` | `0x1905` | `storage.cell.voltage_maximum` | `cell` | slot=stack_address | `millivolt_to_volt` | `mapped` | Proven |
| `minimum_cell_voltage` | `0x1905` | `storage.cell.voltage_minimum` | `cell` | slot=stack_address | `millivolt_to_volt` | `mapped` | Proven |
| `temperature_maximum` | `0x1906` | `storage.temperature.cell` | `cell` | slot=stack_address | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `temperature_minimum` | `0x1906` | `storage.temperature.cell` | `cell` | slot=stack_address | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `state_of_health` | `0x1906` | `storage.state.soh` | `pack` | slot=stack_address | `percent_over_100` | `mapped` | Hypothesis |

## pcs_gauge

Selector `growatt.bms.low_voltage.can.pcs.gauge`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `full_charge_capacity` | `0x316` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | none | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `fine_cycle_count` | `0x316` | `storage.state.cycle_count` | `pack` | none | `count_unchanged` | `waiting` | Proven |
| `cell_source_word` | `0x315` | `storage.measurement.cell_voltage` | `cell` | slot=cell_index | `millivolt_to_volt` | `waiting` | Hypothesis |
| `biased_accumulator` | `0x316` | `accumulator_is_not_ampere_hour` | `pack` | none | `unassigned` | `not_admitted` | Proven |

## pcs_parallel

Selector `growatt.bms.low_voltage.can.pcs.parallel`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `summary_voltage` | `0x61X` | `multiplexer_selects_the_quantity` | `pack` | bank=caller_resolved_bank | `unassigned` | `not_admitted` | Unknown |
| `summary_current` | `0x61X` | `storage.pack.current` | `pack` | bank=caller_resolved_bank | `identity` | `mapped` | Hypothesis |
| `port_voltage` | `0x61X` | `port_voltage_is_not_pack_voltage` | `pack` | bank=caller_resolved_bank | `identity` | `not_admitted` | Proven |
| `state_of_charge` | `0x61X` | `storage.state.soc` | `pack` | bank=caller_resolved_bank | `percent_over_100` | `mapped` | Hypothesis |
| `limit_word_0` | `0x62X` | `limit_quantity_unassigned` | `interface` | bank=caller_resolved_bank | `unassigned` | `not_admitted` | Unknown |
| `limit_word_1` | `0x62X` | `limit_quantity_unassigned` | `interface` | bank=caller_resolved_bank | `unassigned` | `not_admitted` | Unknown |
| `full_charge_capacity` | `0x63X` | `capacity_unit_unassigned` | `pack` | bank=caller_resolved_bank | `unassigned` | `not_admitted` | Unknown |
| `remaining_capacity` | `0x63X` | `capacity_unit_unassigned` | `pack` | bank=caller_resolved_bank | `unassigned` | `not_admitted` | Unknown |
| `cycle_count` | `0x63X` | `storage.state.cycle_count` | `pack` | bank=caller_resolved_bank | `count_unchanged` | `waiting` | Unknown |
| `maximum_cell_voltage` | `0x64X` | `storage.cell.voltage_maximum` | `cell` | bank=caller_resolved_bank | `millivolt_to_volt` | `mapped` | Proven |
| `minimum_cell_voltage` | `0x64X` | `storage.cell.voltage_minimum` | `cell` | bank=caller_resolved_bank | `millivolt_to_volt` | `mapped` | Proven |
| `temperature_maximum` | `0x65X` | `storage.temperature.cell` | `cell` | bank=caller_resolved_bank | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `temperature_minimum` | `0x65X` | `storage.temperature.cell` | `cell` | bank=caller_resolved_bank | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `cell_sum_voltage` | `0x6DX` | `sum_is_not_one_cell_voltage` | `pack` | bank=caller_resolved_bank | `identity` | `not_admitted` | Proven |

## link

Selector `growatt.bms.low_voltage.can.link`. The caller selects this profile. It does not select any other profile.

| Source | Frame | Element | Dimension | Qualifiers | Conversion | Disposition | Grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `module_voltage` | `0x1805` | `module_report_is_not_the_pack` | `module` | slot=module_source | `identity` | `not_admitted` | Proven |
| `module_current` | `0x1805` | `module_report_is_not_the_pack` | `module` | slot=module_source | `identity` | `not_admitted` | Proven |
| `port_voltage` | `0x1805` | `port_voltage_is_not_pack_voltage` | `module` | slot=module_source | `identity` | `not_admitted` | Proven |
| `charge_limit` | `0x1805` | `storage.limit.charge_current` | `interface` | slot=module_source | `unassigned` | `waiting` | Unknown |
| `discharge_limit` | `0x1805` | `storage.limit.discharge_current` | `interface` | slot=module_source | `unassigned` | `waiting` | Unknown |
| `maximum_cell_voltage` | `0x1806` | `storage.cell.voltage_maximum` | `cell` | slot=module_source | `millivolt_to_volt` | `mapped` | Proven |
| `minimum_cell_voltage` | `0x1806` | `storage.cell.voltage_minimum` | `cell` | slot=module_source | `millivolt_to_volt` | `mapped` | Proven |
| `mean_cell_voltage` | `0x1806` | `mean_is_not_a_cell_or_an_extremum` | `cell` | slot=module_source | `millivolt_to_volt` | `not_admitted` | Proven |
| `cell_voltage` | `0x1810` | `storage.measurement.cell_voltage` | `cell` | slot=module_source, slot=cell_index | `millivolt_to_volt` | `waiting` | Proven |
| `temperature_maximum` | `0x1808` | `storage.temperature.cell` | `cell` | slot=module_source | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `temperature_minimum` | `0x1808` | `storage.temperature.cell` | `cell` | slot=module_source | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `temperature_slot` | `0x1830` | `storage.temperature.cell` | `cell` | slot=module_source, slot=sensor_index | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `fine_state_of_charge` | `0x1807` | `module_report_is_not_the_pack` | `module` | slot=module_source | `percent_over_100` | `not_admitted` | Hypothesis |
| `full_charge_capacity` | `0x180B` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | slot=module_source | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `remaining_capacity` | `0x180B` | `ampere_hour_estimate_is_not_a_pack_element` | `pack` | slot=module_source | `ampere_hour_unchanged` | `not_admitted` | Proven |
| `fine_cycle_count` | `0x180B` | `storage.state.cycle_count` | `pack` | slot=module_source | `count_unchanged` | `waiting` | Proven |
| `fine_temperature_maximum` | `0x1862` | `storage.temperature.cell` | `cell` | slot=module_source | `celsius_plus_273_15` | `mapped` | Hypothesis |
| `fine_temperature_minimum` | `0x1862` | `storage.temperature.cell` | `cell` | slot=module_source | `celsius_plus_273_15` | `mapped` | Hypothesis |

## Validation

Run `python3 scripts/validate_growatt_bms_can_storage_mapping.py` and
`python3 scripts/test_validate_growatt_bms_can_storage_mapping.py`.
