package canbusreg

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func cx13Standard(t *testing.T, value uint32, p [8]byte) canbus.Frame {
	t.Helper()
	id, err := canbus.NewStandardID(value)
	if err != nil {
		t.Fatal(err)
	}
	f, err := canbus.NewFrame(id, p[:])
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCX13FaultBlocks(t *testing.T) {
	profiles := []struct {
		code, release string
		blocks        int
	}{
		{"QAAA", "0002", 1}, {"QAAA", "0005", 2}, {"QAAA", "0006", 2},
		{"QAAA", "0007", 3}, {"QAAA", "0009", 3}, {"QAAB", "0004", 3},
		{"QAAB", "0002", 2}, {"QAAB", "0006", 3}, {"QAAA", "0011", 3},
	}
	for _, v := range profiles {
		s := cx01Select(t, cx01Spec(t, v.code, v.release))
		for block := 0; block < 3; block++ {
			for bit := 0; bit < 64; bit++ {
				t.Run(fmt.Sprintf("%s%s/block%d/bit%d", v.code, v.release, block, bit), func(t *testing.T) {
					var p [8]byte
					p[bit/8] = 1 << uint(bit%8)
					i := cx01Input(t, s.CX01Spec(), uint32(0x1850+block), p)
					r := CX13DecodeLINKDiagnostics(s, i)
					eligible := block < v.blocks
					if !eligible {
						if r.Status != "unsupported" || len(r.Fields) != 0 || r.Raw != p {
							t.Fatal(r)
						}
						return
					}
					if r.Status != "accepted" || len(r.Fields) != 64 {
						t.Fatal(r)
					}
					for b := 0; b < 64; b++ {
						f := r.Fields[fmt.Sprintf("current_fault_%d", block*64+b)]
						want := int64(0)
						if b == bit {
							want = 1
						}
						if f.Numerator != want || f.Denominator != 1 || f.Meaning != "current fault bit" || f.Raw != p || f.Evidence != i.Evidence || f.PhysicalQualified {
							t.Fatal(b, f)
						}
					}
				})
			}
		}
	}
}

func TestCX13ThirdBlockHypothesisNegatives(t *testing.T) {
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, v := range []struct{ code, release string }{{"QAAA", "0002"}, {"QAAA", "0005"}, {"QAAA", "0006"}, {"QAAB", "0002"}, {"QAAA", "0012"}, {"qAAA", "0011"}} {
		t.Run(v.code+v.release, func(t *testing.T) {
			s := cx01Select(t, cx01Spec(t, v.code, v.release))
			i := cx01Input(t, s.CX01Spec(), 0x1852, p)
			r := CX13DecodeLINKDiagnostics(s, i)
			if len(r.Fields) != 0 || r.Raw != p || r.Status != "unsupported" {
				t.Fatal(r)
			}
		})
	}
}

func TestCX13SelectedWordsStateAndReservedSiblings(t *testing.T) {
	s := cx07Member(t, true)
	p := [8]byte{0xa1, 4, 0, 0, 1, 0, 0, 0}
	r := CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1853, p))
	if r.Fields["selected_a"].Numerator != 1185 || r.Fields["selected_b"].Status != "empty" || r.Fields["reserved_4"].Status != "invalid" {
		t.Fatal(r)
	}
	for _, n := range []byte{254, 255} {
		p = [8]byte{1, 2, 3, n, 0, 0, 0, 7}
		r = CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1861, p))
		if r.Fields["counter"].Numerator != int64(n) || r.Fields["counter"].Unit != "" || r.Fields["additional_state"].Numerator != 7 {
			t.Fatal(r)
		}
	}
	p = [8]byte{}
	r = CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1853, p))
	if r.Fields["selected_a"].Status != "empty" || r.Fields["selected_b"].Status != "empty" {
		t.Fatal(r)
	}
	for name := range r.Fields {
		if name == "current_fault_0" || name == "retained_fault_0" {
			t.Fatal("selected zero inferred bitmap")
		}
	}
	// No frame-to-frame store: a rejected sibling cannot mutate a prior result.
	first := CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1850, [8]byte{1}))
	_ = CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1853, p))
	if first.Fields["current_fault_0"].Numerator != 1 {
		t.Fatal("prior observation erased")
	}
}

