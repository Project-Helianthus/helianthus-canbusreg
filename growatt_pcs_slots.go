package canbusreg

import "encoding/binary"

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 326, 327, 333, 334: optional slot extensions;
// Measurement conversions; Opaque fields. All values remain candidates.
// SlotValid and GroupValid assert an external association with the selected
// slot/group. Member is the explicitly associated reporting member dictionary,
// never a dictionary inferred from a word or a forwarding publisher.
type CX08PCSInput struct {
	CX06PCSInput
	SlotValid, GroupValid   bool
	NativeSlot, NativeGroup uint8
	Member                  *CXSelection
}

func cx08Release11(s CXSelection) bool {
	c := s.CX01Spec()
	return s.CX01Group() == "L8" && (c.ProjectCode == "QAAA" && c.Release == "0011" || c.ProjectCode == "QAAa" && c.Release == "9011" && c.Choice == "release11")
}

// CX08DecodePCSSlots consumes supplied evidence without bus I/O or caching.
// The ordinary and parallel current offsets require Bank="standard" or
// Bank="parallel" respectively; neither is inferred from current plausibility.
func CX08DecodePCSSlots(s CXSelection, i CX08PCSInput) CXNativeResult {
	r := cx01Result(s, i.CX01LINKInput, cx06Admit(s, i.CX06PCSInput))
	if r.Status != "decoded" {
		return r
	}
	p := r.Raw
	selected := cx08Release11(s)
	be := func(at int) int64 { return int64(binary.BigEndian.Uint16(p[at : at+2])) }
	add := func(name string, n, d int64, unit, meaning string, gate bool) {
		cx06Add(&r, i.CX06PCSInput, name, n, d, unit, meaning, "Proven", gate)
	}
	slot := selected && i.SlotValid && p[0] == i.NativeSlot
	measured := slot && i.MeasurementValid
	switch i.Evidence.Frame.ID().Value() {
	case 0x326:
		padding := p[6] == 0 && p[7] == 0
		add("slot", int64(p[0]), 1, "", "native slot", slot && padding)
		add("voltage", be(1), 100, "V", "wrapped cached voltage; original decivolts unrecoverable", measured && padding)
		n, d, unit := be(3), int64(1), ""
		bank := false
		switch s.CX01Spec().Bank {
		case "standard":
			n -= 4000
			bank = true
		case "parallel":
			n -= 10000
			bank = true
		}
		gate := measured && padding && bank
		if gate {
			d, unit = 10, "A"
		} else {
			n = be(3)
		}
		add("current", n, d, unit, "bank-selected current", gate)
		add("soc_low", int64(p[5]), 1, "", "low SOC-tenths byte; no unique percentage", slot && padding)
	case 0x327:
		gate := slot && p[7] == 0
		add("slot", int64(p[0]), 1, "", "native slot", gate)
		add("maximum_cell", be(1), 1, "mV", "maximum cell", gate && i.MeasurementValid)
		add("minimum_cell", be(3), 1, "mV", "minimum cell", gate && i.MeasurementValid)
		add("maximum_index", int64(p[5]), 1, "", "native maximum index", gate)
		add("minimum_index", int64(p[6]), 1, "", "native minimum index", gate)
	case 0x333:
		gate := selected && i.GroupValid && p[0] == i.NativeGroup && i.SlotValid && p[1] == i.NativeSlot && p[6] == 0 && p[7] == 0
		add("group", int64(p[0]), 1, "", "native group", gate)
		add("slot", int64(p[1]), 1, "", "native slot", gate)
		for at, name := range []string{"dtc_a", "dtc_b"} {
			word := uint16(be(2 + 2*at))
			add(name, int64(word), 1, "", "", false)
			if !gate || i.Member == nil {
				continue
			}
			c, m := s.CX01Spec(), i.Member.CX01Spec()
			if m.Direction != c.Direction || m.Flavor != c.Flavor || m.Interface != c.Interface || m.Epoch != c.Epoch || m.Scope != c.Scope || m.Bank != c.Bank || m.Endpoint != c.Endpoint || m.Group != c.Group || m.Slot != c.Slot {
				continue
			}
			count, _ := cx07Dictionary(*i.Member)
			if count > 0 && word == 0 {
				add(name, 0, 1, "", "no selected code", true)
				field := r.Fields[name]
				if field.Status == "accepted" {
					field.Status = "empty"
					r.Fields[name] = field
				}
				continue
			}
			for index := 0; index < count; index++ {
				expected, _ := CX07EncodeDTC(*i.Member, index)
				if word == expected {
					add(name, int64(word), 1, "", "selected member numeric word", true)
					add(name+"_index", int64(index), 1, "", "member numeric index; condition opaque", true)
					break
				}
			}
		}
	case 0x334:
		padding := p[2] == 0 && p[3] == 0 && p[4] == 0 && p[5] == 0 && p[6] == 0 && p[7] == 0
		modern := s.CX01Spec().ProjectCode == "QAAa"
		add("state", int64(p[0]), 1, "", "state code; meaning opaque", selected && padding && (modern || p[1] == 0))
		add("independent", int64(p[1]), 1, "", "independent code; meaning opaque", selected && modern && padding)
	default:
		r.Status = "unsupported"
	}
	return r
}
