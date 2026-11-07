package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
)

func cx01Spec(t *testing.T, code, release string) CX01SelectionSpec {
	t.Helper()
	bus, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	return CX01SelectionSpec{Interface: bus, Flavor: "link", PCSFlavor: "pcs-standard", ProjectCode: code, Release: release, Direction: "module-to-coordinator", Scope: "local", Bank: "bank-a", Endpoint: "endpoint-a", RouteSet: true, Destination: 0xf4, Source: 2, Epoch: 1}
}
func cx01Select(t *testing.T, spec CX01SelectionSpec) CXSelection {
	t.Helper()
	s, e := NewCXSelection(spec)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestCX01SelectorGroups(t *testing.T) {
	groups := []struct{ code, release, group, layout string }{
		{"QAaT", "8252", "L0", "C"}, {"QAAA", "0002", "L0", "C"}, {"QAAA", "0005", "L1", "C"}, {"QAAa", "2005", "L1", "C"}, {"QAAa", "3005", "L1", "C"}, {"QAAa", "5005", "L1", "C"}, {"QAAa", "6005", "L1", "C"}, {"QAAa", "9005", "L1", "C"}, {"QAAa", "9006", "L1", "C"},
		{"QAAA", "0006", "L2", "W"}, {"QAAA", "0007", "L3", "W"}, {"QAAA", "0008", "L3", "W"}, {"QAAa", "2007", "L3", "W"}, {"QAAa", "6008", "L3", "W"}, {"QAAa", "6009", "L3", "W"}, {"QAAa", "7004", "L3", "W"}, {"QAAa", "8008", "L3", "W"},
		{"QAAA", "0009", "L4", "W"}, {"QAAA", "0010", "L4", "W"}, {"QAAa", "1011", "L4", "W"}, {"QAAa", "4010", "L4", "W"}, {"QAAa", "4011", "L4", "W"}, {"QAAa", "9010", "L4", "W"},
		{"QAAb", "5004", "L5", "B"}, {"QAAB", "0004", "L5", "B"}, {"QAAB", "0002", "L6", "W"}, {"QAAB", "0003", "L6", "W"}, {"QAAB", "0006", "L7", "B"}, {"QAAA", "0011", "L8", "W"},
	}
	for _, v := range groups {
		t.Run(v.code+v.release, func(t *testing.T) {
			s := cx01Select(t, cx01Spec(t, v.code, v.release))
			if s.CX01Group() != v.group || s.CX01Layout() != v.layout {
				t.Fatalf("selection: %s %s", s.CX01Group(), s.CX01Layout())
			}
		})
	}
}
func TestCX01HypothesisSelectorNegatives(t *testing.T) {
	for _, v := range []struct{ code, release, hole, group string }{{"QAaT", "8252", "8253", "L0"}, {"QAAa", "2005", "2006", "L1"}, {"QAAa", "2007", "2008", "L3"}, {"QAAa", "1011", "1012", "L4"}, {"QAAb", "5004", "5005", "L5"}, {"QAAB", "0002", "0001", "L6"}, {"QAAB", "0006", "0005", "L7"}} {
		t.Run(v.group, func(t *testing.T) {
			for _, gate := range []string{"case", "hole", "flavor", "direction"} {
				t.Run(gate, func(t *testing.T) {
					spec := cx01Spec(t, v.code, v.release)
					switch gate {
					case "case":
						spec.ProjectCode = "q" + spec.ProjectCode[1:]
					case "hole":
						spec.Release = v.hole
					case "flavor":
						spec.PCSFlavor = "link"
					case "direction":
						spec.Direction = "coordinator-to-module"
					}
					s := cx01Select(t, spec)
					r := CXDecodeLINKTelemetry(s, cx01Input(t, spec, 0x180b, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
					if s.CX01Group() != "" || len(r.Fields) != 0 || r.Raw != [8]byte{1, 2, 3, 4, 5, 6, 7, 8} || r.Fields["full_capacity"].Status == "accepted" {
						t.Fatalf("admitted %#v", r)
					}
				})
			}
		})
	}
}
func TestCX01AmbiguousChoiceAndImmutability(t *testing.T) {
	spec := cx01Spec(t, "QAAa", "9011")
	s := cx01Select(t, spec)
	if s.CX01Group() != "" || s.CX01Layout() != "W" {
		t.Fatal("ambiguity")
	}
	spec.Choice = "release11"
	s = cx01Select(t, spec)
	spec.ProjectCode = "QAAA"
	copy := s.CX01Spec()
	copy.Release = "0006"
	if s.CX01Group() != "L8" || s.CX01Spec().Release != "9011" {
		t.Fatal("mutable")
	}
	spec = s.CX01Spec()
	spec.PCSFlavor = "pcs-extended19"
	if cx01Select(t, spec).CX01Group() != "" {
		t.Fatal("L8 extended19")
	}
	spec.Choice = "legacy19"
	if cx01Select(t, spec).CX01Group() != "L4" {
		t.Fatal("legacy")
	}
}

func TestCX01FlavorMatrix(t *testing.T) {
	for _, v := range []struct {
		code, release string
		allowed       []string
	}{{"QAaT", "8252", []string{"pcs-standard"}}, {"QAAA", "0005", []string{"pcs-standard", "pcs-extended19"}}, {"QAAA", "0006", []string{"pcs-standard", "pcs-extended19"}}, {"QAAA", "0007", []string{"pcs-standard", "pcs-extended19"}}, {"QAAA", "0009", []string{"pcs-standard", "pcs-extended19"}}, {"QAAB", "0004", []string{"pcs-standard", "pcs-extended19", "pcs-parallel"}}, {"QAAB", "0002", []string{"pcs-standard", "pcs-extended19", "pcs-parallel"}}, {"QAAB", "0006", []string{"pcs-standard", "pcs-parallel"}}, {"QAAA", "0011", []string{"pcs-standard", "pcs-parallel"}}} {
		for _, flavor := range []string{"pcs-standard", "pcs-extended19", "pcs-parallel"} {
			spec := cx01Spec(t, v.code, v.release)
			spec.PCSFlavor = flavor
			want := false
			for _, a := range v.allowed {
				want = want || a == flavor
			}
			if (cx01Select(t, spec).CX01Group() != "") != want {
				t.Fatalf("%s%s %s", v.code, v.release, flavor)
			}
			spec.Flavor = flavor
			spec.Direction = "bms-to-pcs"
			if (cx01Select(t, spec).CX01Group() != "") != want {
				t.Fatal("PCS selector parity")
			}
		}
	}
}
func TestCX01NativeIndicationNeverChooses(t *testing.T) {
	for _, choice := range []string{"", "legacy19", "release11"} {
		spec := cx01Spec(t, "QAAa", "9011")
		spec.Choice = choice
		s := cx01Select(t, spec)
		for _, id := range []uint32{0x18ffF202, 0x1900F202, 0x1909F202, 0x190aF202, 0x326, 0x327, 0x333, 0x334} {
			for _, associated := range []bool{false, true} {
				i := cx01Input(t, spec, 0x1805, [8]byte{})
				if id <= 0x7ff {
					native, _ := canbus.NewStandardID(id)
					i.Evidence.Frame, _ = canbus.NewFrame(native, make([]byte, 8))
				} else {
					native, _ := canbus.NewExtendedID(id)
					i.Evidence.Frame, _ = canbus.NewFrame(native, make([]byte, 8))
				}
				i.Direction = "bms-to-pcs"
				r := CX01ConfirmNativeSelection(s, CX01NativeIndication{i, associated})
				want := "unsupported"
				if choice != "" && associated && id != 0x18ffF202 && id != 0x190aF202 {
					contract := "release11"
					if id > 0x7ff {
						contract = "legacy19"
					}
					if choice == contract {
						want = "confirmed"
					} else {
						want = "conflict"
					}
				}
				if r.Status != want || r.Selection.CX01Spec().Choice != choice {
					t.Fatalf("indication %x %s %v: %s", id, choice, associated, r.Status)
				}
			}
		}
		for _, kind := range []uint32{0x1805, 0x1853, 0x1861, 0x1869} {
			i := cx01Input(t, spec, kind, [8]byte{})
			r := CX01ConfirmNativeSelection(s, CX01NativeIndication{Input: i})
			want := "unsupported"
			if kind != 0x1805 && kind != 0x1869 && choice != "" {
				want = "confirmed"
				if choice == "legacy19" {
					want = "conflict"
				}
			}
			if r.Status != want {
				t.Fatal("LINK evidence")
			}
		}
	}
}
func TestCX01Release11ReportRoute(t *testing.T) {
	for _, choice := range []string{"legacy19", "release11"} {
		spec := cx01Spec(t, "QAAa", "9011")
		spec.Choice = choice
		spec.Destination = 0xf5
		s := cx01Select(t, spec)
		i := cx01Input(t, spec, 0x1869, [8]byte{})
		want := "confirmed"
		if choice == "legacy19" {
			want = "conflict"
		}
		if r := CX01ConfirmNativeSelection(s, CX01NativeIndication{Input: i}); r.Status != want {
			t.Fatalf("1869 F5 %s: %s", choice, r.Status)
		}
		spec.Destination = 0xf3
		s = cx01Select(t, spec)
		i = cx01Input(t, spec, 0x1869, [8]byte{})
		if r := CX01ConfirmNativeSelection(s, CX01NativeIndication{Input: i}); r.Status != "unsupported" {
			t.Fatalf("1869 other route: %s", r.Status)
		}
	}
}
func TestCX01InvalidSelection(t *testing.T) {
	for _, gate := range []string{"code-length", "release-length", "ascii", "release-digit", "choice", "scope", "bank", "endpoint", "epoch", "route"} {
		t.Run(gate, func(t *testing.T) {
			s := cx01Spec(t, "QAAA", "0006")
			switch gate {
			case "code-length":
				s.ProjectCode = "QAA"
			case "release-length":
				s.Release = "006"
			case "ascii":
				s.ProjectCode = "QA\x00A"
			case "release-digit":
				s.Release = "000x"
			case "choice":
				s.Choice = "guessed"
			case "scope":
				s.Scope = ""
			case "bank":
				s.Bank = ""
			case "endpoint":
				s.Endpoint = ""
			case "epoch":
				s.Epoch = 0
			case "route":
				s.RouteSet = false
			}
			if _, e := NewCXSelection(s); e == nil {
				t.Fatal("invalid selection")
			}
		})
	}
}

func TestCX01FeatureScopeAndReleaseBoundaries(t *testing.T) {
	for _, v := range []struct{ code, release, below, above, feature, grade string }{
		{"QAAA", "0006", "0005", "0007", "three-word-pcs-gauge", "Proven"},
		{"QAAA", "0011", "0010", "0012", "slots", "Proven"},
		{"QAAA", "0011", "0010", "0012", "dtc-extensions", "Proven"},
		{"QAAb", "5004", "5003", "5005", "reduced-configuration", "Hypothesis"},
		{"QAAB", "0002", "0001", "0004", "broad-configuration", "Hypothesis"},
	} {
		t.Run(v.feature, func(t *testing.T) {
			spec := cx01Spec(t, v.code, v.release)
			s := cx01Select(t, spec)
			if s.CX01FeatureGrade(v.feature) != v.grade {
				t.Fatal("feature")
			}
			for _, release := range []string{v.below, v.above} {
				other := spec
				other.Release = release
				if cx01Select(t, other).CX01FeatureGrade(v.feature) != "Unknown" {
					t.Fatal("numeric ordering")
				}
			}
			for _, gate := range []string{"case", "direction", "flavor"} {
				other := spec
				switch gate {
				case "case":
					other.ProjectCode = "q" + other.ProjectCode[1:]
				case "direction":
					other.Direction = "coordinator-to-module"
				case "flavor":
					other.PCSFlavor = "link"
				}
				if cx01Select(t, other).CX01FeatureGrade(v.feature) != "Unknown" {
					t.Fatal("feature gate")
				}
			}
		})
	}
}
