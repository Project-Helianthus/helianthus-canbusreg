package canbusreg

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

// Native contract: https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-pcs-protocol-v1.md
// Sections: Standard parallel / clustering profile; Addressed families
// 50X…68X, 6CX, 6DX; Coordinator summaries 590, 690, 6A0, 6B0, 6E0.
// Candidate decoding never establishes physical qualification or inventory.
func cx10Admission(s CXSelection, i CX01LINKInput) bool {
	c := s.CX01Spec()
	f := i.Evidence.Frame
	g, e := strconv.ParseUint(c.Group, 10, 8)
	return e == nil && g < 8 && c.Group == strconv.FormatUint(g, 10) && c.Flavor == "pcs-parallel" && s.CX01Group() != "" && c.Direction == "bms-to-pcs" && !f.ID().Extended() && f.DLC() == 8 && f.RawDLC() == 8 && i.Evidence.Interface == c.Interface && i.Epoch == c.Epoch && i.Direction == c.Direction && i.Scope == c.Scope && i.Bank == c.Bank && i.Endpoint == c.Endpoint && i.Group == c.Group && i.Slot == c.Slot && i.FieldAge >= 0
}

func cx10Family(s CXSelection, id uint32) (uint32, bool) {
	c := s.CX01Spec()
	g, _ := strconv.ParseUint(c.Group, 10, 8)
	if id == 0x37f || id == 0x3f7 {
		return id, s.CX01Group() == "L8"
	}
	family := id &^ 15
	if id == 0x590 || id == 0x690 || id == 0x6a0 || id == 0x6b0 || id == 0x6e0 {
		if c.Scope != "aggregate" {
			return 0, false
		}
		family = id
	} else if id&15 != uint32(g) {
		return 0, false
	}
	group := s.CX01Group()
	later := group == "L5" || group == "L7" || group == "L8"
	switch family {
	case 0x600, 0x610, 0x620, 0x630, 0x640, 0x650, 0x660, 0x670, 0x680, 0x6c0, 0x690, 0x6a0, 0x6b0:
		return family, group == "L6" || later
	case 0x500, 0x6d0, 0x6e0:
		return family, later
	case 0x510, 0x590:
		return family, group == "L7" || group == "L8"
	case 0x520:
		return family, group == "L8"
	}
	return family, false
}

func CX10DecodeParallel(s CXSelection, i CX01LINKInput) CXNativeResult {
	r := cx01Result(s, i, "unsupported")
	if !cx10Admission(s, i) {
		r.Status = "invalid"
		return r
	}
	family, ok := cx10Family(s, i.Evidence.Frame.ID().Value())
	if !ok {
		return r
	}
	p := r.Raw
	add := func(name string, n int64, unit, meaning, status string) {
		grade := "Proven"
		if status == "raw" {
			grade = "Unknown"
		}
		r.Fields[name] = CXNativeField{Numerator: n, Denominator: 1, Unit: unit, Meaning: meaning, Grade: grade, Status: status, Raw: p, Evidence: i.Evidence, Epoch: i.Epoch, Scope: i.Scope, Bank: i.Bank, Endpoint: i.Endpoint, Group: i.Group, Slot: i.Slot, Age: i.FieldAge}
	}
	b := func(name string, at int) { add(name, int64(p[at]), "", name, "accepted") }
	w := func(name string, at int) {
		add(name, int64(binary.LittleEndian.Uint16(p[at:at+2])), "", name, "accepted")
	}
	rawWord := func(name string, at int) {
		w(name, at)
		f := r.Fields[name]
		f.Status = "raw"
		f.Grade = "Unknown"
		f.Meaning = ""
		r.Fields[name] = f
	}
	invalid := func(name string) {
		f := r.Fields[name]
		f.Status = "invalid"
		f.Meaning = ""
		f.Unit = ""
		r.Fields[name] = f
	}
	switch family {
	case 0x500, 0x660, 0x670:
		offset := 0
		if family == 0x500 {
			offset = 16
		}
		if family == 0x670 {
			offset = 8
		}
		for at := 0; at < 8; at++ {
			b(fmt.Sprintf("fault_%d", offset+at), at)
		}
	case 0x600:
		w("product", 0)
		b("hardware", 2)
		rawWord("version_mux", 3)
		for at := 5; at < 8; at++ {
			b(fmt.Sprintf("count_config_%d", at), at)
		}
		if i.Scope == "aggregate" && r.Fields["product"].Numerator == 65535 {
			invalid("product")
		}
	case 0x610:
		rawWord("voltage_mux", 0)
		rawWord("current", 2)
		rawWord("port", 4)
		rawWord("soc", 6)
	case 0x620:
		rawWord("limit_0", 0)
		rawWord("limit_2", 2)
		b("permission_4", 4)
		b("permission_5", 5)
		b("state", 6)
		b("flags", 7)
	case 0x630:
		for at := 0; at < 8; at += 2 {
			rawWord(fmt.Sprintf("quantity_%d", at), at)
		}
	case 0x640:
		b("maximum_index", 0)
		b("zero_1", 1)
		w("maximum_mv", 2)
		b("minimum_index", 4)
		b("zero_5", 5)
		w("minimum_mv", 6)
		for _, name := range []string{"maximum_mv", "minimum_mv"} {
			f := r.Fields[name]
			f.Unit = "mV"
			r.Fields[name] = f
		}
		if p[1] != 0 {
			invalid("zero_1")
			invalid("maximum_index")
			invalid("maximum_mv")
		}
		if p[5] != 0 {
			invalid("zero_5")
			invalid("minimum_index")
			invalid("minimum_mv")
		}
	case 0x650:
		for at, name := range []string{"maximum_index", "zero_1", "maximum_code", "minimum_index", "zero_4", "minimum_code", "extra", "soh"} {
			b(name, at)
		}
		if p[1] != 0 {
			invalid("zero_1")
			invalid("maximum_index")
			invalid("maximum_code")
		}
		if p[4] != 0 {
			invalid("zero_4")
			invalid("minimum_index")
			invalid("minimum_code")
		}
	case 0x690:
		for at := 0; at < 8; at++ {
			b(fmt.Sprintf("summary_%d", at), at)
		}
	case 0x6a0:
		for at := 0; at < 8; at += 2 {
			rawWord(fmt.Sprintf("word_%d", at), at)
		}
	case 0x6b0:
		count := 1
		c := s.CX01Spec()
		if c.ProjectCode+c.Release == "QAAB0004" {
			count = 4
		}
		if c.ProjectCode+c.Release == "QAAb5004" || s.CX01Group() == "L7" || s.CX01Group() == "L8" {
			count = 5
		}
		for at := 0; at < count; at++ {
			b(fmt.Sprintf("summary_%d", at), at)
		}
	case 0x6e0:
		rawWord("word_0", 0)
		rawWord("word_2", 2)
		for at := 4; at < 8; at++ {
			b(fmt.Sprintf("state_%d", at), at)
		}
	default:
		// Availability alone supplies no field meanings for these families.
		r.Status = "raw"
		return r
	}
	r.Status = "accepted"
	return r
}
