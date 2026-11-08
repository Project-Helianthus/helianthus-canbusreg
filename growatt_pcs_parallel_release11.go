package canbusreg

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: Common release-11 PCS contract; Standard parallel / clustering profile.
// Sources are explicit, independently attributed candidate values, never inferred
// from another byte or defaulted to zero. Nil means absent. No source is updated.
type CX11ParallelContext struct {
	SOHTenths, SOCTenths, Permission, State *CXNativeField
	CategoryFirst, CategorySecond, PackCode *CXNativeField
	// CellSumValid is independent evidence that the cell-sum word is a valid
	// measurement (value 1). Zero diagnostic counters alone do not establish it.
	CellSumValid *CXNativeField
}

func cx11SourceValue(s CXSelection, f *CXNativeField, maximum int64) (int64, bool) {
	c := s.CX01Spec()
	if f == nil {
		return 0, false
	}
	return f.Numerator, f.Status == "accepted" && (f.Grade == "Proven" || f.Grade == "Hypothesis") && f.Denominator == 1 && f.Numerator >= 0 && f.Numerator <= maximum && f.Evidence.Interface == c.Interface && f.Epoch == c.Epoch && f.Scope == c.Scope && f.Bank == c.Bank && f.Endpoint == c.Endpoint && f.Group == c.Group && f.Slot == c.Slot && f.Age >= 0
}

