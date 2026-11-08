package canbusreg

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func cx07Member(t *testing.T, modern bool) CXSelection {
	t.Helper()
	spec := cx01Spec(t, "QAAA", "0011")
	spec.Group, spec.Slot = "group-a", "slot-a"
	if modern {
		spec.ProjectCode, spec.Release, spec.Choice = "QAAa", "9011", "release11"
	}
	return cx01Select(t, spec)
}

func cx07Expected(index int, modern bool) uint16 {
	if modern {
		if index > 130 {
			return []uint16{1185, 1151, 1161, 1181, 1188}[index-131]
		}
		return uint16(1000 + index)
	}
	digits := fmt.Sprintf("%04d", 1000+index)
	var word uint16
	for _, d := range digits {
		word = word*16 + uint16(d-'0')
	}
	return word
}

func TestCX07ExhaustiveRoundtrip(t *testing.T) {
	for _, modern := range []bool{false, true} {
		count := 134
		if modern {
			count = 136
		}
		s := cx07Member(t, modern)
		for index := 0; index < count; index++ {
			t.Run(fmt.Sprintf("modern=%v/index=%d", modern, index), func(t *testing.T) {
				word, ok := CX07EncodeDTC(s, index)
				if !ok || word != cx07Expected(index, modern) {
					t.Fatalf("encode %d %v", word, ok)
				}
				var p [8]byte
				binary.LittleEndian.PutUint16(p[:2], word)
				i := cx01Input(t, s.CX01Spec(), 0x1853, p)
				i.FieldAge = 7
				i.Evidence.RawRecord[15] = 73
				r := CX07DecodeDTC(s, i, 0)
				f := r.Fields["numeric_index"]
				if r.Status != "accepted" || f.Numerator != int64(index) || f.Denominator != 1 || f.Grade != "Proven" || f.Raw != p || f.Evidence != i.Evidence || f.Age != i.FieldAge || f.Epoch != i.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.PhysicalQualified || r.PhysicalQualified {
					t.Fatalf("roundtrip %#v", r)
				}
				if r.Fields["selected_word"].Numerator != int64(word) {
					t.Fatal("lost word")
				}
			})
		}
		for _, index := range []int{-1, count, count + 1} {
			if _, ok := CX07EncodeDTC(s, index); ok {
				t.Fatal("out of range", index)
			}
		}
	}
}

func TestCX07UnknownWordHypothesisNegatives(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s := cx07Member(t, modern)
		words := []uint16{999, 1131, 1134, 1184, 1186, 1187, 1189, 0xffff, 0x0999, 0x1134, 0x100a, 0x10a0, 0x1a00, 0xa000}
		for _, word := range words {
			// Values are outside both dictionaries except where explicitly excluded.
			if !modern && word == 0x1131 {
				continue
			}
			t.Run(fmt.Sprintf("unknown/modern=%v/word=%d", modern, word), func(t *testing.T) {
				var p [8]byte
				binary.LittleEndian.PutUint16(p[:2], word)
				p[7] = 71
				i := cx01Input(t, s.CX01Spec(), 0x1853, p)
				r := CX07DecodeDTC(s, i, 0)
				f := r.Fields["selected_word"]
				if r.Status != "unknown" || f.Status != "unknown" || f.Grade != "Hypothesis" || f.Numerator != int64(word) || r.Raw != p || r.Evidence != i.Evidence || f.Raw != p || f.Meaning != "" {
					t.Fatalf("unknown substituted %#v", r)
				}
				if _, ok := r.Fields["numeric_index"]; ok {
					t.Fatal("invented index")
				}
			})
		}
	}
}

