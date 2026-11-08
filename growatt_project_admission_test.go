package canbusreg

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func cx16Fixture(t *testing.T) (CXSelection, CX16ReplayInput) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewCXSelection(CX01SelectionSpec{Interface: iface, Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs", Scope: "local", Bank: "standard", Endpoint: "pack", Group: "0", Slot: "1", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := canbus.NewStandardID(0x313)
	f, _ := canbus.NewFrame(id, []byte{0x14, 0xbe, 0xff, 0xf6, 0, 10, 50, 0xe4})
	i := CX01LINKInput{Evidence: Evidence{Frame: f, Interface: iface, Sequence: 1, Monotonic: time.Second}, Epoch: 1, Direction: "bms-to-pcs", Scope: "local", Bank: "standard", Endpoint: "pack", Group: "0", Slot: "1", FieldAge: time.Second}
	return s, CX16ReplayInput{Selection: s, PCS: CX08PCSInput{CX06PCSInput: CX06PCSInput{CX05PCSInput: CX05PCSInput{CX01LINKInput: i, PublisherResolved: true, MeasurementValid: true, TemperatureValid: true}}}}
}

func cx16Profile(t *testing.T, s CXSelection, inputs ...CX16ReplayInput) Profile {
	t.Helper()
	p, err := NewCX16SelectedProfile(s, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cx16Classify(t *testing.T, p Profile, e Evidence) CX16Projection {
	t.Helper()
	c := NewRegistry(p).Classify(e)
	r, ok := c.Projection.(CX16Projection)
	if !ok || c.Profile != "growatt.project.selected" {
		t.Fatalf("classification: %#v", c)
	}
	if r.Current.PhysicalQualified {
		t.Fatal("physical qualification")
	}
	return r
}

func TestCX16SelectedCore(t *testing.T) {
	s, i := cx16Fixture(t)
	r := cx16Classify(t, cx16Profile(t, s, i), i.PCS.Evidence)
	f := r.Current.Fields["core/voltage"]
	if f.Status != "accepted" || f.Numerator != 5310 || f.Denominator != 100 || f.Unit != "V" || f.Evidence != i.PCS.Evidence || f.Raw != i.PCS.Evidence.Frame.Bytes() || f.Age != time.Second || f.PhysicalQualified {
		t.Fatalf("field: %#v", f)
	}
}

func cx16Frame(t *testing.T, i *CX16ReplayInput, id uint32, extended bool, payload [8]byte) {
	t.Helper()
	var identifier canbus.Identifier
	var err error
	if extended {
		identifier, err = canbus.NewExtendedID(id)
	} else {
		identifier, err = canbus.NewStandardID(id)
	}
	if err != nil {
		t.Fatal(err)
	}
	i.PCS.Evidence.Frame, err = canbus.NewFrame(identifier, payload[:])
	if err != nil {
		t.Fatal(err)
	}
}

func cx16Reselect(t *testing.T, i *CX16ReplayInput, spec CX01SelectionSpec) CXSelection {
	t.Helper()
	s, err := NewCXSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	i.Selection = s
	in := &i.PCS.CX01LINKInput
	in.Evidence.Interface = spec.Interface
	in.Epoch, in.Direction, in.Scope, in.Bank, in.Endpoint, in.Group, in.Slot = spec.Epoch, spec.Direction, spec.Scope, spec.Bank, spec.Endpoint, spec.Group, spec.Slot
	return s
}

func cx16LINK(t *testing.T, kind uint32, payload [8]byte) (CXSelection, CX16ReplayInput) {
	t.Helper()
	s, i := cx16Fixture(t)
	c := s.CX01Spec()
	c.Flavor, c.PCSFlavor, c.Direction = "link", "pcs-standard", "module-to-coordinator"
	c.RouteSet, c.Destination, c.Source = true, 0xf4, 1
	s = cx16Reselect(t, &i, c)
	cx16Frame(t, &i, kind<<16|0xf401, true, payload)
	return s, i
}

func cx16Withheld(t *testing.T, r CX16Projection, i CX16ReplayInput) {
	t.Helper()
	if r.Current.Raw != i.PCS.Evidence.Frame.Bytes() || r.Current.Evidence != i.PCS.Evidence || len(r.Diagnostics) == 0 || r.Diagnostics[len(r.Diagnostics)-1].Input != i.PCS.CX01LINKInput {
		t.Fatalf("raw/context lost: %#v", r)
	}
	for name, field := range r.Current.Fields {
		if field.Status == "accepted" || field.Unit != "" || field.Meaning != "" {
			t.Fatalf("interpretation %s: %#v", name, field)
		}
	}
}

func TestCX16EverySelectionFieldNegative(t *testing.T) {
	mutations := map[string]func(*CX01SelectionSpec){
		"interface-name":  func(c *CX01SelectionSpec) { c.Interface, _ = canbus.NewInterfaceIdentity("other", 1) },
		"interface-index": func(c *CX01SelectionSpec) { c.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2) },
		"flavor":          func(c *CX01SelectionSpec) { c.Flavor = "pcs-parallel" },
		"pcs-flavor":      func(c *CX01SelectionSpec) { c.PCSFlavor = "pcs-extended19" },
		"project-case":    func(c *CX01SelectionSpec) { c.ProjectCode = "QAAa" },
		"release":         func(c *CX01SelectionSpec) { c.Release = "0011" },
		"choice":          func(c *CX01SelectionSpec) { c.Choice = "legacy19" },
		"direction":       func(c *CX01SelectionSpec) { c.Direction = "coordinator-to-module" },
		"scope":           func(c *CX01SelectionSpec) { c.Scope = "aggregate" },
		"bank":            func(c *CX01SelectionSpec) { c.Bank = "parallel" },
		"endpoint":        func(c *CX01SelectionSpec) { c.Endpoint = "other" },
		"group":           func(c *CX01SelectionSpec) { c.Group = "1" },
		"slot":            func(c *CX01SelectionSpec) { c.Slot = "2" },
		"route-set":       func(c *CX01SelectionSpec) { c.RouteSet = true },
		"destination":     func(c *CX01SelectionSpec) { c.Destination = 0xf5 },
		"source":          func(c *CX01SelectionSpec) { c.Source = 2 },
		"epoch":           func(c *CX01SelectionSpec) { c.Epoch = 2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			c := s.CX01Spec()
			mutate(&c)
			var err error
			i.Selection, err = NewCXSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			cx16Withheld(t, cx16Classify(t, cx16Profile(t, s, i), i.PCS.Evidence), i)
		})
	}
}

func TestCX16EveryContextFieldNegative(t *testing.T) {
	for _, name := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "age", "sequence", "monotonic", "publisher"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			switch name {
			case "interface-name":
				i.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				i.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "epoch":
				i.PCS.Epoch++
			case "direction":
				i.PCS.Direction = "other"
			case "scope":
				i.PCS.Scope = "aggregate"
			case "bank":
				i.PCS.Bank = "parallel"
			case "endpoint":
				i.PCS.Endpoint = "other"
			case "group":
				i.PCS.Group = "1"
			case "slot":
				i.PCS.Slot = "2"
			case "age":
				i.PCS.FieldAge = -1
			case "sequence":
				i.PCS.Evidence.Sequence = 0
			case "monotonic":
				i.PCS.Evidence.Monotonic = -1
			case "publisher":
				i.PCS.PublisherResolved = false
			}
			cx16Withheld(t, cx16Classify(t, cx16Profile(t, s, i), i.PCS.Evidence), i)
		})
	}
}

