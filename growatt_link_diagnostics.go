package canbusreg

import (
	"encoding/binary"
	"fmt"
)

// DecodeLINKDiagnostics decodes admitted diagnostic evidence without inferring a unique fault cause.
// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Sections: Faults and additional status; Release-11 field and behavior gates.
// This is a stateless receive-only projection. Current bits, selected words,
// member state and coordinator state remain separate observations.
func DecodeLINKDiagnostics(s Selection, i LINKInput) NativeResult {
	r := selectionResult(s, i, "invalid")
	if i.Evidence.Sequence == 0 || i.Evidence.Monotonic < 0 {
		return r
	}
	kind := i.Evidence.Frame.ID().Value() >> 16
	module := lINKAdmission(s, i)
	monitor := lINKReportAdmission(s, i) && kind == 0x1869
	coordinator := lINKCoordinatorAdmission(s, i) && s.spec.Destination == 0xdf && kind == 0x1869
	if !module && !monitor && !coordinator {
		return r
	}
	r.Status = "unsupported"
	group := s.SelectionGroup()
	if coordinator {
		// The immutable selector intentionally withholds module features for a
		// coordinator direction. Resolve the explicit member family separately;
		// the returned result still carries the original coordinator selection.
		c := s.SelectionSpecValue()
		c.Direction = "module-to-coordinator"
		member, err := NewSelection(c)
		if err != nil {
			return r
		}
		group = member.SelectionGroup()
	}
	if group == "" {
		return r
	}
	p := r.Raw
	add := func(name string, n int64, meaning, grade, status, unit string) {
		r.Fields[name] = NativeField{Numerator: n, Denominator: 1, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
	}
	raw := func(name string, n byte) { add(name, int64(n), "", "Unknown", "raw", "") }
	zero := func(at int) {
		status := "accepted"
		if p[at] != 0 {
			status = "invalid"
		}
		add(fmt.Sprintf("reserved_%d", at), int64(p[at]), "reserved zero", "Proven", status, "")
	}
	switch kind {
	case 0x1850, 0x1851, 0x1852:
		if !module || kind == 0x1851 && group == "L0" {
			return r
		}
		grade := "Proven"
		if kind == 0x1852 {
			switch group {
			case "L3", "L4", "L5", "L7", "L8":
			default:
				return r
			}
			grade = "Hypothesis"
		}
		base := int(kind-0x1850) * 64
		for byteOffset, value := range p {
			for bit := 0; bit < 8; bit++ {
				index := base + byteOffset*8 + bit
				add(fmt.Sprintf("current_fault_%d", index), int64(value>>uint(bit)&1), "current fault bit", grade, "accepted", "")
			}
		}
	case 0x1853:
		if !module || group != "L8" {
			return r
		}
		for j, name := range []string{"selected_a", "selected_b"} {
			n := binary.LittleEndian.Uint16(p[j*2 : j*2+2])
			status, meaning := "accepted", "selected numeric word"
			if n == 0 {
				status, meaning = "empty", "no selected code"
			}
			add(name, int64(n), meaning, "Proven", status, "")
		}
		for at := 4; at < 8; at++ {
			zero(at)
		}
	case 0x1861, 0x1869:
		if group != "L7" && group != "L8" || kind == 0x1861 && !module || kind == 0x1869 && !monitor && !coordinator {
			return r
		}
		prefix := ""
		if coordinator {
			prefix = "coordinator_"
		}
		for at := 0; at < 3; at++ {
			raw(fmt.Sprintf("%sstate_%d", prefix, at), p[at])
		}
		if kind == 0x1861 {
			add("counter", int64(p[3]), "counter saturated at 255; untruncated value unknown", "Proven", "accepted", "")
			for at := 4; at < 7; at++ {
				zero(at)
			}
		} else {
			for _, at := range []int{3, 5} {
				add(fmt.Sprintf("%sword_%d", prefix, at), int64(binary.LittleEndian.Uint16(p[at:at+2])), "", "Unknown", "raw", "")
			}
		}
		name := prefix + "additional_state"
		raw(name, p[7])
		if s.spec.ProjectCode == "QAAA" && s.spec.Release == "0011" {
			// The output contract specifies zero; the receiving input ignores B7.
			f := r.Fields[name]
			f.Status = "ignored"
			r.Fields[name] = f
		}
	case 0x1807:
		if !module || group != "L8" {
			return r
		}
		for _, at := range []int{2, 3} {
			add(fmt.Sprintf("count_b%d", at), int64(p[at]), "two category counters summed modulo 256; contributors unknown", "Hypothesis", "accepted", "count")
		}
	default:
		return r
	}
	r.Status = "accepted"
	return r
}
