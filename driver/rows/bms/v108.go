package bms

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// V108Profile is the profile identifier of the Growatt low-voltage BMS CAN
// V1.08 profile, the identifier the frozen revision selection registers.
const V108Profile = "growatt.bms.low_voltage.can.v1_08"

// kelvinOffset is 273.15 K, the addend of the Celsius-to-kelvin conversion of
// the average temperature binding.
var kelvinOffset = semreg.Decimal{Coefficient: "27315", Exponent10: -2}

// v108BitRows returns one native row per named bit of the byte at byteIdx of
// the V1.08 profile; an empty name is a reserved or unnamed bit with no row.
func v108BitRows(id uint32, byteIdx int, names ...string) []roweval.NativeRow[Selector] {
	var rows []roweval.NativeRow[Selector]
	for bit, name := range names {
		if name != "" {
			rows = append(rows, nativeRowIn(V108Profile, id, name, signalSlice{offset: uint16(8*byteIdx + bit), width: 1, order: bitSlice}, "native.flag"))
		}
	}
	return rows
}

// withDomain returns the row with its native value bounded to [lo, hi]. A value
// outside the bounds gives the row no value, as the frozen decoder gives none.
func withDomain(r roweval.NativeRow[Selector], lo, hi string) roweval.NativeRow[Selector] {
	r.Domain = roweval.Bounds{Min: &semreg.Decimal{Coefficient: lo}, Max: &semreg.Decimal{Coefficient: hi}}
	return r
}

// serialLayoutQualifier is the qualifier the V1.08 frame evaluation sets for a
// 0x324 frame from its byte 0: the frame number zero, whose layout carries the
// battery identifier, or a later frame number, whose layout carries seven serial
// characters.
const (
	serialLayoutQualifier = "serial_layout"
	serialLayoutZero      = "frame_zero"
	serialLayoutLater     = "later_frame"
)

// serialRows are the native rows of the serial response 0x324 that depend on the
// frame number: the battery identifier of frame 0 at byte 1, and the serial
// characters, bytes 2 to 7 in frame 0 and bytes 1 to 7 in later frames. The two
// fragment rows share one name and differ in their layouts and conditions.
func serialRows() []roweval.NativeRow[Selector] {
	battery := nativeRowIn(V108Profile, 0x324, "serial_battery_id", signalSlice{8, 8, bitSlice, false}, "native.index")
	battery.When = serialWhen(serialLayoutZero)
	zero := nativeRowIn(V108Profile, 0x324, "serial_fragment", signalSlice{16, 48, bitSlice, false}, "native.raw")
	zero.When = serialWhen(serialLayoutZero)
	later := nativeRowIn(V108Profile, 0x324, "serial_fragment", signalSlice{8, 56, bitSlice, false}, "native.raw")
	later.When = serialWhen(serialLayoutLater)
	return []roweval.NativeRow[Selector]{battery, zero, later}
}

func serialWhen(layout string) roweval.When {
	return roweval.When{Equal: map[string]string{serialLayoutQualifier: layout}}
}

// serialLayoutOf is the layout qualifier of a 0x324 frame: frame number zero
// selects the frame 0 layout, any other frame number the later layout.
func serialLayoutOf(frameNumber byte) string {
	if frameNumber == 0 {
		return serialLayoutZero
	}
	return serialLayoutLater
}

// rowsAppliedTo returns the rows whose equal-value conditions the qualifiers
// meet. The evaluation keys its output by row identifier, so a row of another
// layout of the same name must not reach it; every V1.08 condition is an
// equal-value condition.
func rowsAppliedTo(rows []roweval.NativeRow[Selector], values map[string]string) []roweval.NativeRow[Selector] {
	var out []roweval.NativeRow[Selector]
	for _, r := range rows {
		applies := true
		for k, v := range r.When.Equal {
			if values[k] != v {
				applies = false
			}
		}
		if applies {
			out = append(out, r)
		}
	}
	return out
}