func TestCX16GeometryAndRawRecords(t *testing.T) {
	for _, name := range []string{"ide", "dlc", "raw-dlc", "rtr", "fd", "error", "zero-record", "mismatched-record", "evidence-record"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16Fixture(t)
			payload := i.PCS.Evidence.Frame.Bytes()
			switch name {
			case "ide":
				cx16Frame(t, &i, 0x313, true, payload)
			case "dlc":
				id, _ := canbus.NewStandardID(0x313)
				i.PCS.Evidence.Frame, _ = canbus.NewFrame(id, payload[:7])
			case "raw-dlc":
				raw := make([]byte, canbus.SocketCANRecordSize)
				raw[0], raw[1], raw[4], raw[7] = 0x13, 3, 8, 9
				copy(raw[8:], payload[:])
				var err error
				i.PCS.Evidence.Frame, err = canbus.DecodeSocketCANRecord(raw)
				if err != nil {
					t.Fatal(err)
				}
			case "rtr", "error", "fd", "zero-record", "mismatched-record":
				raw := make([]byte, canbus.SocketCANRecordSize)
				raw[0], raw[1], raw[4] = 0x13, 3, 8
				copy(raw[8:], payload[:])
				if name == "rtr" {
					raw[3] |= 0x40
				}
				if name == "error" {
					raw[3] |= 0x20
				}
				if name == "fd" {
					raw = append(raw, make([]byte, 56)...)
				}
				if name == "zero-record" {
					raw = make([]byte, canbus.SocketCANRecordSize)
				}
				if name == "mismatched-record" {
					raw[0]++
				}
				i.PCS.NativeRecord = raw
			case "evidence-record":
				i.PCS.Evidence.RawRecord[0] = 1
			}
			r := cx16Classify(t, cx16Profile(t, s, i), i.PCS.Evidence)
			cx16Withheld(t, r, i)
			if string(r.Diagnostics[0].NativeRecord) != string(i.PCS.NativeRecord) {
				t.Fatal("native bytes lost")
			}
		})
	}
}

