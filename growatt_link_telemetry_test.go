package canbusreg

import (
	"encoding/binary"
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func cx01Input(t *testing.T, s CX01SelectionSpec, kind uint32, p [8]byte) CX01LINKInput {
	t.Helper()
	id, e := canbus.NewExtendedID(kind<<16 | uint32(s.Destination)<<8 | uint32(s.Source))
	if e != nil {
		t.Fatal(e)
	}
	f, e := canbus.NewFrame(id, p[:])
	if e != nil {
		t.Fatal(e)
	}
	return CX01LINKInput{Evidence: Evidence{Interface: s.Interface, Frame: f, Sequence: 1, Monotonic: time.Second}, Epoch: s.Epoch, Direction: s.Direction, Scope: s.Scope, Bank: s.Bank, Endpoint: s.Endpoint, Group: s.Group, Slot: s.Slot}
}
func TestCX01PackedTelemetry(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	for _, current := range []uint64{3990, 4000, 4010} {
		var p [8]byte
		binary.LittleEndian.PutUint64(p[:], 530|current<<14|531<<27|4095<<41|2047<<53)
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1805, p))
		for name, want := range map[string]int64{"pack_voltage": 530, "current": int64(current) - 4000, "port_voltage": 531, "discharge_limit": 4095, "charge_limit": 2047} {
			f := r.Fields[name]
			if f.Numerator != want {
				t.Fatalf("%s: %#v", name, f)
			}
		}
		if r.Fields["pack_voltage"].Denominator != 10 || r.Fields["current"].Unit != "A" || r.Fields["charge_limit"].Unit != "" || r.PhysicalQualified {
			t.Fatal(r)
		}
	}
	for i, width := range []uint{14, 13, 14, 12, 11} {
		for _, value := range []uint64{0, 1, (1 << width) - 2, (1 << width) - 1} {
			var p [8]byte
			shift := []uint{0, 14, 27, 41, 53}[i]
			binary.LittleEndian.PutUint64(p[:], value<<shift)
			r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1805, p))
			names := []string{"pack_voltage", "current", "port_voltage", "discharge_limit", "charge_limit"}
			for j, name := range names {
				want := int64(0)
				if j == i {
					want = int64(value)
				}
				if j == 1 {
					want -= 4000
				}
				if r.Fields[name].Numerator != want {
					t.Fatalf("boundary %d %d %s", i, value, name)
				}
			}
		}
	}
}
func TestCX01AdmissionKeys(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc"} {
		t.Run(key, func(t *testing.T) {
			i := cx01Input(t, spec, 0x1805, p)
			switch key {
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
				i.Group = "other"
			case "slot":
				i.Slot = "other"
			case "destination":
				i = cx01Input(t, spec, 0x1805, p)
				id, _ := canbus.NewExtendedID(0x1805f5f4)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "source":
				spec2 := spec
				spec2.Source++
				i = cx01Input(t, spec2, 0x1805, p)
			case "ide":
				id, _ := canbus.NewStandardID(5)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				id, _ := canbus.NewExtendedID(0x1805f402)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:7])
			}
			r := CXDecodeLINKTelemetry(s, i)
			if len(r.Fields) != 0 || r.Status == "accepted" || r.Evidence != i.Evidence {
				t.Fatalf("key escaped: %#v", r)
			}
		})
	}
}
func TestCX01LayoutsAndRawSiblings(t *testing.T) {
	p := [8]byte{1, 1, 2, 0, 9, 10, 3, 0}
	for _, v := range []struct{ release, code, name string }{{"0005", "QAAA", "checkpoint_0"}, {"0006", "QAAA", "full_capacity"}, {"0006", "QAAB", "capacity_derived"}} {
		spec := cx01Spec(t, v.code, v.release)
		r := CXDecodeLINKTelemetry(cx01Select(t, spec), cx01Input(t, spec, 0x180b, p))
		if _, ok := r.Fields[v.name]; !ok {
			t.Fatalf("missing %s", v.name)
		}
		if r.Fields["remaining_capacity"].Numerator != 3 {
			t.Fatal("sibling lost")
		}
	}
	spec := cx01Spec(t, "QAAA", "0006")
	r := CXDecodeLINKTelemetry(cx01Select(t, spec), cx01Input(t, spec, 0x1806, [8]byte{255, 1, 2, 3, 4, 5, 6, 7}))
	if r.Fields["maximum"].Numerator != 513 || r.Fields["tail"].Numerator != 1798 {
		t.Fatal(r)
	}
	if _, ok := r.Fields["maximum_index"]; ok {
		t.Fatal("invented index")
	}
}
func cx01ShortFrame(id canbus.Identifier) (canbus.Frame, error) {
	return canbus.NewFrame(id, []byte{1})
}

