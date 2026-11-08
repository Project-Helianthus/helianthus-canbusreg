package canbusreg

import (
	"encoding/binary"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 311…314: limits, summary and capacity; Measurement conversions;
// 319, 322, 330: extrema and location. All results remain candidates.
// CX05PCSInput supplies explicit source and validity assertions, never inferred
// from nominal bitrate, plausible bytes or matching standard identifiers.
// NativeRecord, when supplied, is checked against Evidence.Frame; malformed and
// FD records are copied into Diagnostics without producing fields.
// FieldStates may withhold individual fields as absent, invalid, unsupported,
// stale, conflict or unassigned. FieldAges overrides only that field's age.
// Indices are interpreted only for a supported native contract; the temperature
// extrema of 322 use distinct field names so retention cannot mix quantities.
// IndexMinimum/Maximum are inclusive caller-selected native bounds, not cell
// geometry inferred from extrema. Count is used only for the fine-average gate.
type CX05PCSInput struct {
	CX01LINKInput
	NativeRecord                       []byte
	PublisherResolved                  bool
	MeasurementValid, TemperatureValid bool
	CountValid                         bool
	Count                              uint16
	IndexValid                         bool
	IndexScope                         string
	IndexMinimum, IndexMaximum         uint16
	FieldStates                        map[string]string
	FieldAges                          map[string]time.Duration
}

type CX05PCSResult struct {
	CXNativeResult
	Diagnostics []byte
}

// CX05DecodePCSStandard is a pure receive-only decoder. It retains native evidence
// and does not aggregate publishers, maintain a cache or qualify hardware.
func CX05DecodePCSStandard(s CXSelection, i CX05PCSInput) CX05PCSResult {
	r := CX05PCSResult{CXNativeResult: cx01Result(s, i.CX01LINKInput, "invalid"), Diagnostics: append([]byte(nil), i.NativeRecord...)}
	c := s.CX01Spec()
	f := i.Evidence.Frame
	if len(i.NativeRecord) != 0 {
		decoded, err := canbus.DecodeSocketCANRecord(i.NativeRecord)
		if err != nil || decoded != f {
			return r
		}
	}
	if i.Evidence.RawRecord != [canbus.SocketCANRecordSize]byte{} {
		decoded, err := canbus.DecodeSocketCANRecord(i.Evidence.RawRecord[:])
		if err != nil || decoded != f {
			return r
		}
	}
	if c.Interface.Name() == "" || c.Interface.Index() <= 0 || c.Epoch == 0 || c.Bank == "" || c.Endpoint == "" || (c.Scope != "local" && c.Scope != "aggregate") || c.Flavor != "pcs-standard" || c.Direction != "bms-to-pcs" || i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot || i.FieldAge < 0 || f.ID().Extended() || f.DLC() != 8 || f.RawDLC() != 8 {
		return r
	}
	if !i.PublisherResolved {
		r.Status = "conflict"
		return r
	}
	p := r.Raw
	local := c.ProjectCode == "QAAA" && c.Release == "0006" && c.Scope == "local"
	release11 := s.CX01Group() == "L8"
	gateway := c.ProjectCode == "QCAA" && c.Release == "0001" && c.Scope == "aggregate"
	fixedAggregate := c.ProjectCode == "QAAB" && c.Release == "0004" && c.Scope == "aggregate"
	supported := local || release11 || gateway || fixedAggregate
	measured := i.MeasurementValid && (local || release11)
	add := func(name string, n, d int64, unit, meaning, grade string, interpreted bool) {
		status := "raw"
		if interpreted {
			status = "accepted"
		} else {
			unit, meaning, grade = "", "", "Unknown"
		}
		age := i.FieldAge
		if a, ok := i.FieldAges[name]; ok {
			age = a
		}
		if state, ok := i.FieldStates[name]; ok {
			switch state {
			case "absent", "invalid", "unsupported", "stale", "conflict", "unassigned":
				status = state
			default:
				status = "invalid"
			}
		}
		if age < 0 {
			status = "invalid"
		}
		if status != "accepted" {
			unit, meaning = "", ""
		}
		r.Fields[name] = CXNativeField{Numerator: n, Denominator: d, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: age}
	}
	invalid := func(name string, gate bool) {
		if gate {
			f := r.Fields[name]
			if _, explicit := i.FieldStates[name]; !explicit && f.Status == "raw" {
				f.Status = "invalid"
				r.Fields[name] = f
			}
		}
	}
	raw := func(name string, n int64) { add(name, n, 1, "", "", "Unknown", false) }
	value := func(name string, n, d int64, unit, meaning string, gate bool) {
		add(name, n, d, unit, meaning, "Proven", gate)
	}
	be := func(at int) int64 { return int64(binary.BigEndian.Uint16(p[at : at+2])) }
	signed := func(at int) int64 { return int64(int16(binary.BigEndian.Uint16(p[at : at+2]))) }
	index := func(name string, at int) {
		n := uint16(p[at])
		value(name, int64(n), 1, "index", "selected native index", supported && i.IndexValid && i.IndexScope == c.Scope && i.IndexMinimum <= i.IndexMaximum && n >= i.IndexMinimum && n <= i.IndexMaximum)
	}
	switch f.ID().Value() {
	case 0x311:
		value("voltage_limit", be(0), 10, "V", "charge voltage limit", local && i.MeasurementValid)
		charge, discharge := 2, 4
		if gateway {
			charge, discharge = 4, 2
		}
		value("charge_limit", be(charge), 1, "A", "charge aggregation path", local && i.MeasurementValid)
		value("discharge_limit", be(discharge), 1, "A", "discharge aggregation path", local && i.MeasurementValid)
		raw("status", be(6))
		for _, hint := range []struct {
			name string
			mask uint16
		}{{"raw_state", 3}, {"fault_hint", 4}, {"sleep_hint", 0x10}, {"discharge_hint", 0x20}, {"charge_hint", 0x40}, {"force_hint", 0x400}} {
			n := be(6) & int64(hint.mask)
			if hint.mask != 3 && n != 0 {
				n = 1
			}
			add(hint.name, n, 1, "", "candidate "+hint.name+"; no action or interlock", "Hypothesis", local)
		}
	case 0x312:
		raw("status_a", int64(binary.LittleEndian.Uint16(p[0:2])))
		raw("status_b", int64(binary.LittleEndian.Uint16(p[2:4])))
		raw("reported_count", int64(p[4]))
		for at, name := range []string{"padding_5", "padding_6", "padding_7"} {
			raw(name, int64(p[at+5]))
		}
	case 0x313:
		meaning := "pack voltage"
		if release11 {
			meaning = "output-port voltage"
		}
		value("voltage", be(0), 100, "V", meaning, measured)
		value("current", signed(2), 10, "A", "positive charging", measured)
		tempMeaning := "coded minimum temperature"
		if release11 {
			tempMeaning = "count-aware fine average temperature"
		}
		add("temperature", signed(4), 10, "C", tempMeaning, "Hypothesis", measured && i.TemperatureValid && (!release11 || i.CountValid && i.Count > 0))
		value("soc", int64(p[6]), 1, "percent", "reported estimate; completion unproven", measured && p[6] <= 100)
		value("soh", int64(p[7]&0x7f), 1, "percent", "upward-rounded estimate", measured && p[7]&0x7f <= 100)
		invalid("soc", measured && p[6] > 100)
		invalid("soh", measured && p[7]&0x7f > 100)
		raw("soh_status", int64(p[7]))
		if release11 {
			add("soh_high_bit", int64(p[7]>>7), 1, "", "SOH below 60.1 percent OR independent diagnostic aggregate", "Hypothesis", measured)
		} else {
			value("soh_high_bit", int64(p[7]>>7), 1, "", "separate status flag", measured)
		}
	case 0x314:
		value("remaining_capacity", be(0), 100, "Ah", "estimated remaining capacity", measured)
		value("full_capacity", be(2), 100, "Ah", "estimated full capacity", measured)
		value("spread", be(4), 1, "mV", "cell voltage spread", measured)
		value("cycles", be(6), 1, "cycles", "whole cycles", measured)
	case 0x319:
		raw("state_0", int64(p[0]))
		raw("state_7", int64(p[7]))
		value("maximum_cell", be(1), 1, "mV", "maximum cell extremum", measured)
		value("minimum_cell", be(3), 1, "mV", "minimum cell extremum", measured)
		index("maximum_index", 5)
		index("minimum_index", 6)
	case 0x322:
		value("maximum_temperature", signed(0), 10, "C", "maximum temperature", measured && i.TemperatureValid)
		value("minimum_temperature", signed(2), 10, "C", "minimum temperature", measured && i.TemperatureValid)
		index("maximum_temperature_index", 4)
		index("minimum_temperature_index", 5)
		for at, name := range []string{"extra_6", "extra_7"} {
			add(name, int64(p[at+6]), 1, "percent", "downstream SOC extremum", "Hypothesis", gateway && i.MeasurementValid && p[at+6] <= 100)
			invalid(name, gateway && i.MeasurementValid && p[at+6] > 100)
		}
	case 0x330:
		for at, name := range []string{"maximum_location", "maximum_index", "minimum_location", "minimum_index"} {
			if at%2 == 1 {
				index(name, at)
			} else {
				raw(name, int64(p[at]))
				add(name+"_fixed_aggregate", int64(p[at]), 1, "", "fixed aggregate location", "Hypothesis", fixedAggregate && s.CX01Group() != "" && i.IndexValid && i.IndexScope == c.Scope && p[at] == 1)
			}
		}
		value("maximum_cell", be(4), 1, "mV", "maximum cell extremum", measured)
		value("minimum_cell", be(6), 1, "mV", "minimum cell extremum", measured)
	default:
		r.Status = "unsupported"
		return r
	}
	r.Status = "decoded"
	return r
}

// CX05RetainAccepted preserves independent last-known-good fields and their
// original ages. Rejected updates and identity changes cannot refresh them;
// a valid empty observation supersedes the retained value.
func CX05RetainAccepted(previous, update CXNativeResult) CXNativeResult {
	out := update
	out.Fields = make(map[string]CXNativeField)
	if previous.Selection == update.Selection {
		for name, f := range previous.Fields {
			if f.Status == "accepted" {
				out.Fields[name] = f
			}
		}
	}
	for name, f := range update.Fields {
		if f.Status == "accepted" || f.Status == "empty" {
			// A valid empty observation supersedes retained values.
			out.Fields[name] = f
		} else if _, ok := out.Fields[name]; !ok {
			out.Fields[name] = f
		}
	}
	return out
}