func TestCX13MonitorCoordinatorAndByteSeven(t *testing.T) {
	for _, v := range []struct{ code, release, choice string }{{"QAAA", "0011", ""}, {"QAAa", "9011", "release11"}, {"QAAB", "0006", ""}} {
		for _, route := range []string{"module", "monitor", "coordinator"} {
			t.Run(v.code+v.release+"/"+route, func(t *testing.T) {
				c := cx01Spec(t, v.code, v.release)
				c.Choice = v.choice
				kind := uint32(0x1861)
				if route == "monitor" {
					c.Destination = 0xf5
					kind = 0x1869
				}
				if route == "coordinator" {
					c.Direction = "coordinator-to-module"
					c.Scope = "aggregate"
					c.Source = 0xf4
					c.Destination = 0xdf
					kind = 0x1869
				}
				s := cx01Select(t, c)
				p := [8]byte{1, 2, 3, 0x34, 0x12, 0x78, 0x56, 9}
				i := cx01Input(t, c, kind, p)
				r := CX13DecodeLINKDiagnostics(s, i)
				prefix := ""
				if route == "coordinator" {
					prefix = "coordinator_"
				}
				if r.Status != "accepted" || r.Fields[prefix+"state_0"].Numerator != 1 {
					t.Fatal(r)
				}
				if route != "module" && (r.Fields[prefix+"word_3"].Numerator != 0x1234 || r.Fields[prefix+"word_5"].Numerator != 0x5678) {
					t.Fatal(r)
				}
				f := r.Fields[prefix+"additional_state"]
				if v.code == "QAAA" {
					if f.Status != "ignored" || f.Meaning != "" {
						t.Fatal(f)
					}
				} else if f.Numerator != 9 || f.Status != "raw" {
					t.Fatal(f)
				}
				if r.Fields[prefix+"state_0"].Meaning != "" || r.PhysicalQualified {
					t.Fatal(r)
				}
			})
		}
	}
	for _, kind := range []uint32{0x1869, 0x0301, 0x0318} {
		s := cx07Member(t, true)
		i := cx01Input(t, s.CX01Spec(), kind, [8]byte{1})
		r := CX13DecodeLINKDiagnostics(s, i)
		if len(r.Fields) != 0 {
			t.Fatal("wrong route/heartbeat/retained interpreted", r)
		}
	}
}

func TestCX13CountsHypothesisNegativesAndBoundaries(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s := cx07Member(t, modern)
		for _, n := range []byte{0, 1, 254, 255} {
			p := [8]byte{0, 0, n, n}
			i := cx01Input(t, s.CX01Spec(), 0x1807, p)
			r := CX13DecodeLINKDiagnostics(s, i)
			for _, name := range []string{"count_b2", "count_b3"} {
				f := r.Fields[name]
				if f.Numerator != int64(n) || f.Grade != "Hypothesis" || f.Unit != "count" {
					t.Fatal(r)
				}
			}
		}
		for _, field := range []string{"count_b2", "count_b3"} {
			for _, gate := range []string{"profile", "scope"} {
				t.Run(fmt.Sprintf("%v/%s/%s", modern, field, gate), func(t *testing.T) {
					selected := s
					c := s.CX01Spec()
					p := [8]byte{0, 0, 255, 0}
					i := cx01Input(t, c, 0x1807, p)
					if gate == "profile" {
						c.Release = "0010"
						c.ProjectCode = "QAAA"
						c.Choice = ""
						selected = cx01Select(t, c)
					} else {
						i.Scope = "aggregate"
					}
					r := CX13DecodeLINKDiagnostics(selected, i)
					if r.Raw != p || r.Fields[field].Meaning != "" {
						t.Fatal(r)
					}
				})
			}
		}
	}
}