// CX11DecodeParallel is a pure receiver decoder. Context validates derived wire
// codes without constructing traffic or turning substituted codes into fresh
// measurements. Candidate protocol grades never establish qualification.
func CX11DecodeParallel(s CXSelection, i CX01LINKInput, x CX11ParallelContext) CXNativeResult {
	r := cx01Result(s, i, "unsupported")
	if !cx10Admission(s, i) {
		r.Status = "invalid"
		return r
	}
	if s.CX01Group() != "L8" {
		return r
	}
	family, ok := cx10Family(s, i.Evidence.Frame.ID().Value())
	if !ok {
		return r
	}
	p := r.Raw
	if family == 0x37f || family == 0x3f7 {
		r.Status = "raw"
		return r
	}
	// Slot-bearing publications must identify the explicitly selected member.
	if family == 0x510 || family == 0x520 {
		slot, e := strconv.ParseUint(i.Slot, 10, 8)
		if e != nil || i.Slot != strconv.FormatUint(slot, 10) || byte(slot) != p[0] {
			r.Status = "invalid"
			return r
		}
	}
	add := func(name string, n, d int64, unit, meaning, grade, status string) {
		r.Fields[name] = CXNativeField{Numerator: n, Denominator: d, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
	}
	raw := func(name string, n int64) { add(name, n, 1, "", "", "Unknown", "raw") }
	proven := func(name string, n int64) { add(name, n, 1, "", name, "Proven", "accepted") }
	le := func(at int) int64 { return int64(binary.LittleEndian.Uint16(p[at : at+2])) }
	mark := func(name, status string) {
		f := r.Fields[name]
		f.Status = status
		f.Grade = "Unknown"
		f.Unit = ""
		f.Meaning = ""
		r.Fields[name] = f
	}
	derived := func(name string, wire, d, expected int64, known bool, unit, meaning, grade string) {
		add(name, wire, d, unit, meaning, grade, "accepted")
		if !known {
			mark(name, "withheld")
		} else if wire != expected {
			mark(name, "conflict")
		}
	}
	zero := func(name string, at int) {
		proven(name, int64(p[at]))
		if p[at] != 0 {
			mark(name, "invalid")
		}
	}
	switch family {
	case 0x510:
		proven("slot", int64(p[0]))
		proven("minimum_cell", le(1))
		raw("independent_b3", int64(p[3]))
		for at := 4; at < 7; at++ {
			proven(fmt.Sprintf("state_%d", at), int64(p[at]))
		}
		if s.CX01Spec().ProjectCode == "QAAa" {
			proven("state_7", int64(p[7]))
		} else if i.Scope == "local" {
			zero("state_7", 7)
		} else {
			raw("state_7", int64(p[7]))
		}
	case 0x520:
		proven("slot", int64(p[0]))
		for j, name := range []string{"dtc_a", "dtc_b"} {
			d := CX07DecodeDTC(s, i, 1+2*j)
			if f, exists := d.Fields["selected_word"]; exists {
				r.Fields[name] = f
			}
			if f, exists := d.Fields["numeric_index"]; exists {
				r.Fields[name+"_index"] = f
			}
		}
		for at := 5; at < 8; at++ {
			zero(fmt.Sprintf("zero_%d", at), at)
		}
	case 0x610:
		proven("mux", le(0)>>15)
		add("voltage", le(0)&32767, 10, "V", "voltage quantity; mux retained separately", "Proven", "accepted")
		add("current", le(2)-10000, 10, "A", "native biased current", "Proven", "accepted")
		raw("port", le(4))
		if i.Scope == "local" {
			add("port", le(4), 10, "V", "local port voltage", "Proven", "accepted")
		}
		add("soc", le(6), 10, "percent", "SOC", "Proven", "accepted")
	case 0x650:
		proven("maximum_index", int64(p[0]))
		zero("zero_1", 1)
		proven("minimum_index", int64(p[3]))
		zero("zero_4", 4)
		for j, name := range []string{"maximum_temperature", "minimum_temperature", "average_temperature"} {
			at := []int{2, 5, 6}[j]
			raw(name, int64(p[at]))
			if i.Scope == "local" {
				add(name, int64(p[at])-40, 1, "C", "temperature code minus 40", "Hypothesis", "accepted")
			}
		}
		if p[1] != 0 {
			raw("maximum_temperature", int64(p[2]))
			mark("maximum_temperature", "invalid")
			mark("maximum_index", "invalid")
		}
		if p[4] != 0 {
			raw("minimum_temperature", int64(p[5]))
			mark("minimum_temperature", "invalid")
			mark("minimum_index", "invalid")
		}
		raw("soh", int64(p[7]))
		if i.Scope == "local" {
			n, known := cx11SourceValue(s, x.SOHTenths, 65535)
			derived("soh", int64(p[7]), 1, (820*n/8192)%256, known, "", "derived SOH code; not an inverse measurement", "Hypothesis")
		}
	case 0x680:
		raw("limit_a", le(0))
		raw("limit_b", le(2))
		raw("soc_report", le(4))
		raw("state_bit_2", int64(p[6]))
		raw("state_bit_1", int64(p[7]))
		if i.Scope == "local" {
			permission, kp := cx11SourceValue(s, x.Permission, 1)
			soc, ks := cx11SourceValue(s, x.SOCTenths, 65535)
			expected := int64(0)
			known := kp
			meaning := "inactive SOC report; not a fresh measurement"
			if permission != 0 {
				known = known && ks
				expected = soc
				meaning = "reported SOC code; not an independent measurement"
				if soc == 0 {
					expected = 10
					meaning = "substituted SOC code; not a fresh measurement"
				}
			}
			derived("soc_report", le(4), 10, expected, known, "percent", meaning, "Proven")
			state, known := cx11SourceValue(s, x.State, 255)
			derived("state_bit_2", int64(p[6]), 1, (state>>2)&1, known, "", "copy of state bit 2", "Proven")
			derived("state_bit_1", int64(p[7]), 1, (state>>1)&1, known, "", "copy of state bit 1", "Proven")
		}
	case 0x6d0:
		raw("cell_sum", le(0))
		raw("pack_over_200", int64(p[2]))
		raw("state_nonzero", int64(p[3]))
		for at := 4; at < 8; at++ {
			zero(fmt.Sprintf("zero_%d", at), at)
		}
		if i.Scope == "local" {
			a, ka := cx11SourceValue(s, x.CategoryFirst, 65535)
			b, kb := cx11SourceValue(s, x.CategorySecond, 65535)
			add("cell_sum", le(0), 10, "V", "cell-sum voltage", "Hypothesis", "accepted")
			if !ka || !kb {
				mark("cell_sum", "withheld")
			} else if a != 0 || b != 0 {
				f := r.Fields["cell_sum"]
				f.Status = "invalid"
				f.Unit = ""
				f.Meaning = "diagnostic-invalid sentinel"
				if le(0) != 65535 {
					f.Status = "conflict"
				}
				r.Fields["cell_sum"] = f
			} else if le(0) == 65535 {
				mark("cell_sum", "invalid")
			} else if valid, kv := cx11SourceValue(s, x.CellSumValid, 1); !kv || valid != 1 {
				mark("cell_sum", "withheld")
			}
			pack, kp := cx11SourceValue(s, x.PackCode, 65535)
			state, ks := cx11SourceValue(s, x.State, 255)
			flag := int64(0)
			if pack > 200 {
				flag = 1
			}
			derived("pack_over_200", int64(p[2]), 1, flag, kp, "", "pack code exceeds 200", "Hypothesis")
			flag = 0
			if state != 0 {
				flag = 1
			}
			derived("state_nonzero", int64(p[3]), 1, flag, ks, "", "nonzero native state; enum unspecified", "Hypothesis")
		}
	case 0x590:
		for at := 0; at < 3; at++ {
			proven(fmt.Sprintf("state_%d", at), int64(p[at]))
		}
		raw("aggregate_3", le(3))
		raw("aggregate_5", le(5))
		// Native output zero does not establish the receiver's input feature gate.
		raw("input_b7", int64(p[7]))
	default:
		return r
	}
	r.Status = "accepted"
	return r
}
