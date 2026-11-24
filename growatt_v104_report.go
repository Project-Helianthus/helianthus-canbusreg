package canbusreg

import (
	"encoding/binary"
	"fmt"
)

// GrowattReportField is an exact native numeric value or an unresolved byte span.
// A zero denominator means no numeric interpretation is available.
type GrowattReportField struct {
	Numerator, Denominator  int64
	Unit, Label, Unresolved string
	Raw                     []byte
}

// GrowattBMSReport retains a single selected report and its native evidence.
// Only fields carried by that frame are included.
type GrowattBMSReport struct {
	Evidence Evidence
	Fields   map[string]GrowattReportField
}

type growattReportFields map[string]GrowattReportField

func (f growattReportFields) number(name string, n, d int64, unit string) {
	f[name] = GrowattReportField{Numerator: n, Denominator: d, Unit: unit}
}
func (f growattReportFields) raw(name string, p []byte, reason string) {
	f[name] = GrowattReportField{Raw: append([]byte(nil), p...), Unresolved: reason}
}
func (f growattReportFields) bits(p byte, names ...string) {
	for bit, name := range names {
		if name != "" {
			f.number(name, int64(p>>bit&1), 1, "")
		}
	}
}
func (f growattReportFields) enum(name string, code byte, labels ...string) {
	f.number(name, int64(code), 1, "")
	v := f[name]
	if int(code) < len(labels) {
		v.Label = labels[code]
	}
	f[name] = v
}

