package bms

import (
	"fmt"
	"strconv"

	"github.com/Project-Helianthus/helianthus-canbus"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"
)

// MonitorProfile is the profile identifier of the Growatt low-voltage BMS
// addressed monitor profile. The frozen decoder declares no identifier for it,
// so this name is chosen here and the package report states it.
const MonitorProfile = "growatt.bms.low_voltage.can.addressed_monitor.v1"

// monitorKind is the layout kind of one addressed monitor field, the kinds the
// frozen decoder's layout table uses.
type monitorKind uint8

const (
	mWord        monitorKind = iota // little-endian unsigned 16-bit word
	mSignedWord                     // little-endian signed 16-bit word
	mSignedOctet                    // signed 8-bit byte
	mBits                           // bit field of one byte: shift and bits
	mRunMode                        // run mode code, one byte
	mWorkMode                       // work mode code, one byte
	mRaw                            // four raw bytes with an unqualified integer byte order
)

// monitorField is one live field of an addressed monitor family: its payload
// position, its kind, the bit field of a mBits field, the offset and the
// denominator the page states (value = (raw + offset) / denominator), and its
// native unit. A denominator of zero is one.
type monitorField struct {
	name        string
	kind        monitorKind
	at, width   int
	shift, bits uint8
	offset      int64
	denominator int64
	unit        string
}

// monitorBase returns the base identifier of an addressed monitor frame: the
// system form keeps the controller address in the low byte; the module forms
// (families 08, 10 and 14) keep the module address in bits 20 to 23 as well.
func monitorBase(id uint32) (uint32, bool) {
	switch id >> 24 {
	case 0x04:
		return id & 0xffffff00, true
	case 0x08, 0x10, 0x14:
		return id & 0xff0fff00, true
	}
	return 0, false
}

// MonitorFrameDisposition is the frame gate of the addressed monitor profile: an
// extended identifier with DLC 8 and raw DLC 8 whose base identifier has live
// fields. Any other frame gives roweval.ReasonNoMatch. The controller and module
// addresses are protocol addresses of the frame and carry no gate of their own.
func MonitorFrameDisposition(frame canbus.Frame) roweval.Reason {
	if !frame.ID().Extended() || frame.DLC() != 8 || frame.RawDLC() != 8 {
		return roweval.ReasonNoMatch
	}
	base, ok := monitorBase(frame.ID().Value())
	if !ok {
		return roweval.ReasonNoMatch
	}
	if _, ok := monitorLayouts[base]; !ok {
		return roweval.ReasonNoMatch
	}
	return ""
}

// monitorDecimal is the normalised decimal of an integer: the coefficient carries
// no trailing zero, and the zeros move to the exponent, as the pinned validator
// requires. The generator checks that every offset divides evenly by its
// denominator, so the offset is exact.
func monitorDecimal(n int64) semreg.Decimal {
	d := semreg.Decimal{}
	for n != 0 && n%10 == 0 {
		n /= 10
		d.Exponent10++
	}
	d.Coefficient = strconv.FormatInt(n, 10)
	return d
}

// monitorNativeRow is the native row of one addressed monitor field at the
// identifier of its frame. The row carries the scale and offset the page states,
// so its native value is in the native unit the page states.
func monitorNativeRow(id uint32, f monitorField) roweval.NativeRow[Selector] {
	var slice signalSlice
	order := bitSlice
	switch f.kind {
	case mWord:
		slice, order = signalSlice{offset: uint16(8 * f.at), width: 16, order: littleEndian}, littleEndian
	case mSignedWord:
		slice, order = signalSlice{offset: uint16(8 * f.at), width: 16, order: littleEndian, signed: true}, littleEndian
	case mSignedOctet:
		slice = signalSlice{offset: uint16(8 * f.at), width: 8, order: bitSlice, signed: true}
	case mBits:
		slice = signalSlice{offset: uint16(8*f.at) + uint16(f.shift), width: uint16(f.bits), order: bitSlice}
	case mRunMode, mWorkMode:
		slice = signalSlice{offset: uint16(8 * f.at), width: 8, order: bitSlice}
	case mRaw:
		slice = signalSlice{offset: uint16(8 * f.at), width: uint16(8 * f.width), order: bitSlice}
	}
	codec := "unsigned"
	if slice.signed {
		codec = "signed"
	}
	r := roweval.NativeRow[Selector]{
		ID:      MonitorProfile + "." + f.name,
		Unit:    f.unit,
		SpecRef: fmt.Sprintf("addressed monitor %#x", id),
		At:      Selector{Identifier: id, Extended: true, DLC: 8, RawDLC: 8, Slice: slice},
		Wire:    roweval.Layout{BitOffset: slice.offset, Width: slice.width, Codec: codec, ByteOrder: order, BitOrder: "lsb0", WordOrder: "single"},
	}
	den := f.denominator
	if den == 0 {
		den = 1
	}
	if den != 1 || f.offset != 0 {
		r.Decode = roweval.Convert{Numeric: &roweval.Affine{Num: 1, Den: den, Add: monitorDecimal(f.offset / den)}}
	}
	return r
}