func TestCX13AdmissionEveryKey(t *testing.T) {
	s := cx07Member(t, true)
	c := s.CX01Spec()
	p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "destination", "source", "ide", "dlc", "raw-dlc", "age", "sequence", "monotonic", "route-set", "flavor", "pcs-flavor", "project-case", "release", "choice"} {
		t.Run(key, func(t *testing.T) {
			i := cx01Input(t, c, 0x1852, p)
			selected := s
			switch key {
			case "interface-name":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity("different", c.Interface.Index())
			case "interface-index":
				i.Evidence.Interface, _ = canbus.NewInterfaceIdentity(c.Interface.Name(), 2)
			case "epoch":
				i.Epoch++
			case "direction":
				i.Direction = "coordinator-to-module"
			case "scope":
				i.Scope = "aggregate"
			case "bank":
				i.Bank = "other"
			case "endpoint":
				i.Endpoint = "other"
			case "group":
				i.Group = "other"
			case "slot":
				i.Slot = "other"
			case "destination", "source":
				other := c
				if key == "destination" {
					other.Destination++
				} else {
					other.Source++
				}
				i = cx01Input(t, other, 0x1852, p)
			case "ide":
				id, _ := canbus.NewStandardID(0x502)
				i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
			case "dlc":
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:7])
			case "raw-dlc":
				record := i.Evidence.RawRecord
				binary.LittleEndian.PutUint32(record[:4], 0x80000000|i.Evidence.Frame.ID().Value())
				record[4], record[7] = 8, 9
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
				i.Evidence.RawRecord = record
			case "age":
				i.FieldAge = -1
			case "sequence":
				i.Evidence.Sequence = 0
			case "monotonic":
				i.Evidence.Monotonic = -1
			default:
				other := c
				switch key {
				case "route-set":
					other.RouteSet = false
				case "flavor":
					other.Flavor = "pcs-standard"
				case "pcs-flavor":
					other.PCSFlavor = "unknown"
				case "project-case":
					other.ProjectCode = "qAAa"
				case "release":
					other.Release = "9012"
				case "choice":
					other.Choice = ""
				}
				selected, _ = NewCXSelection(other)
			}
			r := CX13DecodeLINKDiagnostics(selected, i)
			if r.Status == "accepted" || len(r.Fields) != 0 || r.Evidence != i.Evidence || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}

func TestCX13CountsModuloAndUnknownContributors(t *testing.T) {
	for _, modern := range []bool{false, true} {
		s := cx07Member(t, modern)
		for _, byteOffset := range []int{2, 3} {
			for _, total := range []uint16{255, 256} {
				t.Run(fmt.Sprintf("modern%v/B%d/total%d", modern, byteOffset, total), func(t *testing.T) {
					var p [8]byte
					p[byteOffset] = byte(total)
					r := CX13DecodeLINKDiagnostics(s, cx01Input(t, s.CX01Spec(), 0x1807, p))
					f := r.Fields[fmt.Sprintf("count_b%d", byteOffset)]
					if f.Numerator != int64(total%256) || f.Meaning != "two category counters summed modulo 256; contributors unknown" || len(r.Fields) != 2 {
						t.Fatal(r)
					}
				})
			}
		}
	}
}

func TestCX13DiagnosticMetadataAndByteOrder(t *testing.T) {
	s := cx07Member(t, true)
	for _, kind := range []uint32{0x1850, 0x1851, 0x1852, 0x1853, 0x1861, 0x1807} {
		p := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
		i := cx01Input(t, s.CX01Spec(), kind, p)
		i.FieldAge = 23
		i.Evidence.Sequence = 19
		i.Evidence.Monotonic = 123
		i.Evidence.RawRecord[15] = 91
		r := CX13DecodeLINKDiagnostics(s, i)
		if r.Selection != s || r.Epoch != i.Epoch || r.Evidence != i.Evidence || r.Raw != p || r.PhysicalQualified {
			t.Fatal(r)
		}
		for name, f := range r.Fields {
			if f.Raw != p || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot || f.Age != i.FieldAge || f.PhysicalQualified {
				t.Fatal(name, f)
			}
		}
		if kind == 0x1853 && (r.Fields["selected_a"].Numerator != 0x0201 || r.Fields["selected_b"].Numerator != 0x0403) {
			t.Fatal("BE substituted for LE", r)
		}
	}
}

func TestCX13StateAndDTCFeatureGates(t *testing.T) {
	for _, v := range []struct{ code, release, choice string }{{"QAAA", "0006", ""}, {"QAAB", "0002", ""}, {"QAAa", "9011", "legacy19"}, {"QAAa", "9011", ""}, {"QAAA", "0012", ""}} {
		for _, kind := range []uint32{0x1853, 0x1861, 0x1869} {
			t.Run(fmt.Sprintf("%s%s/%s/%x", v.code, v.release, v.choice, kind), func(t *testing.T) {
				c := cx01Spec(t, v.code, v.release)
				c.Choice = v.choice
				if kind == 0x1869 {
					c.Destination = 0xf5
				}
				s := cx01Select(t, c)
				i := cx01Input(t, c, kind, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
				r := CX13DecodeLINKDiagnostics(s, i)
				if r.Status == "accepted" || len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
			})
		}
	}
}