func decodeGrowattBMSReport(e Evidence, revision string) (GrowattBMSReport, bool) {
	if !growattV104Geometry(e.Frame) {
		return GrowattBMSReport{}, false
	}
	p := e.Frame.Bytes()
	id := e.Frame.ID().Value()
	if revision == BMSRevisionV104 {
		if !growattV104ListedFrame(e.Frame) || !growattV104Valid(e.Frame) {
			return GrowattBMSReport{}, false
		}
	} else if revision != BMSRevisionV108 || !growattV108Valid(id, p) {
		return GrowattBMSReport{}, false
	}
	f := make(growattReportFields)
	u := func(at int) int64 { return int64(binary.BigEndian.Uint16(p[at : at+2])) }
	switch id {
	case 0x311:
		f.number("charge_voltage_limit", u(0), 10, "V")
		f.number("charge_current_limit", u(2), 10, "A")
		f.number("discharge_current_limit", u(4), 10, "A")
		f.enum("operating_state", p[7]&3, "soft_starting", "standby", "charging", "discharging")
		f.bits(p[7], "", "", "error_byte_valid", "cell_balance", "sleep", "discharge_output", "charge_output", "battery_terminal_open")
		f.enum("master_operation_mode", p[6]&3, "standalone", "parallel", "parallel_preparation")
		if revision == BMSRevisionV104 {
			f.enum("storage_system_state", p[6]>>2&3, "none", "standby", "charging", "discharging")
		}
	case 0x312:
		growattBMSProtection(f, p, revision)
		f.number("pack_count", int64(p[4]), 1, "count")
		if revision == BMSRevisionV104 {
			f.raw("manufacturer", p[5:7], "")
			f.number("total_cell_count", int64(p[7]), 1, "count")
		} else {
			growattV108Derating(f, p)
		}
	case 0x313:
		f.number("voltage", int64(int16(u(0))), 100, "V")
		f.number("current", int64(int16(u(2))), 10, "A")
		f.number("soc", int64(p[6]), 1, "%")
		f.number("soh", int64(p[7]&0x7f), 1, "%")
		if revision == BMSRevisionV104 {
			f.number("maximum_cell_temperature", int64(int16(u(4))), 10, "°C")
			f.number("soh_valid", int64(p[7]>>7), 1, "")
		} else {
			f.number("average_battery_temperature", int64(int16(u(4))), 10, "°C")
			f.number("life_warning", int64(p[7]>>7), 1, "")
		}
	case 0x314:
		f.number("remaining_capacity", 10*u(0), 1, "mAh")
		f.number("full_charge_capacity", 10*u(2), 1, "mAh")
		f.number("cell_voltage_spread", u(4), 1, "mV")
		f.number("cycle_count", u(6), 1, "count")
	case 0x315, 0x316, 0x317, 0x318:
		for i := 0; i < 4; i++ {
			f.number(fmt.Sprintf("cell_voltage_%d", 4*(id-0x315)+uint32(i)+1), u(2*i), 1, "mV")
		}
	case 0x319:
		if revision == BMSRevisionV108 {
			if p[0]&3 != 3 {
				f.enum("battery_type", p[0]&3, "lithium_iron_phosphate", "ternary", "lithium_titanate")
			}
		} else {
			f.number("battery_chemistry_code", int64(p[0]&3), 1, "")
		}
		f.bits(p[0], "", "", "", "", "", "force_charge_request", "discharge_enable", "charge_enable")
		f.number("maximum_cell_voltage", u(1), 1, "mV")
		f.number("minimum_cell_voltage", u(3), 1, "mV")
		f.number("maximum_cell_index", int64(p[5]), 1, "")
		f.number("minimum_cell_index", int64(p[6]), 1, "")
		f.number("protected_pack_id", int64(p[7]), 1, "")
	case 0x320:
		f.raw("manufacturer", p[:2], "")
		f.number("hardware_version", int64(p[2]), 1, "")
		if revision == BMSRevisionV104 {
			f.number("software_version", int64(p[3]), 1, "")
			f.raw("build_date_time", p[4:], "packed field positions and byte order unspecified")
		} else {
			f.number("software_version", int64(binary.LittleEndian.Uint16(p[3:5])), 1, "")
			f.number("highest_parallel_software_version", int64(binary.LittleEndian.Uint16(p[5:7])), 1, "")
		}
	case 0x321:
		if revision == BMSRevisionV104 {
			f.number("update_state", int64(p[0]), 1, "")
			f.number("pack_progress", int64(p[1]), 1, "")
			f.number("programming_pack_id", int64(p[2]), 1, "")
			f.number("successful_update_count", int64(p[3]), 1, "count")
		} else {
			f.raw("update_status", p[:1], "status groups overlap at bit 1")
			f.number("pack_progress", int64(p[1]), 1, "%")
			f.number("pack_programming_status", int64(p[2]), 1, "")
		}
	default:
		growattV108AdditionalReport(f, id, p)
	}
	return GrowattBMSReport{Evidence: e, Fields: f}, true
}

func growattBMSProtection(f growattReportFields, p [8]byte, revision string) {
	f.bits(p[0], "protection_soft_start_failure", "protection_module_undervoltage", "protection_module_overvoltage", "protection_cell_undervoltage", "protection_cell_overvoltage", "protection_short_circuit_discharge", "protection_charge_overcurrent", "protection_discharge_overcurrent")
	disagreement, failure := "", ""
	if revision == BMSRevisionV108 {
		disagreement, failure = "protection_parallel_version_disagreement", "protection_parallel_failure"
	}
	f.bits(p[1], disagreement, failure, "protection_delta_voltage_failure", "protection_system_error", "protection_charge_undertemperature", "protection_discharge_undertemperature", "protection_charge_overtemperature", "protection_discharge_overtemperature")
	f.bits(p[2], "", "warning_module_undervoltage", "warning_module_overvoltage", "warning_cell_undervoltage", "warning_cell_overvoltage", "", "warning_charge_overcurrent", "warning_discharge_overcurrent")
	f.bits(p[3], "warning_internal_communication_failure", "warning_before_shutdown", "warning_delta_voltage_failure", "", "warning_charge_undertemperature", "warning_discharge_undertemperature", "warning_charge_overtemperature", "warning_discharge_overtemperature")
}
