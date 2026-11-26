// Package bms defines native rows and SemReg storage bindings for the Growatt
// low-voltage BMS CAN V1.04 and V1.08 profiles, the separately selected common
// field projection, and the extended addressed monitor profile. Each table
// evaluates only its own profile's frames. The common projection uses only the
// selected member or aggregate scope and does not select a revision.
//
// ResourceRows and RelationRows define the BMS battery, module and cell
// resources and the system-to-battery, battery-to-pack and pack-to-interface
// relations. V1.04, V1.08 and the addressed monitor bind cell voltage extrema
// when the cell subject is resolved; V1.04 and the monitor also bind maximum
// cell temperature with a resolved cell subject. V1.08 average_temperature
// binds to the pack instead. The V1.08 0x324 serial response has native rows
// selected by the frame number's layout.
//
// # Native-only values
//
// These fields keep their native rows and evidence but produce no SemReg
// candidate:
//
//   - V1.04 and V1.08 protection_flags and warning_flags and their named
//     protection and warning bits (including V1.08 extended status bits): no
//     signed row pattern maps the bytes and bits to storage status symbols.
//     Common projection protection_flags and warning_flags stay raw without a
//     selected revision.
//   - V1.04, V1.08 and common charge_voltage_limit, charge_current_limit and
//     discharge_current_limit, and monitor permitted_charge_current and
//     permitted_discharge_current: these are device-reported limits, not
//     measurements; the read row has no value-limit carriage.
//   - V1.04 and V1.08 operating_state: no mapping from the four operating codes
//     to the storage operating symbols. For charge_enable, discharge_enable
//     and force_charge_request, no SemReg interlock condition class is defined.
//   - Monitor usoc, soh and asoc: the scale is specified but the unit is not.
//     Monitor protection/alarm, system fault, other error and hardware error
//     code bytes and named subfields: no severity mapping is assigned.
//   - Monitor cumulative_incoming_charge, cumulative_outgoing_charge,
//     cumulative_incoming_energy and cumulative_outgoing_energy: the integer
//     byte order is unqualified, so the four-byte spans remain raw.
//   - V1.08 derating_reason and its named bits: no storage element is defined.
//     V1.08 0x322 highest_cell_temperature, lowest_cell_temperature and their
//     indices: no binding associates these temperature extrema with SemReg
//     cell subjects.
//
// V1.04, V1.08 and common pack_voltage and pack_current each have native rows
// and individual bindings. Their derived power product is not a native field
// and produces no candidate: no signed read-row pattern combines the two operands.
package bms
