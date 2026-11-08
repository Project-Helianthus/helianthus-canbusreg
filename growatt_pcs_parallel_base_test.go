package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func cx10Spec(t *testing.T) CX01SelectionSpec {
	t.Helper()
	bus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return CX01SelectionSpec{Interface: bus, Flavor: "pcs-parallel", ProjectCode: "QAAB", Release: "0004", Direction: "bms-to-pcs", Scope: "local", Bank: "bank", Endpoint: "endpoint", Group: "3", Epoch: 1}
}
func cx10Selection(t *testing.T, c CX01SelectionSpec) CXSelection {
	t.Helper()
	s, e := NewCXSelection(c)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func cx10Input(t *testing.T, c CX01SelectionSpec, id uint32, p [8]byte) CX01LINKInput {
	t.Helper()
	native, e := canbus.NewStandardID(id)
	if e != nil {
		t.Fatal(e)
	}
	f, e := canbus.NewFrame(native, p[:])
	if e != nil {
		t.Fatal(e)
	}
	return CX01LINKInput{Evidence: Evidence{Interface: c.Interface, Frame: f, Sequence: 1, Monotonic: time.Second}, Epoch: c.Epoch, Direction: c.Direction, Scope: c.Scope, Bank: c.Bank, Endpoint: c.Endpoint, Group: c.Group, Slot: c.Slot, FieldAge: time.Millisecond}
}
func TestCX10BaseLayout(t *testing.T) {
	c := cx10Spec(t)
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x603, p))
	if r.Fields["product"].Numerator != 513 || r.Fields["version_mux"].Numerator != 1284 || r.Raw != p || r.PhysicalQualified {
		t.Fatalf("layout: %#v", r)
	}
}