// MonitorTable returns the native rows of the addressed monitor frame at one
// identifier, and the bindings of the pack voltage and current that the pack
// resource of the caller receives. The rows are those of the identifier's base
// family, so a row of one controller or module address never applies to a frame
// of another. A monitor identifier whose base has no layout gives an empty table.
//
// The bindings are the internal pack voltage and the internal pack current of
// the first family only. The cell extrema, the cell temperature, the usable
// state of charge and health, the protection words, the device limits and the
// cumulative counters are not bound here; see the package report.
func MonitorTable(identifier uint32) roweval.Table[Selector] {
	base, ok := monitorBase(identifier)
	if !ok {
		return roweval.Table[Selector]{}
	}
	layout, ok := monitorLayouts[base]
	if !ok {
		return roweval.Table[Selector]{}
	}
	rows := make([]roweval.NativeRow[Selector], 0, len(layout))
	for _, f := range layout {
		rows = append(rows, monitorNativeRow(identifier, f))
	}
	t := roweval.Table[Selector]{Native: rows}
	if base == 0x04010000 {
		t.Bindings = []roweval.BindingRow{
			packReadIn(MonitorProfile, "internal_pack_voltage", "addressed monitor 0x04010000", "storage.pack.voltage", "unit.volt", 1, ""),
			packReadIn(MonitorProfile, "current", "addressed monitor 0x04010000", "storage.pack.current", "unit.ampere", 1, ""),
		}
	}
	return t
}

// EvaluateMonitorFrame gates one addressed monitor frame and, when it is
// admitted, evaluates the rows of its own identifier and their bindings.
func EvaluateMonitorFrame(frame canbus.Frame, a roweval.Acquired[Selector]) (map[string]roweval.Evaluation, roweval.Reason, error) {
	if reason := MonitorFrameDisposition(frame); reason != "" {
		return nil, reason, nil
	}
	return evaluateAdmitted(MonitorTable(frame.ID().Value()), frame, a)
}

