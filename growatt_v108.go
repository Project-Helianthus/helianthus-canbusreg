package canbusreg

import "fmt"

// BMSRevisionV108 explicitly selects the V1.08 layout, never a detected revision.
const BMSRevisionV108 = "V1.08"

func growattV108Valid(id uint32, p [8]byte) bool {
	switch id {
	case 0x311, 0x314, 0x315, 0x316, 0x317, 0x318, 0x319, 0x324, 0x325:
		return true
	case 0x312:
		return p[4] >= 1 && p[4] <= 254
	case 0x313:
		return p[6] <= 100
	case 0x320:
		return p[2] >= 1 && p[2] <= 9
	case 0x321:
		return p[1] <= 100
	case 0x322:
		return p[6] <= 100 && p[7] <= 100
	case 0x323:
		return p[0] >= 1 && p[0] <= 254
	}
	return false
}

func growattV108Derating(f growattReportFields, p [8]byte) {
	f.bits(p[5], "derating_hardware_fault", "derating_full_charge", "derating_mos_temperature", "derating_ambient_temperature", "derating_precharge_fault", "derating_communication_fault", "derating_bus_fault")
	f.bits(p[6], "derating_high_cell_voltage", "derating_low_cell_voltage", "derating_high_cell_temperature", "derating_low_cell_temperature", "derating_high_total_voltage", "derating_low_total_voltage", "derating_cell_voltage_difference", "derating_temperature_difference")
}

func growattV108AdditionalReport(f growattReportFields, id uint32, p [8]byte) {
	switch id {
	case 0x322:
		f.number("highest_cell_temperature", int64(int16(uint16(p[0])<<8|uint16(p[1]))), 10, "°C")
		f.number("lowest_cell_temperature", int64(int16(uint16(p[2])<<8|uint16(p[3]))), 10, "°C")
		for i, name := range []string{"highest_temperature_index", "lowest_temperature_index", "highest_soc", "lowest_soc"} {
			unit := "index"
			if i >= 2 {
				unit = "%"
			}
			f.number(name, int64(p[i+4]), 1, unit)
		}
	case 0x323:
		f.number("total_cell_count", int64(p[0]), 1, "count")
		f.bits(p[4], "protection_charge_overpower", "protection_discharge_overpower", "protection_external_communication_fault", "protection_precharge_failure", "protection_hardware_fault", "protection_analog_front_end_communication_fault", "protection_cell_lost", "protection_total_current_sample_fault")
		f.bits(p[5], "protection_total_voltage_sample_fault", "protection_load_side_voltage_sample_fault", "protection_calibration_parameter_load_fault", "protection_bus_reverse_connection", "protection_hardware_overvoltage", "protection_hardware_overcurrent", "protection_parallel_fault", "protection_primary_secondary_voltage_difference")
		f.bits(p[6], "protection_hardware_discharge_overcurrent", "protection_charge_limit_failure", "protection_discharge_limit_failure", "protection_main_path_open")
		f.bits(p[7], "warning_charge_overpower", "warning_discharge_overpower", "warning_internal_charge_circulation", "warning_internal_discharge_circulation")
	case 0x324:
		f.number("frame_number", int64(p[0]), 1, "")
		start := 1
		if p[0] == 0 {
			f.number("battery_id", int64(p[1]), 1, "")
			start = 2
		}
		f.raw("serial_fragment", p[start:], "")
	case 0x325:
		f.number("frame_number", int64(p[0]), 1, "")
		f.number("battery_id", int64(p[1]), 1, "")
		f.raw("history_bytes", p[2:], "history layout unresolved")
	}
}

func growattBMSRevisionProfile(revision string) (string, error) {
	switch revision {
	case BMSRevisionV104:
		return growattLowVoltageBMSV104Profile, nil
	case BMSRevisionV108:
		return "growatt.bms.low_voltage.can.v1_08", nil
	default:
		return "", fmt.Errorf("growatt revision unresolved: explicitly select %s or %s", BMSRevisionV104, BMSRevisionV108)
	}
}