func TestCX01DateHypothesisBoundaryPairs(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	spec.Destination = 0xf5
	s := cx01Select(t, spec)
	for _, v := range []struct {
		name  string
		p     [8]byte
		valid bool
	}{
		{"leap-valid", [8]byte{0xe8, 7, 2, 29, 23, 59, 59, 8}, true}, {"leap-invalid", [8]byte{0xe9, 7, 2, 29, 23, 59, 59, 8}, false},
		{"year-zero", [8]byte{0, 0, 1, 1}, false}, {"year-one", [8]byte{1, 0, 1, 1}, true},
		{"month-zero", [8]byte{0xe8, 7, 0, 1}, false}, {"month-one", [8]byte{0xe8, 7, 1, 1}, true}, {"month-twelve", [8]byte{0xe8, 7, 12, 1}, true}, {"month-thirteen", [8]byte{0xe8, 7, 13, 1}, false},
		{"day-zero", [8]byte{0xe8, 7, 1, 0}, false}, {"day-one", [8]byte{0xe8, 7, 1, 1}, true}, {"day-thirtyone", [8]byte{0xe8, 7, 1, 31}, true}, {"day-thirtytwo", [8]byte{0xe8, 7, 1, 32}, false},
		{"hour-23", [8]byte{0xe8, 7, 1, 1, 23}, true}, {"hour-24", [8]byte{0xe8, 7, 1, 1, 24}, false}, {"minute-59", [8]byte{0xe8, 7, 1, 1, 0, 59}, true}, {"minute-60", [8]byte{0xe8, 7, 1, 1, 0, 60}, false}, {"second-59", [8]byte{0xe8, 7, 1, 1, 0, 0, 59}, true}, {"second-60", [8]byte{0xe8, 7, 1, 1, 0, 0, 60}, false},
	} {
		t.Run(v.name, func(t *testing.T) {
			r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1809, v.p))
			if (r.Fields["year"].Status == "accepted") != v.valid || r.Raw != v.p || r.Fields["status"].Numerator != int64(v.p[7]) || r.PhysicalQualified {
				t.Fatal(r)
			}
		})
	}
	for _, gate := range []string{"direction", "scope", "route", "selector"} {
		t.Run(gate, func(t *testing.T) {
			other := spec
			switch gate {
			case "direction":
				other.Direction = "coordinator-to-module"
			case "scope":
				other.Scope = "aggregate"
			case "route":
				other.Destination = 0xf4
			case "selector":
				other.ProjectCode = "qAAA"
			}
			r := CXDecodeLINKTelemetry(cx01Select(t, other), cx01Input(t, other, 0x1809, [8]byte{0xe8, 7, 1, 1}))
			if r.Fields["year"].Status == "accepted" {
				t.Fatal("date admission")
			}
		})
	}
}
func TestCX01CoordinatorSeparateRole(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	spec.Direction = "coordinator-to-module"
	spec.Scope = "aggregate"
	spec.Source = 0xf4
	spec.Destination = 0xf5
	s := cx01Select(t, spec)
	for _, kind := range []uint32{0x180d, 0x180e, 0x180f} {
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, kind, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
		if r.Status != "raw" || len(r.Fields) == 0 {
			t.Fatal(r)
		}
		for _, f := range r.Fields {
			if f.Unit != "" || f.Status == "accepted" {
				t.Fatal("aggregate units")
			}
		}
	}
	r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1805, [8]byte{}))
	if len(r.Fields) != 0 {
		t.Fatal("coordinator local parsing")
	}
}
func TestCX01OpaqueHypothesesStayRaw(t *testing.T) {
	cases := []struct {
		name    string
		kind    uint32
		payload [8]byte
	}{
		{"aggregate-320-not-release", 0x320, [8]byte{0, 0, 0, 6, 0, 6}},
		{"release-branch-1900", 0x1900, [8]byte{0, 0, 0, 6}},
		{"mux-60x-high", 0x601, [8]byte{0, 0, 0, 6, 128}}, {"mux-60x-low", 0x601, [8]byte{0, 0, 0, 6}},
		{"gateway-release-one", 0x191d, [8]byte{1, 1, 0, 'Q', 'C', 'A', 'A', '0'}}, {"gateway-state-zero", 0x191d, [8]byte{0, 1, 0, 'Q', 'C', 'A', 'A', '0'}},
		{"321-no-release", 0x321, [8]byte{0, 'Q', 'A', 'A', 'A'}}, {"321-no-selector", 0x321, [8]byte{}}, {"321-tail-nonzero", 0x321, [8]byte{0, 'Q', 'A', 'A', 'A', 1}},
		{"synchronization-zero-is-not-choice", 0x180a, [8]byte{}},
	}
	spec := cx01Spec(t, "QAAA", "0006")
	for _, v := range cases {
		t.Run(v.name, func(t *testing.T) {
			r := CXDecodeLINKTelemetry(cx01Select(t, spec), cx01Input(t, spec, v.kind, v.payload))
			if r.Raw != v.payload {
				t.Fatal("raw lost")
			}
			for _, f := range r.Fields {
				if f.Status == "accepted" || f.Unit != "" || f.Meaning != "" {
					t.Fatal("inferred meaning")
				}
			}
		})
	}
	for _, source := range []uint8{0, 1, 9, 10, 244, 254, 255} {
		spec.Source = source
		r := CXDecodeLINKTelemetry(cx01Select(t, spec), cx01Input(t, spec, 0x1805, [8]byte{}))
		if source == 255 || source == 0xf4 {
			if len(r.Fields) != 0 || r.Status != "invalid" {
				t.Fatalf("reserved source %#x ordinary", source)
			}
		} else if r.Fields["pack_voltage"].Status != "accepted" {
			t.Fatalf("universal module bound %d", source)
		}
	}
	spec = cx01Spec(t, "QCAA", "0001")
	r := CXDecodeLINKTelemetry(cx01Select(t, spec), cx01Input(t, spec, 0x1805, [8]byte{1}))
	for _, f := range r.Fields {
		if f.Status == "accepted" {
			t.Fatal("gateway native group")
		}
	}
}
func TestCX01NativeWordsAndIndependentValidity(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	for _, v := range []struct {
		kind uint32
		name string
		want int64
	}{{0x1800, "soc_status", 8}, {0x1801, "mixed", 1029}, {0x1803, "capacity", 1541}, {0x1804, "word_1", 134678021}, {0x1807, "b4", 1541}, {0x1808, "temperature_minimum", -36}, {0x180a, "counter", 262914}, {0x180b, "remaining_capacity", 2055}, {0x180c, "word_2", 1541}, {0x1840, "tail", 2055}} {
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, v.kind, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
		if r.Fields[v.name].Numerator != v.want {
			t.Fatalf("%x %s %#v", v.kind, v.name, r.Fields[v.name])
		}
		if v.kind == 0x1803 && r.Fields["capacity"].Status != "accepted" {
			t.Fatal("bad constant erased capacity")
		}
	}
	for _, release := range []string{"0010", "0011"} {
		spec = cx01Spec(t, "QAAA", release)
		s = cx01Select(t, spec)
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x180a, [8]byte{0, 0, 0, 0, 0, 0, 255, 255}))
		if r.Fields["tail"].Unit != "" || r.Fields["tail"].Numerator != 65535 {
			t.Fatal("tail transformed")
		}
	}
}