// v108Rows are the native rows of the V1.08 frames 0x311 to 0x325 that the
// frozen V1.08 report decoder emits. The shared fields sit at the V1.04 slices;
// the divergent fields are the status word with its states in bits 0 to 9, the
// derating bytes of 0x312, the average temperature and lifetime alarm of 0x313,
// the identity, version and update fields and the cell extremes of the later
// frames. Bits 10 and 11 of the status word are reserved and stay in the raw word.
// Frame 0x324 carries the serial record: its battery identifier and serial
// fragment have one row per layout, selected by the frame number (serialRows).
// The history bytes of 0x325 stay raw.
func v108Rows() []roweval.NativeRow[Selector] {
	rows := []roweval.NativeRow[Selector]{
		nativeRowIn(V108Profile, 0x311, "charge_voltage_limit", signalSlice{0, 16, bigEndian, false}, "native.decivolt"),
		nativeRowIn(V108Profile, 0x311, "charge_current_limit", signalSlice{16, 16, bigEndian, false}, "native.deciampere"),
		nativeRowIn(V108Profile, 0x311, "discharge_current_limit", signalSlice{32, 16, bigEndian, false}, "native.deciampere"),
		nativeRowIn(V108Profile, 0x311, "status_word", signalSlice{48, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x311, "master_operation_mode", signalSlice{48, 2, bitSlice, false}, "native.state"),
		nativeRowIn(V108Profile, 0x311, "operating_state", signalSlice{56, 2, bitSlice, false}, "native.state"),
	}
	rows = append(rows, v108BitRows(0x311, 7, "", "", "error_byte_valid", "cell_balance", "sleep", "discharge_output", "charge_output", "battery_terminal_open")...)

	// 0x312: protection and warning bytes with the V1.08 parallel bits of byte 1,
	// the pack count, the derating bytes B5 and B6 and the reserved byte B7.
	rows = append(rows,
		nativeRowIn(V108Profile, 0x312, "protection_flags", signalSlice{0, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x312, "warning_flags", signalSlice{16, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x312, "pack_count", signalSlice{32, 8, bitSlice, false}, "native.count"),
		nativeRowIn(V108Profile, 0x312, "derating_reason", signalSlice{40, 16, bitSlice, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x312, "reserved_byte_7", signalSlice{56, 8, bitSlice, false}, "native.raw"),
	)
	rows = append(rows, v108BitRows(0x312, 0, "protection_soft_start_failure", "protection_module_undervoltage", "protection_module_overvoltage", "protection_cell_undervoltage", "protection_cell_overvoltage", "protection_short_circuit_discharge", "protection_charge_overcurrent", "protection_discharge_overcurrent")...)
	rows = append(rows, v108BitRows(0x312, 1, "protection_parallel_version_disagreement", "protection_parallel_failure", "protection_delta_voltage_failure", "protection_system_error", "protection_charge_undertemperature", "protection_discharge_undertemperature", "protection_charge_overtemperature", "protection_discharge_overtemperature")...)
	rows = append(rows, v108BitRows(0x312, 2, "", "warning_module_undervoltage", "warning_module_overvoltage", "warning_cell_undervoltage", "warning_cell_overvoltage", "", "warning_charge_overcurrent", "warning_discharge_overcurrent")...)
	rows = append(rows, v108BitRows(0x312, 3, "warning_internal_communication_failure", "warning_before_shutdown", "warning_delta_voltage_failure", "", "warning_charge_undertemperature", "warning_discharge_undertemperature", "warning_charge_overtemperature", "warning_discharge_overtemperature")...)
	rows = append(rows, v108BitRows(0x312, 5, "derating_hardware_fault", "derating_full_charge", "derating_mos_temperature", "derating_ambient_temperature", "derating_precharge_fault", "derating_communication_fault", "derating_bus_fault")...)
	rows = append(rows, v108BitRows(0x312, 6, "derating_high_cell_voltage", "derating_low_cell_voltage", "derating_high_cell_temperature", "derating_low_cell_temperature", "derating_high_total_voltage", "derating_low_total_voltage", "derating_cell_voltage_difference", "derating_temperature_difference")...)

	// 0x313: the shared voltage, current, SOC and SOH, the average temperature
	// (signed, 0.1 degree) and the lifetime alarm, which is not a validity flag.
	rows = append(rows,
		nativeRowIn(V108Profile, 0x313, "pack_voltage", signalSlice{0, 16, bigEndian, true}, "native.centivolt"),
		nativeRowIn(V108Profile, 0x313, "pack_current", signalSlice{16, 16, bigEndian, true}, "native.deciampere"),
		nativeRowIn(V108Profile, 0x313, "average_temperature", signalSlice{32, 16, bigEndian, true}, "native.decicelsius"),
		nativeRowIn(V108Profile, 0x313, "soc", signalSlice{48, 8, bitSlice, false}, "native.percent"),
		nativeRowIn(V108Profile, 0x313, "soh", signalSlice{56, 7, bitSlice, false}, "native.percent"),
		nativeRowIn(V108Profile, 0x313, "lifetime_alarm", signalSlice{63, 1, bitSlice, false}, "native.flag"),
	)

	// 0x314 to 0x318: the same layout as the V1.04 profile.
	rows = append(rows, cellFrameRows(V108Profile)...)

	// 0x319: the battery type code 3 is not a type the decoder names, so the
	// row is bounded to 0 through 2 and gives no value for it.
	rows = append(rows, withDomain(nativeRowIn(V108Profile, 0x319, "battery_type", signalSlice{0, 2, bitSlice, false}, "native.state"), "0", "2"))
	rows = append(rows, v108BitRows(0x319, 0, "", "", "", "", "", "force_charge_request", "discharge_enable", "charge_enable")...)
	rows = append(rows,
		nativeRowIn(V108Profile, 0x319, "maximum_cell_voltage", signalSlice{8, 16, bigEndian, false}, "native.millivolt"),
		nativeRowIn(V108Profile, 0x319, "minimum_cell_voltage", signalSlice{24, 16, bigEndian, false}, "native.millivolt"),
		nativeRowIn(V108Profile, 0x319, "maximum_cell_index", signalSlice{40, 8, bitSlice, false}, "native.index"),
		nativeRowIn(V108Profile, 0x319, "minimum_cell_index", signalSlice{48, 8, bitSlice, false}, "native.index"),
		nativeRowIn(V108Profile, 0x319, "protected_pack_id", signalSlice{56, 8, bitSlice, false}, "native.index"),
	)

	// 0x320: manufacturer and hardware bytes, the little-endian software versions.
	rows = append(rows,
		nativeRowIn(V108Profile, 0x320, "manufacturer", signalSlice{0, 16, bigEndian, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x320, "hardware_version", signalSlice{16, 8, bitSlice, false}, "native.version"),
		nativeRowIn(V108Profile, 0x320, "software_version", signalSlice{24, 16, littleEndian, false}, "native.version"),
		nativeRowIn(V108Profile, 0x320, "highest_parallel_software_version", signalSlice{40, 16, littleEndian, false}, "native.version"),
	)

	// 0x321: update status and progress; the bytes the decoder names no field for
	// stay raw.
	rows = append(rows,
		nativeRowIn(V108Profile, 0x321, "update_status", signalSlice{0, 8, bitSlice, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x321, "pack_progress", signalSlice{8, 8, bitSlice, false}, "native.percent"),
		nativeRowIn(V108Profile, 0x321, "pack_programming_status", signalSlice{16, 8, bitSlice, false}, "native.state"),
		nativeRowIn(V108Profile, 0x321, "reserved_bytes", signalSlice{24, 40, bitSlice, false}, "native.raw"),
	)

	// 0x322: the highest and lowest cell temperatures in 0.1 degree, their cell
	// indices and the state of charge at each extreme. The frame names the cells
	// by index; no binding resolves them to a cell subject.
	rows = append(rows,
		nativeRowIn(V108Profile, 0x322, "highest_cell_temperature", signalSlice{0, 16, bigEndian, true}, "native.decicelsius"),
		nativeRowIn(V108Profile, 0x322, "lowest_cell_temperature", signalSlice{16, 16, bigEndian, true}, "native.decicelsius"),
		nativeRowIn(V108Profile, 0x322, "highest_temperature_index", signalSlice{32, 8, bitSlice, false}, "native.index"),
		nativeRowIn(V108Profile, 0x322, "lowest_temperature_index", signalSlice{40, 8, bitSlice, false}, "native.index"),
		nativeRowIn(V108Profile, 0x322, "highest_soc", signalSlice{48, 8, bitSlice, false}, "native.percent"),
		nativeRowIn(V108Profile, 0x322, "lowest_soc", signalSlice{56, 8, bitSlice, false}, "native.percent"),
	)

	// 0x323: the cell count, and the protection and warning bits the decoder
	// names; the other bits of bytes 6 and 7 are reserved and have no row.
	rows = append(rows, nativeRowIn(V108Profile, 0x323, "total_cell_count", signalSlice{0, 8, bitSlice, false}, "native.count"))
	rows = append(rows, v108BitRows(0x323, 4, "protection_charge_overpower", "protection_discharge_overpower", "protection_external_communication_fault", "protection_precharge_failure", "protection_hardware_fault", "protection_analog_front_end_communication_fault", "protection_cell_lost", "protection_total_current_sample_fault")...)
	rows = append(rows, v108BitRows(0x323, 5, "protection_total_voltage_sample_fault", "protection_load_side_voltage_sample_fault", "protection_calibration_parameter_load_fault", "protection_bus_reverse_connection", "protection_hardware_overvoltage", "protection_hardware_overcurrent", "protection_parallel_fault", "protection_primary_secondary_voltage_difference")...)
	rows = append(rows, v108BitRows(0x323, 6, "protection_hardware_discharge_overcurrent", "protection_charge_limit_failure", "protection_discharge_limit_failure", "protection_main_path_open")...)
	rows = append(rows, v108BitRows(0x323, 7, "warning_charge_overpower", "warning_discharge_overpower", "warning_internal_charge_circulation", "warning_internal_discharge_circulation")...)

	// 0x324 and 0x325: the frame number. The battery number and the serial
	// fragment of 0x324 have one layout in frame 0 and another in later frames,
	// selected by the layout qualifier (serialRows). The history bytes of 0x325
	// sit at a fixed position and stay raw.
	rows = append(rows, nativeRowIn(V108Profile, 0x324, "serial_frame_number", signalSlice{0, 8, bitSlice, false}, "native.raw"))
	rows = append(rows, serialRows()...)
	rows = append(rows,
		nativeRowIn(V108Profile, 0x325, "frame_number", signalSlice{0, 8, bitSlice, false}, "native.raw"),
		nativeRowIn(V108Profile, 0x325, "battery_id", signalSlice{8, 8, bitSlice, false}, "native.index"),
		nativeRowIn(V108Profile, 0x325, "history_bytes", signalSlice{16, 48, bitSlice, false}, "native.raw"),
	)
	return rows
}

// V108Table returns the native rows and the bindings of the V1.08 profile. The
// shared SOC, SOH, pack voltage and pack current bind as they do in the V1.04
// table, with two differences: the SOH is published for either state of the
// lifetime alarm, because bit 7 is not a validity flag in V1.08, and the
// average temperature binds to the pack temperature in kelvin only. The
// bindings declare no write; the V1.08 page documents no command that sets
// these values.
//
// The cell voltage extrema of 0x319 are bound to the cell subject by cellBindings
// when the host resolves a cell; the 0x322 cell temperatures stay native. The protection and
// warning symbols and the device limits have no binding here, as in the V1.04
// table: they are listed in the package report.
func V108Table() roweval.Table[Selector] {
	average := packReadIn(V108Profile, "average_temperature", "CAN 0x313", "storage.temperature.pack", "unit.kelvin", 10, "")
	average.Read.ToRegistry.Numeric = &roweval.Affine{Num: 1, Den: 10, Add: kelvinOffset}
	return roweval.Table[Selector]{
		Native: v108Rows(),
		Bindings: []roweval.BindingRow{
			packReadIn(V108Profile, "pack_current", "CAN 0x313", "storage.pack.current", "unit.ampere", 10, ""),
			packReadIn(V108Profile, "pack_voltage", "CAN 0x313", "storage.pack.voltage", "unit.volt", 100, ""),
			packReadIn(V108Profile, "soc", "CAN 0x313", "storage.state.soc", "unit.ratio", 100, ""),
			packReadIn(V108Profile, "soh", "CAN 0x313", "storage.state.soh", "unit.ratio", 100, ""),
			average,
		},
	}
}

// V108FrameDisposition is the frame gate of the V1.08 profile: the frozen
// growattV108Valid rule for the frames it admits. Count byte 4 of 0x312 is
// admitted at 1 through 254 and byte 7 is not a count in this revision; the SOC
// of 0x313 is admitted at 100 or less; the software version field of 0x320 is
// admitted at 1 through 9 in byte 2; the SOC pair of 0x322 at 100 or less; the
// cell count of 0x323 at 1 through 254; and the 0x321 progress byte at 100 or
// less. The other admitted frames are admitted by identifier and geometry.
func V108FrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	p := frame.Bytes()
	switch frame.ID().Value() {
	case 0x311, 0x314, 0x315, 0x316, 0x317, 0x318, 0x319, 0x324, 0x325:
	case 0x312:
		if p[4] < 1 || p[4] > 254 {
			return "not valid"
		}
	case 0x313:
		if p[6] > 100 {
			return "not valid"
		}
	case 0x320:
		if p[2] < 1 || p[2] > 9 {
			return "not valid"
		}
	case 0x321:
		if p[1] > 100 {
			return "not valid"
		}
	case 0x322:
		if p[6] > 100 || p[7] > 100 {
			return "not valid"
		}
	case 0x323:
		if p[0] < 1 || p[0] > 254 {
			return "not valid"
		}
	default:
		return roweval.ReasonNoMatch
	}
	return ""
}

// EvaluateV108Frame evaluates one frame under the V1.08 profile. A frame the
// gate rejects gives its reason and no evaluation.
func EvaluateV108Frame(frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	if reason := V108FrameDisposition(frame); reason != "" {
		return nil, reason, nil
	}
	table := withCellBindings(V108Table())
	if frame.ID().Value() == 0x324 && !frame.ID().Extended() {
		values := make(map[string]string, len(a.Qualifiers.Values)+1)
		for k, v := range a.Qualifiers.Values {
			values[k] = v
		}
		values[serialLayoutQualifier] = serialLayoutOf(frame.Bytes()[0])
		a.Qualifiers.Values = values
		table.Native = rowsAppliedTo(table.Native, values)
	}
	return evaluateAdmitted(table, frame, a)
}
