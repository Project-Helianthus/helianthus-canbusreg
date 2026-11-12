package canbusreg

import "fmt"

// LabelCondition returns a candidate condition label using the selected native dictionary.
// Native condition-name contracts, Defined condition names:
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// A label annotates a received selected index, never activation, severity,
// diagnosis, operating limits or physical qualification. No predicates are
// evaluated from unreceived state or configurable thresholds.
func LabelCondition(member Selection, index NativeField) NativeField {
	r := index
	r.Meaning, r.Grade, r.Status = "", "Unknown", "unsupported"
	r.PhysicalQualified = false
	count, modern := dictionary(member)
	if count == 0 || index.Numerator < 0 || index.Numerator >= int64(count) {
		return r
	}
	if index.Evidence.Sequence == 0 || index.Evidence.Monotonic < 0 {
		r.Status = "invalid"
		return r
	}
	c := member.SelectionSpecValue()
	i := LINKInput{Evidence: index.Evidence, Epoch: index.Epoch, Direction: c.Direction, Scope: index.Scope, Bank: index.Bank, Endpoint: index.Endpoint, Group: index.Group, Slot: index.Slot, FieldAge: index.Age}
	// Re-admit the exact native index field, including raw bytes and metadata.
	// Caller-changed indices, retained bits and stale fields cannot acquire names.
	offsets := []int{0, 2}
	if c.Flavor == "pcs-standard" {
		offsets = []int{2, 4}
	} else if c.Flavor == "pcs-parallel" {
		offsets = []int{1, 3}
	}
	matched := false
	for _, offset := range offsets {
		decoded := DecodeDTC(member, i, offset)
		if f, ok := decoded.Fields["numeric_index"]; ok && f == index {
			matched = true
		}
	}
	if !matched {
		r.Status = "invalid"
		return r
	}
	n := int(index.Numerator)
	switch n {
	case 31, 35, 91, 93, 102, 103, 128, 131, 133, 134, 135:
		if !modern {
			return r
		}
	}
	name := ""
	switch n {
	case 0, 1:
		name = fmt.Sprintf("Cell overvoltage — condition %d", n)
	case 2, 3:
		name = fmt.Sprintf("Cell undervoltage — condition %d", n)
	case 4, 5:
		name = fmt.Sprintf("Cell-voltage spread high — condition %d", n)
	case 6, 7:
		name = fmt.Sprintf("Pack overvoltage — condition %d", n)
	case 8, 9:
		name = fmt.Sprintf("Pack undervoltage — condition %d", n)
	case 11:
		name = "Reported SOC low"
	case 12, 13, 33, 34:
		name = fmt.Sprintf("Cell temperature high — condition %d", n)
	case 14, 15, 40, 41:
		name = fmt.Sprintf("Cell temperature low — condition %d", n)
	case 16, 17:
		name = fmt.Sprintf("Cell-temperature spread high — condition %d", n)
	case 20, 21:
		name = fmt.Sprintf("Discharge current high — condition %d", n)
	case 22, 23:
		name = fmt.Sprintf("Charge current high — condition %d", n)
	case 30:
		name = "Cell-voltage plausibility"
	case 31:
		name = "Current zero-offset calibration out of range"
	case 35:
		name = "Measurement acquisition communication failure"
	case 38:
		name = "Nonvolatile storage access failure"
	case 39:
		name = "Charging current while a low-temperature condition is active"
	case 42:
		name = "Pack-voltage consistency difference"
	case 44:
		name = "Configuration record failure"
	case 45:
		name = "Hardware status condition"
	case 52:
		name = "Output-port overvoltage"
	case 57:
		name = "Charging current high — additional condition"
	case 58:
		name = "Discharging current high — additional condition"
	case 60:
		name = "Pack voltage exceeds port voltage"
	case 61:
		name = "Port voltage exceeds pack voltage"
	case 62:
		name = "Maximum cell voltage high"
	case 91:
		name = "Parallel discovery/association timeout"
	case 93:
		name = "Parallel peer-word spread in 61X.B4–B5"
	case 102:
		name = "LINK member serial conflict"
	case 103:
		name = "Parallel member serial conflict"
	case 128:
		name = "Startup integrity check A failed"
	case 129, 130:
		name = fmt.Sprintf("Algorithm status condition — condition %d", n)
	case 131:
		name = "Adjacent member state mismatch"
	case 133:
		name = "Startup integrity check B failed"
	case 134:
		name = "Measurement status flag set"
	case 135:
		name = "Pack/port voltage difference with hardware status"
	}
	if name != "" {
		r.Meaning, r.Grade, r.Status = name, "Hypothesis", "accepted"
	}
	return r
}