func TestCX01SnapshotEvidenceAndRelease11(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0011")
	s := cx01Select(t, spec)
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	i := cx01Input(t, spec, 0x1807, p)
	i.FieldAge = 3 * time.Second
	i.Evidence.RawRecord[0] = 17
	r := CXDecodeLINKTelemetry(s, i)
	f := r.Fields["b4"]
	if f.Numerator != 1541 || f.Denominator != 10 || f.Unit != "percent" || f.Age != i.FieldAge || f.Evidence != i.Evidence || f.Raw != p || f.Epoch != 1 || f.Endpoint != spec.Endpoint || f.Scope != "local" || f.Bank != spec.Bank || f.Group != spec.Group || f.Slot != spec.Slot {
		t.Fatal(f)
	}
	i.Evidence.RawRecord[0] = 99
	p[0] = 99
	f.Raw[0] = 99
	if r.Evidence.RawRecord[0] != 17 || r.Fields["b4"].Raw[0] != 1 {
		t.Fatal("alias")
	}
	r = CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1803, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if r.Fields["configuration"].Numerator != 258 || r.Fields["capacity"].Unit != "" {
		t.Fatal("release11 configuration")
	}
	spec = cx01Spec(t, "QAAa", "9011")
	s = cx01Select(t, spec)
	for _, kind := range []uint32{0x1803, 0x1805, 0x1807, 0x1808, 0x180b} {
		r = CXDecodeLINKTelemetry(s, cx01Input(t, spec, kind, [8]byte{}))
		for _, f := range r.Fields {
			if f.Status == "accepted" || f.Unit != "" {
				t.Fatal("ambiguous units")
			}
		}
	}
}
func TestCX01RawDLCGeometry(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	i := cx01Input(t, spec, 0x1805, [8]byte{1})
	record := i.Evidence.RawRecord
	// A classical record with len8_dlc=9 must retain that native geometry.
	binary.LittleEndian.PutUint32(record[:4], 0x80000000|0x1805f402)
	record[4] = 8
	record[7] = 9
	frame, e := canbus.DecodeSocketCANRecord(record[:])
	if e != nil {
		t.Fatal(e)
	}
	i.Evidence.Frame = frame
	r := CXDecodeLINKTelemetry(s, i)
	if r.Status != "invalid" || len(r.Fields) != 0 {
		t.Fatal("raw DLC admitted")
	}
}

func TestCX01ConstantBytesIndependent(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	s := cx01Select(t, spec)
	for bad := 0; bad < 2; bad++ {
		p := [8]byte{1, 1, 0, 0, 10}
		p[bad] = 0
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x1803, p))
		names := []string{"b0", "b1"}
		if r.Fields[names[bad]].Status != "invalid" || r.Fields[names[1-bad]].Status == "invalid" || r.Fields["capacity"].Status != "accepted" {
			t.Fatal("independent sibling invalidated")
		}
	}
}

func TestCX01CoordinatorHighWordsKeepGeometry(t *testing.T) {
	spec := cx01Spec(t, "QAAA", "0006")
	spec.Direction = "coordinator-to-module"
	spec.Scope = "aggregate"
	spec.Source = 0xf4
	spec.Destination = 0xf5
	s := cx01Select(t, spec)
	for kind, name := range map[uint32]string{0x180e: "additional_limit", 0x180f: "quotient"} {
		r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, kind, [8]byte{0, 0, 0, 0, 0, 0, 1, 2}))
		if f := r.Fields[name]; f.Numerator != 513 || f.Status != "raw" || f.Unit != "" {
			t.Fatalf("%x %s %#v", kind, name, f)
		}
	}
}