func TestCX16LINKRouteEpochIdentityAndNoFallback(t *testing.T) {
	for _, name := range []string{"route", "source", "interface", "epoch", "partial-identity", "conflicting-identity"} {
		t.Run(name, func(t *testing.T) {
			s, i := cx16LINK(t, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '0', '6'})
			switch name {
			case "route":
				cx16Frame(t, &i, 0x1802f501, true, i.PCS.Evidence.Frame.Bytes())
			case "source":
				cx16Frame(t, &i, 0x1802f402, true, i.PCS.Evidence.Frame.Bytes())
			case "interface":
				i.PCS.Evidence.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "epoch":
				i.PCS.Epoch++
			case "partial-identity":
				cx16Frame(t, &i, 0x1802f401, true, [8]byte{'Q', 'A', 'A', 'A'})
			case "conflicting-identity":
				cx16Frame(t, &i, 0x1802f401, true, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '1', '1'})
			}
			p := cx16Profile(t, s, i)
			cx16Withheld(t, cx16Classify(t, p, i.PCS.Evidence), i)
			// A wrong route/epoch never installs a legacy interpretation.
			if c := NewRegistry(p, GrowattLowVoltageBMSV104()).Classify(i.PCS.Evidence); c.Profile == growattLowVoltageBMSV104Profile {
				t.Fatal("fallback")
			}
		})
	}
}

func TestCX16WireIdentityConflictStickyAndRaw(t *testing.T) {
	s, i := cx16LINK(t, 0x1802, [8]byte{'Q', 'A', 'A', 'A', '0', '0', '1', '1'})
	_, good := cx16LINK(t, 0x1805, [8]byte{})
	good.PCS.Evidence.Sequence, good.PCS.Evidence.Monotonic = 2, 2*time.Second
	p := cx16Profile(t, s, i, good)
	if r := cx16Classify(t, p, i.PCS.Evidence); r.Current.Status != "conflict" {
		t.Fatal(r)
	}
	r := cx16Classify(t, p, good.PCS.Evidence)
	cx16Withheld(t, r, good)
	if r.Current.Status != "conflict" {
		t.Fatal(r)
	}
}

func TestCX16SequenceConflictAndLastGood(t *testing.T) {
	for _, name := range []string{"same-sequence", "older-sequence", "reversed-time"} {
		t.Run(name, func(t *testing.T) {
			s, first := cx16Fixture(t)
			first.PCS.Evidence.Sequence = 2
			second := first
			payload := first.PCS.Evidence.Frame.Bytes()
			payload[0]++
			cx16Frame(t, &second, 0x313, false, payload)
			switch name {
			case "older-sequence":
				second.PCS.Evidence.Sequence = 1
			case "reversed-time":
				second.PCS.Evidence.Sequence = 3
				second.PCS.Evidence.Monotonic = 0
			}
			p := cx16Profile(t, s, first, second)
			before := cx16Classify(t, p, first.PCS.Evidence)
			after := cx16Classify(t, p, second.PCS.Evidence)
			cx16Withheld(t, after, second)
			if after.LastGood["core/voltage"] != before.LastGood["core/voltage"] {
				t.Fatal("rejection erased/refreshed history")
			}
		})
	}
}

func TestCX16IndependentStatusesZeroAndRetention(t *testing.T) {
	for _, status := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned"} {
		t.Run(status, func(t *testing.T) {
			s, first := cx16Fixture(t)
			second := first
			second.PCS.Evidence.Sequence, second.PCS.Evidence.Monotonic = 2, 2*time.Second
			cx16Frame(t, &second, 0x313, false, [8]byte{})
			second.PCS.FieldStates = map[string]string{"voltage": status}
			second.PCS.FieldAges = map[string]time.Duration{"voltage": 3 * time.Second}
			p := cx16Profile(t, s, first, second)
			old := cx16Classify(t, p, first.PCS.Evidence)
			got := cx16Classify(t, p, second.PCS.Evidence)
			if got.Current.Fields["core/voltage"].Status != status || got.Current.Fields["core/voltage"].Age != 3*time.Second || got.LastGood["core/voltage"] != old.LastGood["core/voltage"] {
				t.Fatal(got)
			}
			for _, name := range []string{"current", "temperature", "soc", "soh"} {
				f := got.Current.Fields["core/"+name]
				if f.Status != "accepted" || f.Numerator != 0 || f.PhysicalQualified {
					t.Fatalf("valid zero sibling %s: %#v", name, f)
				}
			}
		})
	}
}

