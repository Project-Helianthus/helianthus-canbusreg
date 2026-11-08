package canbusreg

import (
	"fmt"
	"github.com/Project-Helianthus/helianthus-canbus"
	"strings"
	"testing"
	"time"
)

func TestCX15ReceiverMatrix(t *testing.T) {
	s := cx01Select(t, cx01Spec(t, "QAAA", "0011"))
	c := CX15ReceiverContext{Direction: "module-to-coordinator", Role: "coordinator", Profile: "link", Assigned: true}
	i := cx01Input(t, s.spec, 0x1800, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	r := CX15ClassifyReceiver(s, c, i)
	if !r.Admitted || r.Class != "T" || r.PhysicalQualified || r.Raw != i.Evidence.Frame.Bytes() {
		t.Fatal(r)
	}
	for _, pid := range []uint32{0x1801, 0x1809, 0x1810, 0x1811, 0x1812, 0x1813, 0x1830, 0x1840} {
		t.Run("L8-omission-"+cx15PIDName(pid), func(t *testing.T) {
			r := CX15ClassifyReceiver(s, c, cx01Input(t, s.spec, pid, i.Evidence.Frame.Bytes()))
			if r.Admitted || r.Status != "unsupported" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}

func TestCX15FiniteTelemetryGroups(t *testing.T) {
	for group := 0; group < 9; group++ {
		for _, v := range []struct {
			pid  uint32
			mask uint16
		}{
			{0x1800, 0x1ff}, {0x1802, 0x1ff}, {0x1803, 0x1ff}, {0x1804, 0x1ff}, {0x1805, 0x1ff}, {0x1806, 0x1ff}, {0x1807, 0x1ff}, {0x1808, 0x1ff}, {0x180a, 0x1ff}, {0x180b, 0x1ff}, {0x180c, 0x1ff}, {0x1850, 0x1ff},
			{0x1801, 0xff}, {0x1809, 0xff}, {0x1810, 0xff}, {0x1811, 0xff}, {0x1812, 0xff}, {0x1813, 0xff}, {0x1830, 0xff}, {0x1840, 0xff}, {0x1851, 0x1fe}, {0x1852, 0x1b8}, {0x1853, 0x100}, {0x1861, 0x180}, {0x1862, 0x110}, {0x18fb, 0x1fe},
		} {
			t.Run(fmt.Sprintf("L%d-%04X", group, v.pid), func(t *testing.T) {
				s, c, i := cx15Fixture(t, group, "link", "module-to-coordinator", "coordinator", v.pid)
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted != (v.mask&(1<<group) != 0) || r.Input != i || r.Raw != i.Evidence.Frame.Bytes() || r.PhysicalQualified {
					t.Fatal(r)
				}
				c.Assigned = false
				if CX15ClassifyReceiver(s, c, i).Admitted {
					t.Fatal("unassigned module")
				}
			})
		}
	}
}

func TestCX15ServiceGroupNegatives(t *testing.T) {
	for group := 0; group < 9; group++ {
		for _, v := range []struct {
			pid  uint32
			mask uint16
		}{
			{0x1870, 0x5f}, {0x1871, 0x7f}, {0x1877, 0x7f}, {0x187b, 0x7f}, {0x1881, 0x5f}, {0x1887, 0x5f}, {0x1889, 0x5f},
			{0x188a, 0x1ff}, {0x188b, 0x5f}, {0x188c, 0x1ff}, {0x18a0, 0x1ff}, {0x18fd, 0x1ff}, {0x18df, 0x5f}, {0x1892, 0x1fe}, {0x1894, 0x5e}, {0x1899, 0x1e}, {0x18fc, 0},
		} {
			t.Run(fmt.Sprintf("L%d-service-%04X", group, v.pid), func(t *testing.T) {
				s, c, i := cx15Fixture(t, group, "link", "service-to-module", "module", v.pid)
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted != (v.mask&(1<<group) != 0) || r.Raw != i.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
				for _, gate := range []string{"direction", "role", "session", "selector", "route", "profile"} {
					t.Run(gate, func(t *testing.T) {
						ss, cc, ii := s, c, i
						switch gate {
						case "direction":
							cc.Direction = "module-to-service"
							ii.Direction = cc.Direction
						case "role":
							cc.Role = "monitor"
						case "session":
							cc.Session = "apparent-response"
						case "selector":
							spec := s.spec
							spec.ProjectCode = "qAAA"
							ss = cx01Select(t, spec)
						case "route":
							spec := s.spec
							spec.Source = 0xf4
							ss = cx01Select(t, spec)
							ii = cx15Input(t, ss, cc, v.pid, i.Evidence.Frame.Bytes())
						case "profile":
							cc.Profile = "mon"
						}
						rr := CX15ClassifyReceiver(ss, cc, ii)
						if rr.Admitted || rr.Raw != r.Raw || rr.NativeFreshnessCount != 0 {
							t.Fatal(rr)
						}
					})
				}
			})
		}
	}
}

func TestCX15CoordinatorAndSessionNegatives(t *testing.T) {
	for _, v := range []struct {
		pid  uint32
		mask uint16
	}{
		{0x1800, 0x1ff}, {0x1805, 0x1ff}, {0x180a, 0x1ff}, {0x1860, 0x1fe}, {0x18fe, 0x1fe}, {0x1869, 0x180}, {0x18b0, 0x1ff}, {0x18b1, 0x1fe},
	} {
		for group := 0; group < 9; group++ {
			t.Run(fmt.Sprintf("L%d-coordination-%04X", group, v.pid), func(t *testing.T) {
				s, c, i := cx15Fixture(t, group, "link", "coordinator-to-module", "module", v.pid)
				p := i.Evidence.Frame.Bytes()
				p[0] = 0xaa
				i = cx15Input(t, s, c, v.pid, p)
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted != (v.mask&(1<<group) != 0) || r.Raw != p {
					t.Fatal(r)
				}
				for _, gate := range []string{"opposite-direction", "wrong-role", "wrong-route"} {
					t.Run(gate, func(t *testing.T) {
						cc, ss, ii := c, s, i
						switch gate {
						case "opposite-direction":
							cc.Direction = "module-to-coordinator"
							ii.Direction = cc.Direction
						case "wrong-role":
							cc.Role = "monitor"
						case "wrong-route":
							spec := ss.spec
							spec.Destination = 0xf5
							ss = cx01Select(t, spec)
							ii = cx15Input(t, ss, cc, v.pid, p)
						}
						rr := CX15ClassifyReceiver(ss, cc, ii)
						if rr.Admitted || rr.Raw != p {
							t.Fatal(rr)
						}
					})
				}
			})
		}
	}
	for _, direction := range []string{"coordinator-to-module", "service-to-module"} {
		t.Run("18B0-distinct-"+direction, func(t *testing.T) {
			s, c, i := cx15Fixture(t, 8, "link", direction, "module", 0x18b0)
			if !CX15ClassifyReceiver(s, c, i).Admitted {
				t.Fatal("session route")
			}
			spec := s.spec
			spec.Destination ^= 1
			s = cx01Select(t, spec)
			i = cx15Input(t, s, c, 0x18b0, i.Evidence.Frame.Bytes())
			if CX15ClassifyReceiver(s, c, i).Admitted {
				t.Fatal("session route widened")
			}
		})
	}
}

func TestCX15LegacySynchronizationBoundaries(t *testing.T) {
	for _, state := range []uint8{2, 3, 4, 5} {
		for _, marker := range []byte{0xa9, 0xaa, 0xab} {
			t.Run(fmt.Sprintf("legacy1860-state%d-marker%02X", state, marker), func(t *testing.T) {
				s, c, _ := cx15Fixture(t, 4, "link", "coordinator-to-module", "module", 0x1860)
				c.CommunicationState = state
				i := cx15Input(t, s, c, 0x1860, [8]byte{marker})
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted != (marker == 0xaa && (state == 3 || state == 4)) || r.Raw != i.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
				c.CommunicationKnown = false
				if CX15ClassifyReceiver(s, c, i).Admitted {
					t.Fatal("unknown state")
				}
			})
		}
	}
	for _, identity := range []struct{ code, release string }{{"QAAA", "0010"}, {"QAAa", "4010"}, {"QAAA", "4010"}} {
		t.Run("legacy1860-unselected-variant-"+identity.code+identity.release, func(t *testing.T) {
			s, c, _ := cx15Fixture(t, 4, "link", "coordinator-to-module", "module", 0x1860)
			spec := s.spec
			spec.ProjectCode = identity.code
			spec.Release = identity.release
			s = cx01Select(t, spec)
			c.CommunicationState = 2
			i := cx15Input(t, s, c, 0x1860, [8]byte{0xaa})
			if CX15ClassifyReceiver(s, c, i).Admitted {
				t.Fatal("identity inferred earlier-state side effect")
			}
		})
	}
}

func TestCX15AddressingNoAllocation(t *testing.T) {
	for group := 0; group < 9; group++ {
		s, c, i := cx15Fixture(t, group, "link", "service-to-module", "module", 0x18f0)
		c.Session = "addressing"
		if !CX15ClassifyReceiver(s, c, i).Admitted {
			t.Fatal("matched addressing")
		}
		t.Run(fmt.Sprintf("L%d-18F0-unknown-state", group), func(t *testing.T) {
			c.AddressingKnown = false
			r := CX15ClassifyReceiver(s, c, i)
			if r.Admitted || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}

func TestCX15Legacy18FCAndOutputOnly(t *testing.T) {
	s, c, i := cx15Fixture(t, 4, "link", "service-to-module", "module", 0x18fc)
	spec := s.spec
	spec.ProjectCode = "QAAa"
	spec.Release = "9011"
	spec.Choice = "legacy19"
	s = cx01Select(t, spec)
	if !CX15ClassifyReceiver(s, c, i).Admitted {
		t.Fatal("legacy input")
	}
	for _, group := range []int{5, 7, 8} {
		t.Run(fmt.Sprintf("L%d-18FC-output-only", group), func(t *testing.T) {
			s, c, i := cx15Fixture(t, group, "link", "service-to-module", "module", 0x18fc)
			if r := CX15ClassifyReceiver(s, c, i); r.Admitted || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}

func TestCX15LegacyFreshnessIsNotSeconds(t *testing.T) {
	for _, identity := range []string{"QAAA0010", "QAAa9011"} {
		counts := map[uint32]uint32{0x1800: 5000, 0x1850: 1000, 0x1862: 5000}
		if identity == "QAAa9011" {
			counts = map[uint32]uint32{0x1800: 1000, 0x1850: 5000, 0x1862: 10000}
		}
		for pid, count := range counts {
			for _, n := range []uint32{count - 1, count} {
				t.Run(fmt.Sprintf("freshness-%s-%04X-%d", identity, pid, n), func(t *testing.T) {
					s, c, i := cx15Fixture(t, 4, "link", "module-to-coordinator", "coordinator", pid)
					spec := s.spec
					spec.ProjectCode = identity[:4]
					spec.Release = identity[4:]
					if identity == "QAAa9011" {
						spec.Choice = "legacy19"
					}
					s = cx01Select(t, spec)
					c.NativeCount = n
					r := CX15ClassifyReceiver(s, c, i)
					if !r.Admitted || r.NativeFreshnessCount != count || r.Input.FieldAge != i.FieldAge {
						t.Fatal(r)
					}
					ii := i
					ii.Endpoint = "other"
					rr := CX15ClassifyReceiver(s, c, ii)
					if rr.Admitted || rr.NativeFreshnessCount != 0 || rr.Raw != r.Raw {
						t.Fatal(rr)
					}
					modern, cc, ii := cx15Fixture(t, 8, "link", "module-to-coordinator", "coordinator", pid)
					if CX15ClassifyReceiver(modern, cc, ii).NativeFreshnessCount != 0 {
						t.Fatal("modern inherited timer")
					}
				})
			}
		}
	}
}

func cx15Fixture(t *testing.T, group int, profile, direction, role string, pid uint32) (CXSelection, CX15ReceiverContext, CX01LINKInput) {
	t.Helper()
	tokens := []string{"QAAA0002", "QAAA0005", "QAAA0006", "QAAA0007", "QAAA0010", "QAAB0004", "QAAB0002", "QAAB0006", "QAAA0011"}
	token := tokens[group]
	spec := cx01Spec(t, token[:4], token[4:])
	spec.Group = "synthetic-group"
	spec.Slot = "synthetic-slot"
	spec.Flavor = profile
	if profile != "link" {
		spec.Direction = "bms-to-pcs"
	}
	spec.RouteSet = true
	switch direction {
	case "coordinator-to-module":
		spec.Destination = 0xdf
		spec.Source = 0xf4
	case "service-to-module":
		spec.Destination = 2
		spec.Source = 0xf5
		if pid == 0x18b0 {
			spec.Destination = 0xff
		}
	case "coordinator-to-battery", "gateway-to-pcs":
		spec.Destination = 0xdf
		spec.Source = 0xf2
	case "service-to-gateway":
		spec.Destination = 0xf2
		spec.Source = 0xf5
	}
	s := cx01Select(t, spec)
	if s.group == "" {
		t.Fatal("fixture has no native selection", token, profile)
	}
	c := CX15ReceiverContext{Direction: direction, Role: role, Profile: profile, Session: "maintenance", Assigned: true, CommunicationKnown: true, CommunicationState: 3, AddressingKnown: true, Form: "periodic", LocalCount: 2, LocalCountKnown: true}
	payload := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if pid == 0x6f0 {
		payload = [8]byte{1, 2}
	}
	i := cx15Input(t, s, c, pid, payload)
	return s, c, i
}
func cx15Input(t *testing.T, s CXSelection, c CX15ReceiverContext, pid uint32, p [8]byte) CX01LINKInput {
	t.Helper()
	var id canbus.Identifier
	var err error
	if c.Profile == "link" || c.Profile == "pcs-extended19" {
		id, err = canbus.NewExtendedID(pid<<16 | uint32(s.spec.Destination)<<8 | uint32(s.spec.Source))
	} else {
		id, err = canbus.NewStandardID(pid)
	}
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(id, p[:])
	if err != nil {
		t.Fatal(err)
	}
	spec := s.spec
	return CX01LINKInput{Evidence: Evidence{Interface: spec.Interface, Frame: frame, Sequence: 1, Monotonic: time.Second, RawRecord: [16]byte{9, 8, 7}}, Epoch: spec.Epoch, Direction: c.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: 2 * time.Second}
}

func TestCX15PCSAndMaintenanceNegativeVectors(t *testing.T) {
	for _, v := range []struct {
		name, profile, direction, role string
		group                          int
		pid                            uint32
	}{
		{"211-calendar", "pcs-standard", "pcs-to-bms", "battery", 4, 0x211},
		{"212-noop-not-324-response", "pcs-standard", "pcs-to-bms", "battery", 4, 0x212},
		{"301-heartbeat-not-extended19", "pcs-standard", "pcs-to-bms", "battery", 8, 0x301},
		{"6F0-parallel-discovery-control", "pcs-parallel", "controller-to-participant", "participant", 8, 0x6f0},
		{"6FF-count-peer", "pcs-parallel", "participant-to-coordinator", "coordinator", 8, 0x6ff},
		{"190A-profile-state", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x190a},
		{"190B-calendar", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x190b},
		{"190C-noop", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x190c},
		{"191A-setters", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x191a},
		{"191C-setters", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x191c},
		{"191E-update", "pcs-extended19", "coordinator-to-battery", "battery", 4, 0x191e},
		{"1919-local-service", "pcs-extended19", "service-to-gateway", "gateway", 4, 0x1919},
		{"1918-local-fragments", "pcs-extended19", "service-to-gateway", "gateway", 4, 0x1918},
		{"782-stack-service", "pcs-standard", "service-to-battery", "battery", 8, 0x782},
		{"728-stack-response", "pcs-standard", "battery-to-service", "service", 8, 0x728},
		{"7F8-parallel-service", "pcs-parallel", "service-to-participant", "participant", 8, 0x7f8},
		{"78F-parallel-response", "pcs-parallel", "participant-to-service", "service", 8, 0x78f},
	} {
		t.Run(v.name, func(t *testing.T) {
			s, c, i := cx15Fixture(t, v.group, v.profile, v.direction, v.role, v.pid)
			if v.pid == 0x6f0 || v.pid == 0x6ff {
				c.Session = "parallel"
			}
			if r := CX15ClassifyReceiver(s, c, i); !r.Admitted || r.PhysicalQualified || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
			for _, gate := range []string{"opposite-direction", "wrong-role", "native-MON-overlap", "unselected-contract", "wrong-IDE"} {
				t.Run(gate, func(t *testing.T) {
					ss, cc, ii := s, c, i
					switch gate {
					case "opposite-direction":
						cc.Direction = "opposite"
						ii.Direction = cc.Direction
					case "wrong-role":
						cc.Role = "unassociated"
					case "native-MON-overlap":
						cc.Profile = "mon"
					case "unselected-contract":
						spec := s.spec
						spec.ProjectCode = "qAAA"
						ss = cx01Select(t, spec)
					case "wrong-IDE":
						var id canbus.Identifier
						if i.Evidence.Frame.ID().Extended() {
							id, _ = canbus.NewStandardID(0x301)
						} else {
							id, _ = canbus.NewExtendedID(v.pid)
						}
						p := i.Evidence.Frame.Bytes()
						ii.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
					}
					rr := CX15ClassifyReceiver(ss, cc, ii)
					if rr.Admitted || rr.Raw != i.Evidence.Frame.Bytes() {
						t.Fatal(rr)
					}
				})
			}
			if v.pid == 0x6f0 || v.pid == 0x6ff || v.pid >= 0x700 || v.profile == "pcs-extended19" {
				t.Run("unestablished-session", func(t *testing.T) {
					c.Session = "apparent-response"
					if CX15ClassifyReceiver(s, c, i).Admitted {
						t.Fatal("session inferred")
					}
				})
			}
		})
	}
	for _, pid := range []uint32{0x190d, 0x190e, 0x190f, 0x191b} {
		t.Run("battery-output-not-input-"+cx15PIDName(pid), func(t *testing.T) {
			s, c, i := cx15Fixture(t, 4, "pcs-extended19", "coordinator-to-battery", "battery", pid)
			if CX15ClassifyReceiver(s, c, i).Admitted {
				t.Fatal("inferred input")
			}
		})
	}
	s, c, i := cx15Fixture(t, 8, "pcs-standard", "pcs-to-bms", "battery", 0x212)
	if CX15ClassifyReceiver(s, c, i).Admitted {
		t.Fatal("release11 212 absent")
	}
	for _, pid := range []uint32{0x190c, 0x190d, 0x190e, 0x190f, 0x191b, 0x191c} {
		s, c, i := cx15Fixture(t, 4, "pcs-extended19", "gateway-to-pcs", "pcs", pid)
		if r := CX15ClassifyReceiver(s, c, i); !r.Admitted || r.Class != "A" {
			t.Fatal(r)
		}
	}
}

func TestCX15MaintenanceResponseAmbiguity(t *testing.T) {
	for _, pid := range []uint32{0x1881, 0x1887, 0x188b, 0x188c} {
		for _, length := range []int{0, 8} {
			t.Run(fmt.Sprintf("%04X-empty-or-response-%d", pid, length), func(t *testing.T) {
				s, c, i := cx15Fixture(t, 4, "link", "service-to-module", "module", pid)
				p := i.Evidence.Frame.Bytes()
				i.Evidence.Frame, _ = canbus.NewFrame(i.Evidence.Frame.ID(), p[:length])
				c.Session = "apparent-response"
				r := CX15ClassifyReceiver(s, c, i)
				if r.Admitted || r.Class != "Q" || r.Raw != i.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestCX15Release11HeartbeatAndState(t *testing.T) {
	for _, token := range []string{"QAAA0011", "QAAa9011"} {
		for _, b7 := range []byte{0, 0x57, 0x58, 0x59} {
			t.Run(fmt.Sprintf("heartbeat-%s-%02X", token, b7), func(t *testing.T) {
				s, c, _ := cx15Fixture(t, 8, "pcs-standard", "pcs-to-bms", "battery", 0x301)
				spec := s.spec
				spec.ProjectCode = token[:4]
				spec.Release = token[4:]
				spec.Choice = "release11"
				s = cx01Select(t, spec)
				i := cx15Input(t, s, c, 0x301, [8]byte{0, 1, 2, 3, 4, 5, 6, b7})
				r := CX15ClassifyReceiver(s, c, i)
				want := "B7 retained"
				if token == "QAAA0011" {
					want = "B7 ignored"
				} else if b7 == 0 || b7 == 0x58 {
					want = "B7 normalizes to zero"
				}
				if !r.Admitted || !strings.Contains(r.Description, want) || r.Raw[7] != b7 || r.Grade != "Proven" {
					t.Fatal(r)
				}
			})
		}
		for _, count := range []uint8{0, 1, 8, 9} {
			for _, b7 := range []byte{0, 1, 2} {
				t.Run(fmt.Sprintf("37F-%s-count%d-B7%d", token, count, b7), func(t *testing.T) {
					s, c, _ := cx15Fixture(t, 8, "pcs-standard", "controller-to-bms", "battery", 0x37f)
					spec := s.spec
					spec.ProjectCode = token[:4]
					spec.Release = token[4:]
					spec.Choice = "release11"
					s = cx01Select(t, spec)
					c.LocalCount = count
					i := cx15Input(t, s, c, 0x37f, [8]byte{1, 2, 3, 4, 5, 6, 7, b7})
					r := CX15ClassifyReceiver(s, c, i)
					want := count >= 1 && count <= 8 && ((token == "QAAA0011" && b7 != 1) || (token == "QAAa9011" && b7 > 1))
					if strings.Contains(r.Description, "native count branch") != want {
						t.Fatal(r)
					}
					c.LocalCountKnown = false
					if strings.Contains(CX15ClassifyReceiver(s, c, i).Description, "native count branch") {
						t.Fatal("unknown count")
					}
					c.LocalCountKnown = true
					i = cx15Input(t, s, c, 0x37f, [8]byte{0, 2, 3, 4, 5, 6, 7, b7})
					if strings.Contains(CX15ClassifyReceiver(s, c, i).Description, "native count branch") {
						t.Fatal("bit0 absent")
					}
				})
			}
		}
	}
	for _, form := range []string{"periodic", "direct", "unknown"} {
		t.Run("3F7-"+form, func(t *testing.T) {
			s, c, i := cx15Fixture(t, 8, "pcs-standard", "bms-to-controller", "controller", 0x3f7)
			c.Form = form
			r := CX15ClassifyReceiver(s, c, i)
			if r.Admitted != (form != "unknown") || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
			if form == "direct" && !strings.Contains(r.Description, "cannot clear alarms") {
				t.Fatal(r)
			}
		})
	}
	for _, pid := range []uint32{0x1869, 0x1861, 0x510, 0x51f, 0x590} {
		for _, token := range []string{"QAAA0011", "QAAa9011"} {
			t.Run(fmt.Sprintf("B7-%s-%04X", token, pid), func(t *testing.T) {
				profile, direction, role := "link", "coordinator-to-module", "module"
				if pid == 0x1861 {
					direction = "module-to-coordinator"
					role = "coordinator"
				}
				if pid < 0x1000 {
					profile = "pcs-parallel"
					direction = "participant-to-coordinator"
					role = "coordinator"
				}
				s, c, i := cx15Fixture(t, 8, profile, direction, role, pid)
				spec := s.spec
				spec.ProjectCode = token[:4]
				spec.Release = token[4:]
				spec.Choice = "release11"
				s = cx01Select(t, spec)
				want := "B7 ignored"
				if token == "QAAa9011" {
					want = "B7 retained"
				}
				if r := CX15ClassifyReceiver(s, c, i); !r.Admitted || !strings.Contains(r.Description, want) {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestCX15ExtendedRouteMustMatchSelection(t *testing.T) {
	for _, v := range []struct {
		direction, role string
		pid             uint32
	}{
		{"coordinator-to-battery", "battery", 0x190a},
		{"gateway-to-pcs", "pcs", 0x190d},
		{"service-to-gateway", "gateway", 0x1919},
	} {
		for _, route := range [][2]uint8{{0xde, 0xf2}, {0xdf, 0xf3}, {0xf2, 0xf5}, {0xf3, 0xf5}, {0xf2, 0xf4}} {
			t.Run(fmt.Sprintf("%s-%02X-%02X", v.direction, route[0], route[1]), func(t *testing.T) {
				s, c, i := cx15Fixture(t, 4, "pcs-extended19", v.direction, v.role, v.pid)
				if !CX15ClassifyReceiver(s, c, i).Admitted {
					t.Fatal("matching selection not admitted")
				}
				if route[0] == s.spec.Destination && route[1] == s.spec.Source {
					return
				}
				spec := s.CX01Spec()
				spec.Destination, spec.Source = route[0], route[1]
				other := cx01Select(t, spec)
				if other.group == "" {
					t.Skip("route not selectable")
				}
				if r := CX15ClassifyReceiver(other, c, i); r.Admitted {
					t.Fatal("frame admitted for different selected route", r)
				}
			})
		}
	}
}
