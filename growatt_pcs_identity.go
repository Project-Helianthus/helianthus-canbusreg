package canbusreg

import (
	"encoding/binary"
	"fmt"
)

// DecodePCSIdentity decodes admitted identity records without selecting a profile from their contents.
// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 320, 321, 323: identity and configuration;
// 324, 325: serial fragments and placeholder; Opaque fields.
func DecodePCSIdentity(s CXSelection, i PCSIdentityPCSInput) CXNativeResult {
	r := selectionResult(s, i.LINKInput, pCSIdentityAdmit(s, i))
	if r.Status != "decoded" {
		return r
	}
	p := r.Raw
	c := s.SelectionSpecValue()
	id := c.ProjectCode + c.Release
	g := s.SelectionGroup()
	selected := g != "" && c.Flavor == "pcs-standard"
	local := c.Scope == "local"
	gateway := id == "QCAA0001" && c.Scope == "aggregate"
	pid := i.Evidence.Frame.ID().Value()
	add := func(name string, n int64, unit, meaning, grade string, gate bool) {
		pCSIdentityAdd(&r, i, name, n, 1, unit, meaning, grade, gate)
	}
	raw := func(name string, n int64) { add(name, n, "", "", "Unknown", false) }
	if gateway && (pid == 0x321 || pid == 0x324 || pid == 0x325) || selected && local && (id == "QAaT8252" || id == "QAAA0002") && pid == 0x324 {
		for at, b := range p {
			raw(fmt.Sprintf("byte_%d", at), int64(b))
		}
		add("placeholder", 0, "", "zero placeholder", "Hypothesis", p == [8]byte{})
		return r
	}
	switch pid {
	case 0x320:
		for at, name := range []string{"product_0", "product_1", "hardware"} {
			add(name, int64(p[at]), "", "identity code; cannot select profile", "Proven", selected)
		}
		for _, at := range []int{3, 5} {
			add(fmt.Sprintf("version_%d", at), int64(binary.LittleEndian.Uint16(p[at:at+2])), "", "version code", "Proven", selected)
		}
		add("reserved_7", int64(p[7]), "", "zero reserved byte", "Proven", selected && p[7] == 0)
	case 0x321:
		omitted := p[0] == 0 && p[1] == 0 && p[2] == 0 && p[3] == 0 && p[4] == 0 && p[5] == 0
		capable := g == "L4" || g == "L5" || g == "L6" || g == "L7" || g == "L8" || id == "QAAA0008" || id == "QAAa6008" || id == "QAAa6009"
		ascii := true
		for _, b := range p[1:5] {
			ascii = ascii && b >= 0x20 && b <= 0x7e
		}
		add("family", int64(binary.BigEndian.Uint32(p[1:5])), "", "four ASCII family bytes", "Proven", selected && capable && p[0] == 0 && p[5] == 0 && ascii)
		add("family_omitted", 0, "", "unlabelled; never an inferred family", "Hypothesis", selected && local && omitted && (capable || id == "QAaT8252" || id == "QAAA0002" || id == "QAAA0005" || id == "QAAA0006" || id == "QAAA0007" || id == "QAAa7004" || id == "QAAa8008"))
		raw("reserved_0", int64(p[0]))
		raw("reserved_5", int64(p[5]))
		add("tail", int64(binary.BigEndian.Uint16(p[6:8])), "", "independent tail", "Proven", selected)
	case 0x323:
		count := int64(p[0])
		grade := "Proven"
		if gateway {
			count += 256 * int64(p[3])
			grade = "Hypothesis"
		}
		add("count", count, "", "configuration count", grade, selected || gateway)
		n := int64(binary.BigEndian.Uint16(p[1:3]))
		raw("parameter", n)
		raw("separate_3", int64(p[3]))
		for at := 4; at < 8; at++ {
			add(fmt.Sprintf("synthesized_%d", at), int64(p[at]), "", "synthesized byte", "Proven", selected)
		}
		threshold := n
		gate := selected && local && g == "L8" && i.MeasurementValid && i.ThresholdStateKnown && !i.AlternativeEligible
		add("threshold", threshold, "mV", "context-selected threshold", "Hypothesis", gate)
	case 0x324, 0x325:
		for at, b := range p {
			raw(fmt.Sprintf("byte_%d", at), int64(b))
		}
	default:
		r.Status = "unsupported"
	}
	return r
}

// PCSIdentitySerialResult is detached from the assembler and earlier telemetry.
// Fragments retain all three native evidence records, including padding.
type PCSIdentitySerialResult struct {
	Status            string
	Serial            [16]byte
	Record            CXNativeResult
	Fragments         [3]CXNativeResult
	PhysicalQualified bool
}

// PCSIdentitySerialAssembler stores one bounded, uninterrupted receive sequence.
// The caller owns serialization of Consume calls; no bus operations occur.
type PCSIdentitySerialAssembler struct {
	active    bool
	selection CXSelection
	slot      uint8
	next      byte
	serial    [16]byte
	fragments [3]CXNativeResult
}

func (a *PCSIdentitySerialAssembler) clear() { *a = PCSIdentitySerialAssembler{} }
// Consume assembles ordered identity fragments, clearing pending assembly on rejected input; callers must serialize access.
func (a *PCSIdentitySerialAssembler) Consume(s CXSelection, i PCSIdentityPCSInput) PCSIdentitySerialResult {
	record := DecodePCSIdentity(s, i)
	out := PCSIdentitySerialResult{Status: "rejected", Record: record}
	p := record.Raw
	c := s.SelectionSpecValue()
	admissible := record.Status == "decoded" && i.Evidence.Frame.ID().Value() == 0x324 && s.SelectionGroup() != "" && c.Flavor == "pcs-standard" && c.Scope == "local" && i.SerialSlotValid && record.Fields["placeholder"].Status != "accepted" && c.ProjectCode+c.Release != "QAaT8252" && c.ProjectCode+c.Release != "QAAA0002"
	if !admissible {
		a.clear()
		return out
	}
	fragment := record
	fragment.Fields = make(map[string]CXNativeField)
	pCSIdentityAdd(&fragment, i, "serial_fragment", int64(p[0]), 1, "", "ordered identity fragment", "Proven", true)
	if fragment.Fields["serial_fragment"].Status != "accepted" {
		a.clear()
		out.Record = fragment
		out.Status = fragment.Fields["serial_fragment"].Status
		return out
	}
	part := p[0]
	if a.active {
		previous := a.fragments[a.next-1].Evidence
		if a.selection != s || a.slot != i.SerialSlot || part != a.next || i.Evidence.Sequence <= previous.Sequence || i.Evidence.Sequence-previous.Sequence != 1 || i.Evidence.Monotonic <= previous.Monotonic {
			a.clear()
			return out
		}
	} else {
		if part != 0 || p[1] != i.SerialSlot {
			return out
		}
		a.active = true
		a.selection = s
		a.slot = i.SerialSlot
	}
	switch part {
	case 0:
		copy(a.serial[0:6], p[2:8])
	case 1:
		copy(a.serial[6:13], p[1:8])
	case 2:
		if p[4] != 0 || p[5] != 0 || p[6] != 0 || p[7] != 0 {
			a.clear()
			return out
		}
		copy(a.serial[13:16], p[1:4])
	default:
		a.clear()
		return out
	}
	// The fragment map is separate from the pending result's map.
	a.fragments[part] = fragment
	a.next = part + 1
	if part == 2 {
		out.Status = "complete"
		out.Serial = a.serial
		out.Fragments = a.fragments
		a.clear()
	} else {
		out.Status = "pending"
	}
	return out
}