func TestCX16BoundsAndAmbiguousSelection(t *testing.T) {
	s, i := cx16Fixture(t)
	for _, n := range []int{0, 1, 128, 129} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			replay := make([]CX16ReplayInput, n)
			for j := range replay {
				replay[j] = i
				replay[j].PCS.Evidence.Sequence = uint64(j + 1)
			}
			_, err := NewCX16SelectedProfile(s, replay)
			if (err == nil) != (n >= 1 && n <= 128) {
				t.Fatalf("bound %d: %v", n, err)
			}
		})
	}
	if _, err := NewCX16SelectedProfile(s, []CX16ReplayInput{i, i}); err == nil {
		t.Fatal("duplicate evidence admitted")
	}
	for _, identity := range []string{"QAAa9011", "qAAA0006", "Qaaa0006", "QAAA0000", "QAAA9999"} {
		c := s.CX01Spec()
		c.ProjectCode, c.Release = identity[:4], identity[4:]
		selected, err := NewCXSelection(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = NewCX16SelectedProfile(selected, []CX16ReplayInput{i}); err == nil {
			t.Fatalf("unresolved selector %s admitted", identity)
		}
	}
}

func TestCX16BoundedDetachedDiagnosticsAndConcurrency(t *testing.T) {
	s, i := cx16Fixture(t)
	i.PCS.NativeRecord = []byte{1, 2, 3}
	p := cx16Profile(t, s, i)
	i.PCS.NativeRecord[0] = 9
	first := cx16Classify(t, p, i.PCS.Evidence)
	first.Diagnostics[0].NativeRecord[0] = 8
	first.Current.Fields["invented"] = CXNativeField{Status: "accepted"}
	var wg sync.WaitGroup
	for n := 0; n < 24; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Classify(i.PCS.Evidence) }()
	}
	wg.Wait()
	last := cx16Classify(t, p, i.PCS.Evidence)
	if len(last.Diagnostics) != 16 || last.Diagnostics[0].NativeRecord[0] != 1 || len(last.Current.Fields) != 0 {
		t.Fatal(last)
	}
}

func TestCX16EveryProjectAndReleaseCharacterNegative(t *testing.T) {
	for _, field := range []string{"project", "release"} {
		for position := 0; position < 4; position++ {
			t.Run(fmt.Sprintf("%s/%d", field, position), func(t *testing.T) {
				s, i := cx16Fixture(t)
				c := s.CX01Spec()
				if field == "project" {
					text := []byte(c.ProjectCode)
					text[position] += 32
					c.ProjectCode = string(text)
				} else {
					text := []byte(c.Release)
					text[position]++
					c.Release = string(text)
				}
				var err error
				i.Selection, err = NewCXSelection(c)
				if err != nil {
					t.Fatal(err)
				}
				cx16Withheld(t, cx16Classify(t, cx16Profile(t, s, i), i.PCS.Evidence), i)
			})
		}
	}
}

func TestCX16ExtendedFamiliesRetainedSeparately(t *testing.T) {
	s, base := cx16Fixture(t)
	c := s.CX01Spec()
	c.Flavor, c.PCSFlavor, c.Direction = "pcs-extended19", "", "bms-to-pcs"
	c.RouteSet, c.Destination, c.Source = true, 0xf2, 1
	s = cx16Reselect(t, &base, c)
	a, b := base, base
	cx16Frame(t, &a, 0x1905<<16|0xf201, true, [8]byte{3, 0, 0x10, 0x0e, 5, 0, 0x20, 0x0d})
	b.PCS.Evidence.Sequence, b.PCS.Evidence.Monotonic = 2, 2*time.Second
	cx16Frame(t, &b, 0x1906<<16|0xf201, true, [8]byte{9, 0, 0, 11, 0, 0, 0, 0})
	p := cx16Profile(t, s, a, b)
	cx16Classify(t, p, a.PCS.Evidence)
	r := cx16Classify(t, p, b.PCS.Evidence)
	va, vb := r.LastGood["extended/1905/maximum_index"], r.LastGood["extended/1906/maximum_index"]
	if va.Status != "accepted" || vb.Status != "accepted" || va.Numerator != 3 || vb.Numerator != 9 {
		t.Fatalf("cross-family overwrite: %#v %#v", va, vb)
	}
	if _, ok := r.LastGood["extended/maximum_index"]; ok {
		t.Fatal("family-less retention key")
	}
	if r.RetainedNative["extended/1905"].Fields["maximum_index"].Numerator != 3 || r.RetainedNative["extended/1906"].Fields["maximum_index"].Numerator != 9 {
		t.Fatal(r.RetainedNative)
	}
}
