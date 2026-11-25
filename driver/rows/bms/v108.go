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

// v108Rows are the native rows of the V1.08 frames 0x311 to 0x313. The shared
// fields sit at the V1.04 slices; the divergent fields are the status word
// with its states in bits 0 to 9, the derating bytes of 0x312 and the average
// temperature and lifetime alarm of 0x313, as the frozen V1.08 report decoder
// names them.
// Bits 10 and 11 of the status word are reserved and stay in the raw word.
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
		opaqueRowIn(V108Profile, 0x312, "derating_reason", signalSlice{40, 16, bitSlice, false}, "native.raw"),
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
	return rows
}

// opaqueRowIn is the opaque native row of the named profile: a raw span with
// no qualified integer reading.
func opaqueRowIn(profile string, id uint32, name string, slice signalSlice, unit string) roweval.NativeRow[Selector] {
	r := nativeRowIn(profile, id, name, slice, unit)
	r.Wire.Codec = opaqueCodec
	return r
}

// V108Table returns the native rows and the bindings of the V1.08 profile. The
// shared SOC, SOH, pack voltage and pack current bind as they do in the V1.04
// table, with two differences: the SOH is published for either state of the
// lifetime alarm, because bit 7 is not a validity flag in V1.08, and the
// average temperature binds to the pack temperature in kelvin only. The
// bindings declare no write; the V1.08 page documents no command that sets
// these values.
//
// The protection and warning symbols and the device limits have no binding
// here, as in the V1.04 table: they are listed in the package report.
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

// V108FrameDisposition is the frame gate of the V1.08 profile, the frozen
// growattV108Valid rule for frames 0x311 to 0x313: the count byte 4 of 0x312 is
// admitted at 1 through 254 and byte 7 is not a count in this revision; the SOC
// of 0x313 is admitted at 100 or less.
func V108FrameDisposition(frame canbus.Frame) roweval.Reason {
	if frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	p := frame.Bytes()
	switch frame.ID().Value() {
	case 0x311:
	case 0x312:
		if p[4] < 1 || p[4] > 254 {
			return "not valid"
		}
	case 0x313:
		if p[6] > 100 {
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
	return evaluateAdmitted(V108Table(), frame, a)
}
