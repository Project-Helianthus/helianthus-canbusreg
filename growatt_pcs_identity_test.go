package canbusreg

import (
	"github.com/Project-Helianthus/helianthus-canbus"
	"testing"
	"time"
)

func TestCX06Auxiliary(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0011", 0x320, [8]byte{1, 2, 3, 4, 5, 6, 7, 0})
	r := CX06DecodePCSIdentity(s, i)
	cx06Want(t, r, "version_3", "accepted", 1284, 1, "")
	cx06Want(t, r, "version_5", "accepted", 1798, 1, "")
	if r.Selection != s {
		t.Fatal("product selected profile")
	}
	s, i = cx06Fixture(t, "QAAA0011", 0x321, [8]byte{0, 'A', 'B', 'C', 'D', 0, 1, 2})
	r = CX06DecodePCSIdentity(s, i)
	cx06Want(t, r, "family", "accepted", 0x41424344, 1, "")
	cx06Want(t, r, "tail", "accepted", 258, 1, "")
	for _, identity := range []string{"QAaT8252", "QAAA0002", "QAAA0005", "QAAA0006", "QAAA0007", "QAAa7004", "QAAa8008", "QAAA0008", "QAAA0009", "QAAA0010", "QAAA0011", "QAAa1011", "QAAa4010", "QAAa4011", "QAAa6008", "QAAa6009", "QAAa9010", "QAAa9011", "QAAb5004", "QAAB0002", "QAAB0003", "QAAB0004", "QAAB0006"} {
		t.Run("omitted_"+identity, func(t *testing.T) {
			s, i := cx06Fixture(t, identity, 0x321, [8]byte{0, 0, 0, 0, 0, 0, 1, 2})
			r := CX06DecodePCSIdentity(s, i)
			cx06Want(t, r, "family_omitted", "accepted", 0, 1, "")
			cx06Want(t, r, "family", "raw", 0, 1, "")
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.ProjectCode = "ZZZZ" })
			r = CX06DecodePCSIdentity(s, i)
			cx06Want(t, r, "family_omitted", "raw", 0, 1, "")
		})
	}
	for _, threshold := range []uint16{0, 50, 51, 52} {
		t.Run("threshold_boundary_"+string(rune('a'+threshold)), func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0011", 0x323, [8]byte{7, byte(threshold >> 8), byte(threshold), 2, 9, 8, 7, 6})
			r := CX06DecodePCSIdentity(s, i)
			want := int64(threshold) - 50
			if threshold < 51 {
				want = 3600
			}
			cx06Want(t, r, "threshold", "accepted", want, 1, "mV")
			cx06Want(t, r, "count", "accepted", 7, 1, "")
			for _, negative := range []string{"state_missing", "alternative", "scope", "selector", "validity"} {
				t.Run(negative, func(t *testing.T) {
					ss, ii := s, i
					switch negative {
					case "state_missing":
						ii.ThresholdStateKnown = false
					case "alternative":
						ii.AlternativeEligible = true
					case "scope":
						ss, ii.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
					case "selector":
						ss, ii.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Release = "0006" })
					case "validity":
						ii.MeasurementValid = false
					}
					rr := CX06DecodePCSIdentity(ss, ii)
					if rr.Fields["threshold"].Status == "accepted" || rr.Raw != r.Raw {
						t.Fatal(rr)
					}
				})
			}
		})
	}
	s, i = cx06Fixture(t, "QCAA0001", 0x323, [8]byte{3, 0, 52, 2, 0, 0, 0, 0})
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
	cx06Want(t, CX06DecodePCSIdentity(s, i), "count", "accepted", 515, 1, "")
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.ProjectCode = "QAAA"; c.Release = "0011" })
	cx06Want(t, CX06DecodePCSIdentity(s, i), "count", "accepted", 3, 1, "")
}
func TestCX06PlaceholderNegativeVectors(t *testing.T) {
	for _, pid := range []uint32{0x315, 0x316, 0x317, 0x318, 0x321, 0x324, 0x325} {
		t.Run(string(rune(pid)), func(t *testing.T) {
			s, i := cx06Fixture(t, "QCAA0001", pid, [8]byte{})
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
			decode := CX06DecodePCSGauge
			if pid >= 0x320 {
				decode = CX06DecodePCSIdentity
			}
			cx06Want(t, decode(s, i), "placeholder", "accepted", 0, 1, "")
			s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "local" })
			r := decode(s, i)
			if r.Fields["placeholder"].Status == "accepted" || r.Raw != [8]byte{} {
				t.Fatal(r)
			}
		})
	}
	for _, identity := range []string{"QAaT8252", "QAAA0002"} {
		s, i := cx06Fixture(t, identity, 0x324, [8]byte{})
		cx06Want(t, CX06DecodePCSIdentity(s, i), "placeholder", "accepted", 0, 1, "")
		s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
		if CX06DecodePCSIdentity(s, i).Fields["placeholder"].Status == "accepted" {
			t.Fatal("scope")
		}
	}
	s, i := cx06Fixture(t, "QAAB0004", 0x315, [8]byte{})
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Flavor = "pcs-parallel" })
	cx06Want(t, CX06DecodePCSGauge(s, i), "placeholder", "accepted", 0, 1, "")
	s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Flavor = "pcs-standard" })
	if CX06DecodePCSGauge(s, i).Fields["placeholder"].Status == "accepted" {
		t.Fatal("flavor")
	}
}
func cx06Fragment(t *testing.T, s CXSelection, i CX06PCSInput, part byte) CX06PCSInput {
	t.Helper()
	p := [8]byte{part, 7, 8, 9, 10, 11, 12, 13}
	if part == 0 {
		p[1] = i.SerialSlot
	}
	id, _ := canbus.NewStandardID(0x324)
	i.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	i.Evidence.Sequence = uint64(part) + 10
	i.Evidence.Monotonic = time.Duration(part+1) * time.Second
	return i
}
func TestCX06SerialAssembly(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0011", 0x324, [8]byte{})
	var a CX06SerialAssembler
	for part := byte(0); part < 3; part++ {
		r := a.Consume(s, cx06Fragment(t, s, i, part))
		if (r.Status == "complete") != (part == 2) {
			t.Fatal(r)
		}
		if part == 2 {
			want := [16]byte{8, 9, 10, 11, 12, 13, 7, 8, 9, 10, 11, 12, 13, 7, 8, 9}
			if r.Serial != want || r.PhysicalQualified {
				t.Fatal(r)
			}
			for at, record := range r.Fragments {
				if record.Evidence.Sequence != uint64(at+10) || record.Raw != cx06Fragment(t, s, i, byte(at)).Evidence.Frame.Bytes() {
					t.Fatal("lost fragment")
				}
			}
			detached := r
			a.Consume(s, cx06Fragment(t, s, i, 0))
			if r.Serial != detached.Serial || r.Fragments[0].Raw != detached.Fragments[0].Raw {
				t.Fatal("attached snapshot")
			}
		}
	}
}
func TestCX06SerialOrdering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []byte
	}{{"orphan", []byte{1, 2}}, {"reorder", []byte{0, 2, 1}}, {"duplicate", []byte{0, 1, 1, 2}}, {"duplicate_conflict", []byte{0, 0, 1, 2}}, {"interleaving", []byte{0, 9, 1, 2}}} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0011", 0x324, [8]byte{})
			var a CX06SerialAssembler
			for at, part := range tc.parts {
				in := cx06Fragment(t, s, i, part)
				in.Evidence.Sequence = uint64(at + 10)
				in.Evidence.Monotonic = time.Duration(at+1) * time.Second
				if tc.name == "duplicate_conflict" && at == 1 {
					p := in.Evidence.Frame.Bytes()
					p[2]++
					id, _ := canbus.NewStandardID(0x324)
					in.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
				}
				r := a.Consume(s, in)
				if r.Status == "complete" || r.Record.Raw != in.Evidence.Frame.Bytes() {
					t.Fatal(r)
				}
			}
		})
	}
}
func TestCX06SerialIdentityKey(t *testing.T) {
	// Each row catches removal of its corresponding full-selection equality gate.
	for _, key := range []string{"interface_name", "interface_index", "flavor", "project", "release", "choice", "scope", "bank", "endpoint", "group", "slot", "epoch", "direction", "pcs_flavor", "route_set", "destination", "source", "serial_slot", "slot_valid", "sequence", "monotonic"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0011", 0x324, [8]byte{})
			var a CX06SerialAssembler
			a.Consume(s, cx06Fragment(t, s, i, 0))
			c := s.CX01Spec()
			switch key {
			case "interface_name":
				c.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface_index":
				c.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "flavor":
				c.Flavor = "pcs-parallel"
			case "project":
				c.ProjectCode = "QAAa"
			case "release":
				c.Release = "0010"
			case "choice":
				c.Choice = "release11"
			case "scope":
				c.Scope = "aggregate"
			case "bank":
				c.Bank = "other"
			case "endpoint":
				c.Endpoint = "other"
			case "group":
				c.Group = "other"
			case "slot":
				c.Slot = "other"
			case "epoch":
				c.Epoch++
			case "direction":
				c.Direction = "other"
			case "pcs_flavor":
				c.PCSFlavor = "pcs-standard"
			case "route_set":
				c.RouteSet = true
			case "destination":
				c.Destination = 1
			case "source":
				c.Source = 1
			case "serial_slot":
				i.SerialSlot++
			case "slot_valid":
				i.SerialSlotValid = false
			}
			ss, err := NewCXSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			i.Evidence.Interface = c.Interface
			i.Direction = c.Direction
			i.Scope = c.Scope
			i.Bank = c.Bank
			i.Endpoint = c.Endpoint
			i.Group = c.Group
			i.Slot = c.Slot
			i.Epoch = c.Epoch
			in := cx06Fragment(t, ss, i, 1)
			if key == "sequence" {
				in.Evidence.Sequence++
			}
			if key == "monotonic" {
				in.Evidence.Monotonic = 0
			}
			r := a.Consume(ss, in)
			if r.Status == "complete" || r.Status == "pending" {
				t.Fatal("key admitted", r)
			}
			r = a.Consume(ss, cx06Fragment(t, ss, i, 2))
			if r.Status == "complete" {
				t.Fatal("identity change completed")
			}
		})
	}
}

