package canbusreg

import "encoding/binary"

// PCSSlotPCSInput supplies explicit slot admission and diagnostic context.
// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: 326, 327, 333, 334: optional slot extensions;
// Measurement conversions; Opaque fields. All values remain candidates.
// SlotValid and GroupValid assert an external association with the selected
// slot/group. Member is the explicitly associated reporting member dictionary,
// never a dictionary inferred from a word or a forwarding publisher.
type PCSSlotPCSInput struct {
	PCSIdentityPCSInput
	SlotValid, GroupValid   bool
	NativeSlot, NativeGroup uint8
	Member                  *Selection
}

func release11(s Selection) bool {
	c := s.SelectionSpecValue()
	return s.SelectionGroup() == "L8" && (c.ProjectCode == "QAAA" && c.Release == "0011" || c.ProjectCode == "QAAa" && c.Release == "9011" && c.Choice == "release11")
}

// DecodePCSSlots consumes supplied evidence without bus I/O or caching.
// The ordinary and parallel current offsets require Bank="standard" or
// Bank="parallel" respectively; neither is inferred from current plausibility.
func DecodePCSSlots(s Selection, i PCSSlotPCSInput) NativeResult {
	r := selectionResult(s, i.LINKInput, pCSIdentityAdmit(s, i.PCSIdentityPCSInput))
	if i.Member != nil {
		r.Member, r.HasMember = *i.Member, true
	}
	if r.Status != "decoded" {
		return r
	}
	p := r.Raw
	selected := release11(s)
	be := func(at int) int64 { return int64(binary.BigEndian.Uint16(p[at : at+2])) }
	add := func(name string, n, d int64, unit, meaning string, gate bool) {
		pCSIdentityAdd(&r, i.PCSIdentityPCSInput, name, n, d, unit, meaning, "Proven", gate)
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
		switch s.SelectionSpecValue().Bank {
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
			c, m := s.SelectionSpecValue(), i.Member.SelectionSpecValue()
			if m.Direction != c.Direction || m.Flavor != c.Flavor || m.Interface != c.Interface || m.Epoch != c.Epoch || m.Scope != c.Scope || m.Bank != c.Bank || m.Endpoint != c.Endpoint || m.Group != c.Group || m.Slot != c.Slot {
				continue
			}
			count, _ := dictionary(*i.Member)
			if count > 0 && word == 0 {
				add(name, 0, 1, "", "no selected code", true)
				field := r.Fields[name]
				if field.Status == "accepted" {
					field.Status = "empty"
					r.Fields[name] = field
				}
				// A valid zero word supersedes a previously accepted index only
				// when the index passes its own state and age checks.
				add(name+"_index", 0, 1, "", "", true)
				idx := r.Fields[name+"_index"]
				if _, own := i.FieldAges[name+"_index"]; !own {
					idx.Age = field.Age
				}
				if idx.Status == "accepted" {
					idx.Status = field.Status
				}
				r.Fields[name+"_index"] = idx
				continue
			}
			for index := 0; index < count; index++ {
				expected, _ := EncodeDTC(*i.Member, index)
				if word == expected {
					add(name, int64(word), 1, "", "selected member numeric word", true)
					src := r.Fields[name]
					add(name+"_index", int64(index), 1, "", "member numeric index; condition opaque", src.Status == "accepted")
					// The index derives from the source word, so it inherits the
					// word's withheld status and age.
					idx := r.Fields[name+"_index"]
					if src.Status != "accepted" {
						idx.Status, idx.Unit, idx.Meaning = src.Status, "", ""
					}
					if _, own := i.FieldAges[name+"_index"]; !own {
						idx.Age = src.Age
					}
					r.Fields[name+"_index"] = idx
					break
				}
			}
		}
	case 0x334:
		padding := p[2] == 0 && p[3] == 0 && p[4] == 0 && p[5] == 0 && p[6] == 0 && p[7] == 0
		modern := s.SelectionSpecValue().ProjectCode == "QAAa"
		add("state", int64(p[0]), 1, "", "state code; meaning opaque", selected && padding && (modern || p[1] == 0))
		add("independent", int64(p[1]), 1, "", "independent code; meaning opaque", selected && modern && padding)
	default:
		r.Status = "unsupported"
	}
	return r
}