func TestCX10AdmissionKeyNegatives(t *testing.T) {
	c := cx10Spec(t)
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	s := cx10Selection(t, c)
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "age", "ide", "dlc"} {
		t.Run(key, func(t *testing.T) {
			i := cx10Input(t, c, 0x603, p)
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "pcs-to-bms"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "other"
			case "endpoint":
				i.Endpoint = "other"
			case "group":
				i.Group = "4"
			case "slot":
				i.Slot = "3"
			case "age":
				i.FieldAge = -1
			case "ide":
				id, _ := canbus.NewExtendedID(0x603)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				id, _ := canbus.NewStandardID(0x603)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:7])
			}
			r := CX10DecodeParallel(s, i)
			if r.Status != "invalid" || len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatalf("admitted: %#v", r)
			}
		})
	}
}
func TestCX10HypothesisContextNegatives(t *testing.T) {
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, gate := range []string{"group7", "group8", "group0", "group-alias", "profile", "direction", "scope-cached-member", "scope-group", "scope-coordinator", "address-slot", "unknown70X"} {
		t.Run(gate, func(t *testing.T) {
			c := cx10Spec(t)
			id := uint32(0x603)
			valid := false
			switch gate {
			case "group7":
				c.Group = "7"
				id = 0x607
				valid = true
			case "group8":
				c.Group = "8"
				id = 0x608
			case "group0":
				c.Group = "0"
				id = 0x600
				valid = true
			case "group-alias":
				c.Group = "03"
			case "profile":
				c.Flavor = "pcs-standard"
			case "direction":
				c.Direction = "pcs-to-bms"
			case "scope-cached-member":
				c.Scope = "cached-member"
			case "scope-group":
				c.Scope = "group"
			case "scope-coordinator":
				c.Scope = "coordinator"
			case "address-slot":
				c.Slot = "4"
				id = 0x604
			case "unknown70X":
				id = 0x703
			}
			s, e := NewCXSelection(c)
			if e != nil && valid {
				t.Fatal(e)
			}
			r := CX10DecodeParallel(s, cx10Input(t, c, id, p))
			if r.Raw != p || r.PhysicalQualified || (len(r.Fields) > 0) != valid {
				t.Fatalf("gate: %#v", r)
			}
		})
	}
}
func TestCX10FamilyAvailability(t *testing.T) {
	for _, v := range []struct {
		code, release string
		added         bool
		newest        bool
		release11     bool
	}{{"QAAB", "0002", false, false, false}, {"QAAB", "0003", false, false, false}, {"QAAB", "0004", true, false, false}, {"QAAb", "5004", true, false, false}, {"QAAB", "0006", true, true, false}, {"QAAA", "0011", true, true, true}} {
		t.Run(v.code+v.release, func(t *testing.T) {
			c := cx10Spec(t)
			c.ProjectCode = v.code
			c.Release = v.release
			for _, family := range []uint32{0x500, 0x510, 0x520, 0x600, 0x610, 0x620, 0x630, 0x640, 0x650, 0x660, 0x670, 0x680, 0x6c0, 0x6d0, 0x590, 0x690, 0x6a0, 0x6b0, 0x6e0, 0x700, 0x37f, 0x3f7} {
				id := family | 3
				cc := c
				if family == 0x590 || family == 0x690 || family == 0x6a0 || family == 0x6b0 || family == 0x6e0 {
					id = family
					cc.Scope = "aggregate"
				}
				want := true
				switch family {
				case 0x500, 0x6d0, 0x6e0:
					want = v.added
				case 0x510, 0x590:
					want = v.newest
				case 0x520:
					want = v.release11
				case 0x700:
					want = false
				case 0x37f, 0x3f7:
					want = v.release11
				}
				r := CX10DecodeParallel(cx10Selection(t, cc), cx10Input(t, cc, id, [8]byte{}))
				if (r.Status != "unsupported") != want {
					t.Fatalf("%x: %s", family, r.Status)
				}
			}
		})
	}
}
func TestCX10BaseFamiliesAndEvidence(t *testing.T) {
	p := [8]byte{1, 0, 3, 4, 5, 0, 7, 8}
	c := cx10Spec(t)
	for _, v := range []struct {
		id   uint32
		name string
		want int64
		unit string
	}{{0x503, "fault_16", 1, ""}, {0x663, "fault_0", 1, ""}, {0x673, "fault_8", 1, ""}, {0x603, "hardware", 3, ""}, {0x613, "current", 1027, ""}, {0x623, "state", 7, ""}, {0x633, "quantity_6", 2055, ""}, {0x643, "maximum_mv", 1027, "mV"}, {0x653, "soh", 8, ""}} {
		t.Run(v.name, func(t *testing.T) {
			i := cx10Input(t, c, v.id, p)
			r := CX10DecodeParallel(cx10Selection(t, c), i)
			f, ok := r.Fields[v.name]
			if !ok || f.Numerator != v.want || f.Denominator != 1 || f.Unit != v.unit || f.Raw != p || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.Age != i.FieldAge || f.PhysicalQualified || r.PhysicalQualified {
				t.Fatalf("field: %#v", f)
			}
		})
	}
}
func TestCX10CoordinatorLayoutsAndScopeNegative(t *testing.T) {
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	c := cx10Spec(t)
	for _, id := range []uint32{0x690, 0x6a0, 0x6b0, 0x6e0} {
		r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, id, p))
		if len(r.Fields) != 0 || r.Raw != p {
			t.Fatal("coordinator leaked into local scope")
		}
	}
	c.Scope = "aggregate"
	r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x6e0, p))
	if len(r.Fields) != 6 || r.Fields["word_2"].Numerator != 1027 || r.Fields["state_5"].Numerator != 6 {
		t.Fatal(r)
	}
	r = CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x6a0, p))
	if len(r.Fields) != 4 || r.Fields["word_4"].Numerator != 1541 {
		t.Fatal(r)
	}
	r = CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x690, p))
	if len(r.Fields) != 8 {
		t.Fatal(r)
	}
	for _, v := range []struct {
		code, release string
		count         int
	}{{"QAAB", "0002", 1}, {"QAAB", "0003", 1}, {"QAAB", "0004", 4}, {"QAAb", "5004", 5}, {"QAAB", "0006", 5}, {"QAAA", "0011", 5}} {
		c.ProjectCode = v.code
		c.Release = v.release
		r = CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x6b0, p))
		if len(r.Fields) != v.count {
			t.Fatal(r)
		}
	}
}
func TestCX10MuxAndUnitGateWithheld(t *testing.T) {
	for _, release := range []string{"0002", "0003", "0004", "0006"} {
		c := cx10Spec(t)
		c.Release = release
		for _, mux := range []byte{0, 1, 127, 128, 254, 255} {
			p := [8]byte{1, mux, 3, 4, 5, 6, 7, 8}
			r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x613, p))
			for _, name := range []string{"voltage_mux", "current", "port", "soc"} {
				f := r.Fields[name]
				if f.Status != "raw" || f.Unit != "" || f.Meaning != "" || f.Raw != p {
					t.Fatal("invented mux or scale", f)
				}
			}
		}
	}
}
func TestCX10InvalidSiblingAndAggregateZero(t *testing.T) {
	c := cx10Spec(t)
	p := [8]byte{1, 1, 3, 4, 5, 0, 7, 8}
	for _, id := range []uint32{0x643, 0x653} {
		if id == 0x653 {
			p[4] = 0
		}
		r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, id, p))
		if r.Fields["maximum_index"].Status != "invalid" || r.Fields["minimum_index"].Status != "accepted" {
			t.Fatal(r)
		}
	}
	c.Scope = "aggregate"
	p = [8]byte{255, 255, 3, 4, 5, 6, 7, 8}
	r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x603, p))
	if r.Fields["product"].Status != "invalid" || r.Fields["hardware"].Status != "accepted" {
		t.Fatal(r)
	}
	r = CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, 0x623, [8]byte{}))
	if r.Fields["limit_0"].Numerator != 0 || r.Fields["limit_0"].Status != "raw" {
		t.Fatal(r)
	}
}

func TestCX10RawDLCNegative(t *testing.T) {
	c := cx10Spec(t)
	i := cx10Input(t, c, 0x603, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	record := i.Evidence.RawRecord
	binary.LittleEndian.PutUint32(record[:4], 0x603)
	record[4] = 8
	record[7] = 9
	p := i.Evidence.Frame.Bytes()
	copy(record[8:], p[:])
	frame, e := canbus.DecodeSocketCANRecord(record[:])
	if e != nil {
		t.Fatal(e)
	}
	i.Evidence.Frame = frame
	i.Evidence.RawRecord = record
	r := CX10DecodeParallel(cx10Selection(t, c), i)
	if r.Status != "invalid" || len(r.Fields) != 0 || r.Raw != p || r.Evidence.RawRecord != record {
		t.Fatal(r)
	}
}
func TestCX10UnitGateNegative(t *testing.T) {
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, identity := range []struct{ code, release string }{{"QAAB", "0004"}, {"QAAA", "0011"}} {
		c := cx10Spec(t)
		c.ProjectCode = identity.code
		c.Release = identity.release
		for _, id := range []uint32{0x613, 0x623, 0x633} {
			r := CX10DecodeParallel(cx10Selection(t, c), cx10Input(t, c, id, p))
			for name, f := range r.Fields {
				if f.Unit != "" || f.Raw != p {
					t.Fatalf("unprovided refinement %s: %#v", name, f)
				}
			}
		}
	}
}