func TestCX07ZeroAndMemberDictionary(t *testing.T) {
	for _, word := range []uint16{0, 0x1100, 1185} {
		for _, modern := range []bool{false, true} {
			s := cx07Member(t, modern)
			var p [8]byte
			binary.LittleEndian.PutUint16(p[:2], word)
			r := CX07DecodeDTC(s, cx01Input(t, s.CX01Spec(), 0x1853, p), 0)
			want := "unknown"
			if word == 0 {
				want = "empty"
			}
			if word == 0x1100 && !modern || word == 1185 && modern {
				want = "accepted"
			}
			if r.Status != want || r.Raw != p {
				t.Fatalf("member table %#v", r)
			}
			if word == 0 && len(r.Fields) != 1 {
				t.Fatal("zero inferred active set")
			}
		}
	}
	// A forwarding coordinator's identity cannot supply a release-11 member table.
	member := cx07Member(t, false)
	coordinator := cx07Member(t, true)
	var p [8]byte
	binary.LittleEndian.PutUint16(p[:2], 0x1100)
	i := cx01Input(t, member.CX01Spec(), 0x1853, p)
	if CX07DecodeDTC(member, i, 0).Status != "accepted" || CX07DecodeDTC(coordinator, i, 0).Status != "unknown" {
		t.Fatal("dictionary identity disagreement lost")
	}
	for _, choice := range []string{"legacy19", ""} {
		spec := coordinator.CX01Spec()
		spec.Choice = choice
		r := CX07DecodeDTC(cx01Select(t, spec), i, 0)
		if r.Status != "unsupported" || r.Raw != p || len(r.Fields) != 0 {
			t.Fatal("legacy dictionary inferred")
		}
	}
}

func TestCX07ByteOrderAndIndependentSiblings(t *testing.T) {
	for _, flavor := range []string{"link", "pcs-standard", "pcs-parallel"} {
		s := cx07Member(t, true)
		spec := s.CX01Spec()
		spec.Flavor = flavor
		if flavor != "link" {
			spec.Direction = "bms-to-pcs"
		}
		s = cx01Select(t, spec)
		p := [8]byte{0, 0, 0, 0, 0, 0, 43, 17}
		at := 0
		if flavor == "link" {
			binary.LittleEndian.PutUint16(p[:2], 1185)
		} else {
			binary.BigEndian.PutUint16(p[:2], 1185)
		}
		i := cx01Input(t, spec, 0x1853, p)
		if flavor != "link" {
			id := uint32(0x333)
			if flavor == "pcs-parallel" {
				id = 0x520
			}
			native, _ := canbus.NewStandardID(id)
			i.Evidence.Frame, _ = canbus.NewFrame(native, p[:])
		}
		if CX07DecodeDTC(s, i, at).Fields["numeric_index"].Numerator != 131 {
			t.Fatal("byte order")
		}
		p[0], p[1] = p[1], p[0]
		i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:])
		if r := CX07DecodeDTC(s, i, 0); r.Status != "unknown" || r.Raw != p {
			t.Fatal("wrong endian matched")
		}
		if r := CX07DecodeDTC(s, i, 2); r.Status != "empty" {
			t.Fatal("sibling erased", r)
		}
	}
}

func TestCX07AdmissionIdentityFields(t *testing.T) {
	s := cx07Member(t, true)
	p := [8]byte{0xe8, 3, 4, 5, 6, 7, 8, 9}
	for _, field := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc", "family", "age", "raw-dlc"} {
		t.Run(field, func(t *testing.T) {
			i := cx01Input(t, s.CX01Spec(), 0x1853, p)
			switch field {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "coordinator-to-module"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "bank-b"
			case "endpoint":
				i.Endpoint = "endpoint-b"
			case "group":
				i.Group = "group-b"
			case "slot":
				i.Slot = "slot-b"
			case "age":
				i.FieldAge = -1
			case "destination", "source", "family":
				id := i.Evidence.Frame.ID().Value()
				if field == "destination" {
					id ^= 1 << 8
				}
				if field == "source" {
					id ^= 1
				}
				if field == "family" {
					id ^= 1 << 16
				}
				native, _ := canbus.NewExtendedID(id)
				i.Evidence.Frame, _ = canbus.NewFrame(native, p[:])
			case "ide":
				native, _ := canbus.NewStandardID(0x333)
				i.Evidence.Frame, _ = canbus.NewFrame(native, p[:])
			case "raw-dlc":
				var record [16]byte
				binary.NativeEndian.PutUint32(record[:4], i.Evidence.Frame.ID().Value()|(1<<31))
				record[4], record[7] = 8, 9
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
				i.Evidence.RawRecord = record
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
			}
			r := CX07DecodeDTC(s, i, 0)
			if r.Status != "invalid" || r.Raw != i.Evidence.Frame.Bytes() || r.Evidence != i.Evidence || len(r.Fields) != 0 {
				t.Fatalf("identity admitted %#v", r)
			}
		})
	}
	for _, offset := range []int{-1, 7, 8} {
		if r := CX07DecodeDTC(s, cx01Input(t, s.CX01Spec(), 0x1853, p), offset); r.Status != "invalid" || r.Raw != p {
			t.Fatal("offset")
		}
	}
	for _, field := range []string{"project-case", "release", "choice", "flavor", "direction", "route-set", "native-flavor"} {
		t.Run("selector/"+field, func(t *testing.T) {
			spec := s.CX01Spec()
			switch field {
			case "route-set":
				spec.RouteSet = false
			case "native-flavor":
				spec.Flavor = "pcs-extended19"
			case "project-case":
				spec.ProjectCode = "qAAa"
			case "release":
				spec.Release = "9012"
			case "choice":
				spec.Choice = "legacy19"
			case "flavor":
				spec.PCSFlavor = "pcs-extended19"
			case "direction":
				spec.Direction = "coordinator-to-module"
			}
			if field == "route-set" {
				selected, err := NewCXSelection(spec)
				if err == nil {
					t.Fatal("route flag admitted")
				}
				r := CX07DecodeDTC(selected, cx01Input(t, spec, 0x1853, p), 0)
				if r.Status != "invalid" || r.Raw != p || len(r.Fields) != 0 {
					t.Fatal("invalid selector admitted")
				}
				return
			}
			selected := cx01Select(t, spec)
			i := cx01Input(t, spec, 0x1853, p)
			r := CX07DecodeDTC(selected, i, 0)
			if r.Status == "accepted" || r.Raw != p || len(r.Fields) != 0 {
				t.Fatal("selector admitted", r)
			}
		})
	}
}