// monitorLayouts are the live fields of the addressed monitor families, keyed by the
// base identifier the frame gate derives, in the order the frozen decoder declares
// them. The table mirrors the frozen decoder's layout field by field; the sweep
// test compares every row with the decoder's output over several payloads.
var monitorLayouts = map[uint32][]monitorField{
	0x04010000: {
		{"internal_pack_voltage", mWord, 0, 2, 0, 0, 0, 10, "native.volt"},
		{"current", mWord, 2, 2, 0, 0, -30000, 10, "native.ampere"},
		{"output_port_voltage", mWord, 4, 2, 0, 0, 0, 10, "native.volt"},
		{"fuse_voltage", mWord, 6, 2, 0, 0, 0, 10, "native.volt"},
	},
	0x04020000: {
		{"maximum_cell_voltage", mWord, 0, 2, 0, 0, 0, 1000, "native.volt"},
		{"minimum_cell_voltage", mWord, 2, 2, 0, 0, 0, 1000, "native.volt"},
		{"position_of_maximum_voltage_cell", mWord, 4, 2, 0, 0, 0, 1, "native.raw"},
		{"position_of_minimum_voltage_cell", mWord, 6, 2, 0, 0, 0, 1, "native.raw"},
	},
	0x04030000: {
		{"maximum_cell_temperature", mWord, 0, 2, 0, 0, -400, 10, "native.celsius"},
		{"minimum_cell_temperature", mWord, 2, 2, 0, 0, -400, 10, "native.celsius"},
		{"position_of_maximum_temperature_sensor", mWord, 4, 2, 0, 0, 0, 1, "native.raw"},
		{"position_of_minimum_temperature_sensor", mWord, 6, 2, 0, 0, 0, 1, "native.raw"},
	},
	0x04040000: {
		{"usoc", mWord, 0, 2, 0, 0, 0, 10, "native.raw"},
		{"soh", mWord, 2, 2, 0, 0, 0, 10, "native.raw"},
		{"asoc", mWord, 4, 2, 0, 0, 0, 10, "native.raw"},
		{"cycle_count", mWord, 6, 2, 0, 0, 0, 1, "native.raw"},
	},
	0x04050000: {
		{"permitted_charge_current", mWord, 0, 2, 0, 0, 0, 10, "native.ampere"},
		{"permitted_discharge_current", mWord, 2, 2, 0, 0, 0, 10, "native.ampere"},
		{"full_charge_capacity_estimate", mWord, 4, 2, 0, 0, 0, 10, "native.ampere_hour"},
		{"remaining_capacity_estimate", mWord, 6, 2, 0, 0, 0, 10, "native.ampere_hour"},
	},
	0x04060000: {
		{"cumulative_incoming_charge", mRaw, 0, 4, 0, 0, 0, 1, "native.raw"},
		{"cumulative_outgoing_charge", mRaw, 4, 4, 0, 0, 0, 1, "native.raw"},
	},
	0x04070000: {
		{"cumulative_incoming_energy", mRaw, 0, 4, 0, 0, 0, 1, "native.raw"},
		{"cumulative_outgoing_energy", mRaw, 4, 4, 0, 0, 0, 1, "native.raw"},
	},
	0x040a0000: {
		{"protect_alarm_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"internal_sum_voltage_high", mBits, 0, 1, 0, 2, 0, 1, "native.state"},
		{"internal_sum_voltage_low", mBits, 0, 1, 2, 2, 0, 1, "native.state"},
		{"cell_voltage_high", mBits, 0, 1, 4, 2, 0, 1, "native.state"},
		{"cell_voltage_low", mBits, 0, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"delta_voltage_single", mBits, 1, 1, 0, 2, 0, 1, "native.state"},
		{"delta_voltage_all", mBits, 1, 1, 2, 2, 0, 1, "native.state"},
		{"delta_temperature_single", mBits, 1, 1, 4, 2, 0, 1, "native.state"},
		{"delta_temperature_all", mBits, 1, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_2", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"charge_temperature_high", mBits, 2, 1, 0, 2, 0, 1, "native.state"},
		{"charge_temperature_low", mBits, 2, 1, 2, 2, 0, 1, "native.state"},
		{"discharge_temperature_high", mBits, 2, 1, 4, 2, 0, 1, "native.state"},
		{"discharge_temperature_low", mBits, 2, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_3", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"soc_high", mBits, 3, 1, 0, 2, 0, 1, "native.state"},
		{"soc_low", mBits, 3, 1, 2, 2, 0, 1, "native.state"},
		{"soh_low", mBits, 3, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_4", mSignedOctet, 4, 1, 0, 0, 0, 1, "native.raw"},
		{"discharge_power_high", mBits, 4, 1, 0, 2, 0, 1, "native.state"},
		{"charge_power_high", mBits, 4, 1, 2, 2, 0, 1, "native.state"},
		{"delta_current_high", mBits, 4, 1, 4, 2, 0, 1, "native.state"},
		{"delta_battery_voltage_high", mBits, 4, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_5", mSignedOctet, 5, 1, 0, 0, 0, 1, "native.raw"},
		{"charge_current_high", mBits, 5, 1, 0, 2, 0, 1, "native.state"},
		{"discharge_current_high", mBits, 5, 1, 2, 2, 0, 1, "native.state"},
		{"protect_alarm_6", mSignedOctet, 6, 1, 0, 0, 0, 1, "native.raw"},
		{"spcl_sum_voltage_low", mBits, 6, 1, 2, 2, 0, 1, "native.state"},
		{"spcl_cell_voltage_low", mBits, 6, 1, 6, 2, 0, 1, "native.state"},
		{"protect_alarm_7", mSignedOctet, 7, 1, 0, 0, 0, 1, "native.raw"},
	},
	0x040b0000: {
		{"system_fault_code_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"current_open_positive", mBits, 0, 1, 0, 2, 0, 1, "native.state"},
		{"current_open_negative", mBits, 0, 1, 2, 2, 0, 1, "native.state"},
		{"positive_not_open", mBits, 0, 1, 4, 2, 0, 1, "native.state"},
		{"positive_not_close", mBits, 0, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"negative_not_open", mBits, 1, 1, 0, 2, 0, 1, "native.state"},
		{"negative_not_close", mBits, 1, 1, 2, 2, 0, 1, "native.state"},
		{"precharge_not_open", mBits, 1, 1, 4, 2, 0, 1, "native.state"},
		{"precharge_not_close", mBits, 1, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_2", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"precharge_charge", mBits, 2, 1, 0, 2, 0, 1, "native.state"},
		{"parallel_id_fail", mBits, 2, 1, 2, 2, 0, 1, "native.state"},
		{"parallel_power_fail", mBits, 2, 1, 4, 2, 0, 1, "native.state"},
		{"short_circuit", mBits, 2, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_3", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"delta_voltage_invalid", mBits, 3, 1, 0, 2, 0, 1, "native.state"},
		{"cell_voltage_low_invalid", mBits, 3, 1, 2, 2, 0, 1, "native.state"},
		{"supply_voltage_low", mBits, 3, 1, 4, 2, 0, 1, "native.state"},
		{"supply_voltage_high", mBits, 3, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_4", mSignedOctet, 4, 1, 0, 0, 0, 1, "native.raw"},
		{"precharge_temperature_high", mBits, 4, 1, 0, 2, 0, 1, "native.state"},
		{"mosfet_temperature_high", mBits, 4, 1, 2, 2, 0, 1, "native.state"},
		{"balance_temperature_high", mBits, 4, 1, 4, 2, 0, 1, "native.state"},
		{"charger_reverse", mBits, 4, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_5", mSignedOctet, 5, 1, 0, 0, 0, 1, "native.raw"},
		{"afe_temperature_low", mBits, 5, 1, 0, 2, 0, 1, "native.state"},
		{"afe_temperature_high", mBits, 5, 1, 2, 2, 0, 1, "native.state"},
		{"afe_voltage_low", mBits, 5, 1, 4, 2, 0, 1, "native.state"},
		{"afe_voltage_high", mBits, 5, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_6", mSignedOctet, 6, 1, 0, 0, 0, 1, "native.raw"},
		{"afe_pin_fault", mBits, 6, 1, 0, 2, 0, 1, "native.state"},
		{"shut_down_fail", mBits, 6, 1, 2, 2, 0, 1, "native.state"},
		{"cell_temperature_high_invalid", mBits, 6, 1, 4, 2, 0, 1, "native.state"},
		{"cell_voltage_high_invalid", mBits, 6, 1, 6, 2, 0, 1, "native.state"},
		{"system_fault_code_7", mSignedOctet, 7, 1, 0, 0, 0, 1, "native.raw"},
		{"charge_in_ov", mBits, 7, 1, 0, 2, 0, 1, "native.state"},
		{"software_version", mBits, 7, 1, 2, 2, 0, 1, "native.state"},
		{"hvil_fault", mBits, 7, 1, 4, 2, 0, 1, "native.state"},
		{"sbmsuse_fault", mBits, 7, 1, 6, 2, 0, 1, "native.state"},
	},
	0x040c0000: {
		{"other_error_code_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"negative_not_close", mBits, 0, 1, 0, 1, 0, 1, "native.state"},
		{"negative_offline", mBits, 0, 1, 1, 1, 0, 1, "native.state"},
		{"precharge_short_vdd", mBits, 0, 1, 2, 1, 0, 1, "native.state"},
		{"precharge_short_gnd", mBits, 0, 1, 3, 1, 0, 1, "native.state"},
		{"precharge_over_current", mBits, 0, 1, 4, 1, 0, 1, "native.state"},
		{"precharge_not_open", mBits, 0, 1, 5, 1, 0, 1, "native.state"},
		{"precharge_not_close", mBits, 0, 1, 6, 1, 0, 1, "native.state"},
		{"precharge_offline", mBits, 0, 1, 7, 1, 0, 1, "native.state"},
		{"other_error_code_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"positive_over_current", mBits, 1, 1, 0, 1, 0, 1, "native.state"},
		{"positive_not_open", mBits, 1, 1, 1, 1, 0, 1, "native.state"},
		{"positive_not_close", mBits, 1, 1, 2, 1, 0, 1, "native.state"},
		{"positive_offline", mBits, 1, 1, 3, 1, 0, 1, "native.state"},
		{"negative_short_vdd", mBits, 1, 1, 4, 1, 0, 1, "native.state"},
		{"negative_short_gnd", mBits, 1, 1, 5, 1, 0, 1, "native.state"},
		{"negative_over_current", mBits, 1, 1, 6, 1, 0, 1, "native.state"},
		{"negative_not_open", mBits, 1, 1, 7, 1, 0, 1, "native.state"},
		{"other_error_code_2", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"actor_not_close", mBits, 2, 1, 4, 1, 0, 1, "native.state"},
		{"actor_not_open", mBits, 2, 1, 5, 1, 0, 1, "native.state"},
		{"positive_short_vdd", mBits, 2, 1, 6, 1, 0, 1, "native.state"},
		{"positive_short_gnd", mBits, 2, 1, 7, 1, 0, 1, "native.state"},
		{"other_error_code_3", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"error_intc", mBits, 3, 1, 0, 1, 0, 1, "native.state"},
		{"error_io", mBits, 3, 1, 1, 1, 0, 1, "native.state"},
		{"error_ad", mBits, 3, 1, 2, 1, 0, 1, "native.state"},
		{"error_flash", mBits, 3, 1, 3, 1, 0, 1, "native.state"},
		{"error_ram", mBits, 3, 1, 4, 1, 0, 1, "native.state"},
		{"error_stack", mBits, 3, 1, 5, 1, 0, 1, "native.state"},
		{"error_clock", mBits, 3, 1, 6, 1, 0, 1, "native.state"},
		{"error_core", mBits, 3, 1, 7, 1, 0, 1, "native.state"},
		{"other_error_code_4", mSignedOctet, 4, 1, 0, 0, 0, 1, "native.raw"},
		{"sbms_terminal", mBits, 4, 1, 6, 1, 0, 1, "native.state"},
		{"mbms_terminal", mBits, 4, 1, 7, 1, 0, 1, "native.state"},
		{"other_error_code_5", mSignedOctet, 5, 1, 0, 0, 0, 1, "native.raw"},
		{"other_error_code_6", mSignedOctet, 6, 1, 0, 0, 0, 1, "native.raw"},
		{"other_error_code_7", mSignedOctet, 7, 1, 0, 0, 0, 1, "native.raw"},
	},
	0x040d0000: {
		{"hardware_error_code_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"detect_iso_voltage", mBits, 0, 1, 0, 2, 0, 1, "native.state"},
		{"detect_current", mBits, 0, 1, 2, 2, 0, 1, "native.state"},
		{"detect_temperature", mBits, 0, 1, 4, 2, 0, 1, "native.state"},
		{"detect_cell_voltage", mBits, 0, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"detect_fuse", mBits, 1, 1, 0, 2, 0, 1, "native.state"},
		{"detect_balance", mBits, 1, 1, 2, 2, 0, 1, "native.state"},
		{"detect_external_voltage", mBits, 1, 1, 4, 2, 0, 1, "native.state"},
		{"detect_internal_voltage", mBits, 1, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_2", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"eeprom_communication", mBits, 2, 1, 4, 2, 0, 1, "native.state"},
		{"external_flash_communication", mBits, 2, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_3", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"bmu_address_fault", mBits, 3, 1, 0, 2, 0, 1, "native.state"},
		{"bmu_power_fault", mBits, 3, 1, 2, 2, 0, 1, "native.state"},
		{"ivu_communication", mBits, 3, 1, 4, 2, 0, 1, "native.state"},
		{"bmu_communication", mBits, 3, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_4", mSignedOctet, 4, 1, 0, 0, 0, 1, "native.raw"},
		{"rs485_communication", mBits, 4, 1, 2, 2, 0, 1, "native.state"},
		{"external_can_communication", mBits, 4, 1, 4, 2, 0, 1, "native.state"},
		{"internal_can_communication", mBits, 4, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_5", mSignedOctet, 5, 1, 0, 0, 0, 1, "native.raw"},
		{"detect_bmu_sum_voltage", mBits, 5, 1, 2, 2, 0, 1, "native.state"},
		{"hardware_error_code_6", mSignedOctet, 6, 1, 0, 0, 0, 1, "native.raw"},
		{"mosfet_actor", mBits, 6, 1, 0, 2, 0, 1, "native.state"},
		{"rtc_fault", mBits, 6, 1, 4, 2, 0, 1, "native.state"},
		{"afe_communication", mBits, 6, 1, 6, 2, 0, 1, "native.state"},
		{"hardware_error_code_7", mSignedOctet, 7, 1, 0, 0, 0, 1, "native.raw"},
		{"detect_load_minus_voltage", mBits, 7, 1, 0, 2, 0, 1, "native.state"},
		{"detect_relay_voltage", mBits, 7, 1, 2, 2, 0, 1, "native.state"},
		{"detect_breaker_voltage", mBits, 7, 1, 4, 2, 0, 1, "native.state"},
		{"detect_leak_voltage", mBits, 7, 1, 6, 2, 0, 1, "native.state"},
	},
	0x04100000: {
		{"hardware_state_code_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"m_ivu_alarm", mBits, 0, 1, 0, 1, 0, 1, "native.state"},
		{"m_short", mBits, 0, 1, 1, 1, 0, 1, "native.state"},
		{"usb_int", mBits, 0, 1, 2, 1, 0, 1, "native.state"},
		{"m_fault", mBits, 0, 1, 3, 1, 0, 1, "native.state"},
		{"m_ivu_power_fault", mBits, 0, 1, 4, 1, 0, 1, "native.state"},
		{"m_s_power_fault", mBits, 0, 1, 5, 1, 0, 1, "native.state"},
		{"hardware_state_code_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"hardware_state_code_2", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"hardware_state_code_3", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"qmax", mWord, 4, 2, 0, 0, 0, 10, "native.ampere_hour"},
		{"rcc", mWord, 6, 2, 0, 0, 0, 10, "native.milliohm"},
	},
	0x04110000: {
		{"actuator_state_code_0", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"actuator_state_code_1", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"run_mode", mRunMode, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"work_mode", mWorkMode, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"maximum_sum_charge_current", mWord, 4, 2, 0, 0, 0, 10, "native.raw"},
		{"maximum_sum_discharge_current", mSignedWord, 6, 2, 0, 0, 0, 10, "native.raw"},
	},
	0x04120000: {
		{"main_software_version", mSignedOctet, 0, 1, 0, 0, 0, 1, "native.raw"},
		{"sub_software_version", mSignedOctet, 1, 1, 0, 0, 0, 1, "native.raw"},
		{"main_hardware_version", mSignedOctet, 2, 1, 0, 0, 0, 1, "native.raw"},
		{"sub_hardware_version", mSignedOctet, 3, 1, 0, 0, 0, 1, "native.raw"},
		{"bms_numeric_serial_number", mRaw, 4, 4, 0, 0, 0, 1, "native.raw"},
	},
	0x04130000: {
		{"charge_current_alarm_threshold", mWord, 0, 2, 0, 0, 0, 10, "native.ampere"},
		{"discharge_current_alarm_threshold", mWord, 2, 2, 0, 0, 0, 10, "native.ampere"},
		{"asoc_ocv", mWord, 4, 2, 0, 0, 0, 10, "native.percent"},
		{"asoc_ah_0", mWord, 6, 2, 0, 0, 0, 10, "native.percent"},
	},
	0x04140000: {
		{"asoc_ah_1", mWord, 0, 2, 0, 0, 0, 10, "native.percent"},
		{"asoc_full_0", mWord, 2, 2, 0, 0, 0, 10, "native.percent"},
		{"asoc_full_1", mWord, 4, 2, 0, 0, 0, 10, "native.percent"},
		{"asoc_empty_0", mWord, 6, 2, 0, 0, 0, 10, "native.percent"},
	},
	0x04150000: {
		{"asoc_empty_1", mWord, 0, 2, 0, 0, 0, 10, "native.percent"},
		{"passed_charge_0", mWord, 2, 2, 0, 0, 0, 10, "native.percent"},
		{"passed_charge_1", mWord, 4, 2, 0, 0, 0, 10, "native.percent"},
		{"soc_flag", mWord, 6, 2, 0, 0, 0, 1, "native.raw"},
	},
	0x04160000: {
		{"cell_number", mWord, 0, 2, 0, 0, 0, 1, "native.raw"},
		{"cell_capacity", mWord, 2, 2, 0, 0, 0, 1, "native.raw"},
		{"breaker_voltage", mWord, 4, 2, 0, 0, 0, 10, "native.volt"},
		{"sum_voltage", mWord, 6, 2, 0, 0, 0, 10, "native.volt"},
	},
	0x04170000: {
		{"insulation_resistance", mWord, 0, 2, 0, 0, 0, 100, "native.megaohm"},
	},
	0x08000000: {
		{"cell_voltage_1", mWord, 0, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_2", mWord, 2, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_3", mWord, 4, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_4", mWord, 6, 2, 0, 0, 0, 1, "native.millivolt"},
	},
	0x08010000: {
		{"cell_voltage_5", mWord, 0, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_6", mWord, 2, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_7", mWord, 4, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_8", mWord, 6, 2, 0, 0, 0, 1, "native.millivolt"},
	},
	0x08020000: {
		{"cell_voltage_9", mWord, 0, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_10", mWord, 2, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_11", mWord, 4, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_12", mWord, 6, 2, 0, 0, 0, 1, "native.millivolt"},
	},
	0x08030000: {
		{"cell_voltage_13", mWord, 0, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_14", mWord, 2, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_15", mWord, 4, 2, 0, 0, 0, 1, "native.millivolt"},
		{"cell_voltage_16", mWord, 6, 2, 0, 0, 0, 1, "native.millivolt"},
	},
	0x10000000: {
		{"cell_temperature_1", mWord, 0, 2, 0, 0, -400, 10, "native.celsius"},
		{"cell_temperature_2", mWord, 2, 2, 0, 0, -400, 10, "native.celsius"},
		{"cell_temperature_3", mWord, 4, 2, 0, 0, -400, 10, "native.celsius"},
		{"balance_temperature_1", mWord, 6, 2, 0, 0, -400, 10, "native.celsius"},
	},
	0x10010000: {
		{"balance_temperature_2", mWord, 0, 2, 0, 0, -400, 10, "native.celsius"},
	},
	0x14000000: {
		{"balance_flag", mWord, 0, 2, 0, 0, 0, 1, "native.raw"},
		{"cell_balance_1", mBits, 0, 1, 7, 1, 0, 1, "native.state"},
		{"cell_balance_2", mBits, 0, 1, 6, 1, 0, 1, "native.state"},
		{"cell_balance_3", mBits, 0, 1, 5, 1, 0, 1, "native.state"},
		{"cell_balance_4", mBits, 0, 1, 4, 1, 0, 1, "native.state"},
		{"cell_balance_5", mBits, 0, 1, 3, 1, 0, 1, "native.state"},
		{"cell_balance_6", mBits, 0, 1, 2, 1, 0, 1, "native.state"},
		{"cell_balance_7", mBits, 0, 1, 1, 1, 0, 1, "native.state"},
		{"cell_balance_8", mBits, 0, 1, 0, 1, 0, 1, "native.state"},
		{"cell_balance_9", mBits, 1, 1, 7, 1, 0, 1, "native.state"},
		{"cell_balance_10", mBits, 1, 1, 6, 1, 0, 1, "native.state"},
		{"cell_balance_11", mBits, 1, 1, 5, 1, 0, 1, "native.state"},
		{"cell_balance_12", mBits, 1, 1, 4, 1, 0, 1, "native.state"},
		{"cell_balance_13", mBits, 1, 1, 3, 1, 0, 1, "native.state"},
		{"cell_balance_14", mBits, 1, 1, 2, 1, 0, 1, "native.state"},
		{"cell_balance_15", mBits, 1, 1, 1, 1, 0, 1, "native.state"},
		{"cell_balance_16", mBits, 1, 1, 0, 1, 0, 1, "native.state"},
	},
}