func TestCX06FamilyValidityAndOmissionAmbiguity(t *testing.T) {
	for _, p := range [][8]byte{{1, 'A', 'B', 'C', 'D', 0, 1, 2}, {0, 'A', 'B', 'C', 'D', 1, 1, 2}, {0, 'A', 0, 'C', 'D', 0, 1, 2}, {0, 0, 0, 0, 0, 0, 1, 2}} {
		s, i := cx06Fixture(t, "QAAA0011", 0x321, p)
		r := CX06DecodePCSIdentity(s, i)
		if r.Fields["family"].Status == "accepted" {
			t.Fatal(r)
		}
		cx06Want(t, r, "tail", "accepted", 258, 1, "")
	}
	s, i := cx06Fixture(t, "QAAA0006", 0x321, [8]byte{0, 'A', 'B', 'C', 'D', 0, 1, 2})
	cx06Want(t, CX06DecodePCSIdentity(s, i), "family", "raw", 0x41424344, 1, "")
	s, i = cx06Fixture(t, "QAAa2007", 0x321, [8]byte{})
	if CX06DecodePCSIdentity(s, i).Fields["family_omitted"].Status == "accepted" {
		t.Fatal("unlisted omitted family selector")
	}
}
func TestCX06GatewayCountSelectorNegativeAndBoundaries(t *testing.T) {
	for _, high := range []byte{0, 1, 255} {
		s, i := cx06Fixture(t, "QCAA0001", 0x323, [8]byte{255, 0, 52, high, 0, 0, 0, 0})
		s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
		cx06Want(t, CX06DecodePCSIdentity(s, i), "count", "accepted", 255+256*int64(high), 1, "")
		s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "local" })
		cx06Want(t, CX06DecodePCSIdentity(s, i), "count", "raw", 255, 1, "")
	}
}
func TestCX06NonzeroPlaceholderAndNoHistory(t *testing.T) {
	for _, pid := range []uint32{0x315, 0x316, 0x317, 0x318, 0x321, 0x324, 0x325} {
		s, i := cx06Fixture(t, "QCAA0001", pid, [8]byte{0, 0, 0, 0, 0, 0, 0, 1})
		s, i.CX05PCSInput = cx05Reselect(t, s, i.CX05PCSInput, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
		decode := CX06DecodePCSGauge
		if pid >= 0x320 {
			decode = CX06DecodePCSIdentity
		}
		r := decode(s, i)
		if r.Fields["placeholder"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal(r)
		}
	}
	s, i := cx06Fixture(t, "QAAA0011", 0x325, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	r := CX06DecodePCSIdentity(s, i)
	if len(r.Fields) != 8 {
		t.Fatal(r)
	}
	for _, f := range r.Fields {
		if f.Status != "raw" {
			t.Fatal(r)
		}
	}
}
func TestCX06SerialSlotAndDetachedEvidence(t *testing.T) {
	s, i := cx06Fixture(t, "QAAA0011", 0x324, [8]byte{})
	var a CX06SerialAssembler
	in := cx06Fragment(t, s, i, 0)
	p := in.Evidence.Frame.Bytes()
	p[1] = 1
	id, _ := canbus.NewStandardID(0x324)
	in.Evidence.Frame, _ = canbus.NewFrame(id, p[:])
	if a.Consume(s, in).Status == "pending" {
		t.Fatal("wrong anchor slot")
	}
	first := a.Consume(s, cx06Fragment(t, s, i, 0))
	first.Record.Fields["byte_2"] = CXNativeField{Status: "corrupted"}
	a.Consume(s, cx06Fragment(t, s, i, 1))
	r := a.Consume(s, cx06Fragment(t, s, i, 2))
	if r.Status != "complete" {
		t.Fatal(r)
	}
	for _, f := range r.Fragments {
		v := f.Fields["serial_fragment"]
		if v.Age != i.FieldAge || v.Status != "accepted" || v.PhysicalQualified || v.Bank != i.Bank || v.Endpoint != i.Endpoint || v.Group != i.Group || v.Slot != i.Slot || v.Scope != i.Scope || v.Raw != f.Raw {
			t.Fatal(v)
		}
	}
	// Completion returns a new identity; no earlier telemetry object is relabelled.
	ts, ti := cx06Fixture(t, "QAAA0011", 0x315, [8]byte{0, 1, 0, 2, 0, 3, 0, 4})
	telemetry := CX06DecodePCSGauge(ts, ti)
	a.Consume(s, cx06Fragment(t, s, i, 0))
	if telemetry.Selection != ts || telemetry.Raw != ti.Evidence.Frame.Bytes() {
		t.Fatal(telemetry)
	}
}

func TestCX06SerialRejectedFieldAndInterleavedPID(t *testing.T) {
	for _, key := range []string{"stale_fragment", "negative_age", "interleaved_pid"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx06Fixture(t, "QAAA0011", 0x324, [8]byte{})
			var a CX06SerialAssembler
			a.Consume(s, cx06Fragment(t, s, i, 0))
			in := cx06Fragment(t, s, i, 1)
			switch key {
			case "stale_fragment":
				in.FieldStates = map[string]string{"serial_fragment": "stale"}
			case "negative_age":
				in.FieldAges = map[string]time.Duration{"serial_fragment": -1}
			case "interleaved_pid":
				id, _ := canbus.NewStandardID(0x315)
				in.Evidence.Frame, _ = canbus.NewFrame(id, in.Evidence.Frame.Data())
			}
			a.Consume(s, in)
			r := a.Consume(s, cx06Fragment(t, s, i, 2))
			if r.Status == "complete" {
				t.Fatal(r)
			}
		})
	}
}