func TestCX07ExhaustiveCategoriesAndDisabledReception(t *testing.T) {
	pairs := map[[2]int][]int{
		{1, 1}: {19, 24, 30, 35, 56},
		{2, 2}: {5, 13, 17, 25, 26, 28, 29, 31, 34, 38, 42, 43, 44, 45, 47, 50, 51, 84, 87, 90, 92, 94, 95, 100, 101, 102, 103, 128, 130, 133},
		{2, 4}: {3, 9, 15, 21, 32, 53}, {3, 2}: {52, 61},
		{3, 3}: {4, 12, 16, 18, 33, 46, 57, 58, 60, 85, 91, 93, 96, 109, 110}, {3, 4}: {2, 8, 11, 14, 20},
		{4, 1}: {48, 62}, {4, 2}: {0, 1, 6, 7, 23, 39, 40, 41, 49, 63, 88, 97, 99}, {4, 3}: {22, 89, 98}, {4, 4}: {59, 83, 86, 123, 129, 131, 132, 134, 135},
	}
	expected := [136][2]int{}
	for pair, indices := range pairs {
		for _, index := range indices {
			expected[index] = pair
		}
	}
	disabled := func(i int) bool {
		return i == 10 || i == 27 || i >= 36 && i <= 37 || i >= 54 && i <= 55 || i >= 64 && i <= 82 || i >= 104 && i <= 108 || i >= 111 && i <= 122 || i >= 124 && i <= 127
	}
	for _, modern := range []bool{false, true} {
		s := cx07Member(t, modern)
		count := 134
		if modern {
			count = 136
		}
		for index := 0; index < count; index++ {
			t.Run(fmt.Sprintf("single/modern=%v/index=%d", modern, index), func(t *testing.T) {
				active := [136]bool{}
				active[index] = true
				r := CX07ExplainLocalSelection(s, active)
				pair := expected[index]
				wantA, wantB := -1, -1
				for _, c := range pair {
					if c == 1 || c == 2 {
						wantA = index
					}
					if c == 3 || c == 4 {
						wantB = index
					}
				}
				if (pair == [2]int{}) != disabled(index) {
					t.Fatal("fixture partition")
				}
				if r.Status != "accepted" || r.Grade != "Proven" || r.A != wantA || r.B != wantB || r.Active != active || r.PhysicalQualified {
					t.Fatalf("category %#v", r)
				}
				for c, got := range r.Categories {
					want := -1
					if pair[0] == c+1 || pair[1] == c+1 {
						want = index
					}
					if got != want {
						t.Fatal("membership", r)
					}
				}
				var p [8]byte
				binary.LittleEndian.PutUint16(p[:2], cx07Expected(index, modern))
				received := CX07DecodeDTC(s, cx01Input(t, s.CX01Spec(), 0x1853, p), 0)
				if received.Status != "accepted" || received.Fields["numeric_index"].Numerator != int64(index) {
					t.Fatal("disabled received code hidden")
				}
			})
		}
	}
}

