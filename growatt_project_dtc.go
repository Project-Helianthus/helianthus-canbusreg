package canbusreg

import "encoding/binary"

// Native numeric contract (candidate protocol evidence, never qualification):
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: Numeric dictionary and version gate; Disabled local selections;
// Selection, severity and clearing.
// https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-link-protocol-v1.md
// Section: Numeric dictionary and version gate.
func dictionary(member Selection) (count int, binaryWords bool) {
	c := member.SelectionSpecValue()
	if member.SelectionGroup() != "L8" {
		return 0, false
	}
	if c.ProjectCode == "QAAA" && c.Release == "0011" {
		return 134, false
	}
	if c.ProjectCode == "QAAa" && c.Release == "9011" && c.Choice == "release11" {
		return 136, true
	}
	return 0, false
}

// EncodeDTC maps a numeric index to a candidate word, without bus I/O.
func EncodeDTC(member Selection, index int) (uint16, bool) {
	count, modern := dictionary(member)
	if index < 0 || index >= count {
		return 0, false
	}
	if modern {
		if index > 130 {
			return [...]uint16{1185, 1151, 1161, 1181, 1188}[index-131], true
		}
		return uint16(1000 + index), true
	}
	n, word := 1000+index, uint16(0)
	for shift := uint(0); shift < 16; shift += 4 {
		word |= uint16(n%10) << shift
		n /= 10
	}
	return word, true
}

// DecodeDTC decodes one caller-located selected word in native evidence.
// member must identify the actual reporting member, including any explicit
// topology association; forwarding coordinator identity is not a substitute.
// offset locates the word, not an active or retained bitmap. No state is mutated.
func DecodeDTC(member Selection, i LINKInput, offset int) NativeResult {
	r := selectionResult(member, i, "invalid")
	c := member.SelectionSpecValue()
	f := i.Evidence.Frame
	id := f.ID().Value()
	if offset < 0 || offset > 6 || f.DLC() != 8 || f.RawDLC() != 8 || i.FieldAge < 0 ||
		i.Evidence.Interface != c.Interface || i.Epoch != c.Epoch || i.Direction != c.Direction || i.Scope != c.Scope || i.Bank != c.Bank || i.Endpoint != c.Endpoint || i.Group != c.Group || i.Slot != c.Slot {
		return r
	}
	var order binary.ByteOrder = binary.BigEndian
	switch c.Flavor {
	case "link":
		if !lINKAdmission(member, i) || id>>16 != 0x1853 {
			return r
		}
		order = binary.LittleEndian
	case "pcs-standard":
		if c.Direction != "bms-to-pcs" || f.ID().Extended() || id != 0x333 {
			return r
		}
	case "pcs-parallel":
		if c.Direction != "bms-to-pcs" || f.ID().Extended() || id < 0x520 || id > 0x52f {
			return r
		}
	default:
		return r
	}
	r.Status = "unsupported"
	count, _ := dictionary(member)
	if count == 0 {
		return r
	}
	word := order.Uint16(r.Raw[offset : offset+2])
	field := NativeField{Numerator: int64(word), Denominator: 1, Raw: r.Raw, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge, Grade: "Hypothesis", Status: "unknown"}
	r.Status = "unknown"
	if word == 0 {
		field.Grade, field.Status, field.Meaning = "Proven", "empty", "no selected code"
		r.Status = "empty"
	} else {
		// Exact finite membership also rejects every nondecimal packed nibble.
		for index := 0; index < count; index++ {
			expected, _ := EncodeDTC(member, index)
			if expected != word {
				continue
			}
			field.Grade, field.Status, field.Meaning = "Proven", "accepted", "selected numeric word"
			idx := field
			idx.Numerator = int64(index)
			idx.Meaning = "numeric diagnostic index; condition opaque"
			r.Fields["numeric_index"] = idx
			r.Status = "accepted"
			break
		}
	}
	r.Fields["selected_word"] = field
	return r
}

func categories(index int) (int, int) {
	switch index {
	case 19, 24, 30, 35, 56:
		return 1, 1
	case 5, 13, 17, 25, 26, 28, 29, 31, 34, 38, 42, 43, 44, 45, 47, 50, 51, 84, 87, 90, 92, 94, 95, 100, 101, 102, 103, 128, 130, 133:
		return 2, 2
	case 3, 9, 15, 21, 32, 53:
		return 2, 4
	case 52, 61:
		return 3, 2
	case 4, 12, 16, 18, 33, 46, 57, 58, 60, 85, 91, 93, 96, 109, 110:
		return 3, 3
	case 2, 8, 11, 14, 20:
		return 3, 4
	case 48, 62:
		return 4, 1
	case 0, 1, 6, 7, 23, 39, 40, 41, 49, 63, 88, 97, 99:
		return 4, 2
	case 22, 89, 98:
		return 4, 3
	case 59, 83, 86, 123, 129, 131, 132, 134, 135:
		return 4, 4
	default:
		return 0, 0
	}
}

// DTCLocalSelection is a pure explanation of a supplied active set, not a
// fault activation/clearing operation or a reconstruction from received words.
// -1 denotes no index selected. Categories carry no severity labels.
type DTCLocalSelection struct {
	Active            [136]bool
	Categories        [4]int
	A, B              int
	WordA, WordB      uint16
	Status, Grade     string
	PhysicalQualified bool
}

// ExplainLocalSelection explains local diagnostic category selection; unsupported dictionaries or out-of-range active indices are rejected.
func ExplainLocalSelection(member Selection, active [136]bool) DTCLocalSelection {
	r := DTCLocalSelection{Active: active, Categories: [4]int{-1, -1, -1, -1}, A: -1, B: -1, Status: "unsupported", Grade: "Unknown"}
	count, _ := dictionary(member)
	if count == 0 || member.SelectionSpecValue().Scope != "local" {
		return r
	}
	for index := count; index < len(active); index++ {
		if active[index] {
			r.Status = "invalid"
			return r
		}
	}
	for index := 0; index < count; index++ {
		if !active[index] {
			continue
		}
		a, b := categories(index)
		if a != 0 {
			r.Categories[a-1] = index
			r.Categories[b-1] = index
		}
	}
	r.A = r.Categories[0]
	if r.A < 0 {
		r.A = r.Categories[1]
	}
	r.B = r.Categories[2]
	if r.B < 0 {
		r.B = r.Categories[3]
	}
	if r.A >= 0 {
		r.WordA, _ = EncodeDTC(member, r.A)
	}
	if r.B >= 0 {
		r.WordB, _ = EncodeDTC(member, r.B)
	}
	r.Status, r.Grade = "accepted", "Proven"
	return r
}