func TestCX07MultiActivePriorityAndBounds(t *testing.T) {
	s := cx07Member(t, true)
	for _, v := range []struct {
		indices    []int
		categories [4]int
		a, b       int
	}{
		{[]int{19, 24, 56, 133, 4, 110, 135}, [4]int{56, 133, 110, 135}, 56, 110},
		{[]int{3}, [4]int{-1, 3, -1, 3}, 3, 3},
		{[]int{48, 62, 52, 61}, [4]int{62, 61, 61, 62}, 62, 61},
		{[]int{10, 27, 127}, [4]int{-1, -1, -1, -1}, -1, -1},
		{nil, [4]int{-1, -1, -1, -1}, -1, -1},
	} {
		active := [136]bool{}
		for _, index := range v.indices {
			active[index] = true
		}
		r := CX07ExplainLocalSelection(s, active)
		if r.Categories != v.categories || r.A != v.a || r.B != v.b {
			t.Fatal("priority", r)
		}
		wa, wb := uint16(0), uint16(0)
		if v.a >= 0 {
			wa = cx07Expected(v.a, true)
		}
		if v.b >= 0 {
			wb = cx07Expected(v.b, true)
		}
		if r.WordA != wa || r.WordB != wb {
			t.Fatal("selected words", r)
		}
	}
	for _, index := range []int{133, 134, 135} {
		active := [136]bool{}
		active[index] = true
		r := CX07ExplainLocalSelection(cx07Member(t, false), active)
		want := "invalid"
		if index == 133 {
			want = "accepted"
		}
		if r.Status != want || r.Active != active {
			t.Fatal("range", r)
		}
	}
	spec := s.CX01Spec()
	spec.Scope = "aggregate"
	if r := CX07ExplainLocalSelection(cx01Select(t, spec), [136]bool{true}); r.Status != "unsupported" || r.A != -1 || r.B != -1 {
		t.Fatal("aggregate local selection")
	}
}

func TestCX07WordLocationsAndValidSiblingRetention(t *testing.T) {
	s := cx07Member(t, true)
	for offset := 0; offset <= 6; offset++ {
		var p [8]byte
		binary.LittleEndian.PutUint16(p[offset:offset+2], 1000)
		r := CX07DecodeDTC(s, cx01Input(t, s.CX01Spec(), 0x1853, p), offset)
		if r.Status != "accepted" || r.Fields["numeric_index"].Numerator != 0 {
			t.Fatal("word location", offset, r)
		}
	}
	p := [8]byte{0xe8, 3, 0xa1, 4, 31, 32, 33, 34}
	i := cx01Input(t, s.CX01Spec(), 0x1853, p)
	previous := CX07DecodeDTC(s, i, 2)
	p[0], p[1] = 0xff, 0xff
	i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:])
	if CX07DecodeDTC(s, i, 0).Status != "unknown" {
		t.Fatal("unknown sibling")
	}
	next := CX07DecodeDTC(s, i, 2)
	if previous.Fields["numeric_index"].Numerator != 131 || next.Fields["numeric_index"].Numerator != 131 || next.Status != "accepted" || previous.Raw == next.Raw {
		t.Fatal("valid sibling lost")
	}
}

func TestCX07PCSAdmissionBoundaries(t *testing.T) {
	for _, flavor := range []string{"pcs-standard", "pcs-parallel"} {
		spec := cx07Member(t, true).CX01Spec()
		spec.Flavor = flavor
		spec.Direction = "bms-to-pcs"
		s := cx01Select(t, spec)
		p := [8]byte{3, 0xe8, 0xff, 0xff}
		for _, id := range []uint32{0x332, 0x333, 0x334, 0x51f, 0x520, 0x52f, 0x530} {
			for _, extended := range []bool{false, true} {
				i := cx01Input(t, spec, 0x1853, p)
				native, _ := canbus.NewStandardID(id)
				if extended {
					native, _ = canbus.NewExtendedID(id)
				}
				i.Evidence.Frame, _ = canbus.NewFrame(native, p[:])
				r := CX07DecodeDTC(s, i, 0)
				accepted := !extended && (flavor == "pcs-standard" && id == 0x333 || flavor == "pcs-parallel" && id >= 0x520 && id <= 0x52f)
				if (r.Status == "accepted") != accepted || r.Raw != p {
					t.Fatalf("PCS admission %s %x %v: %#v", flavor, id, extended, r)
				}
			}
		}
	}
}
