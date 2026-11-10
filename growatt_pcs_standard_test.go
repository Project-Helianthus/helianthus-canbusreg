package canbusreg

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
)

func cx05Fixture(t *testing.T, id uint32, p [8]byte) (CXSelection, CX05PCSInput) {
	t.Helper()
	iface, err := canbus.NewInterfaceIdentity("synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	spec := CX01SelectionSpec{Interface: iface, Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs", Scope: "local", Bank: "standard", Endpoint: "pack", Group: "group", Slot: "slot", Epoch: 1}
	s, err := NewCXSelection(spec)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := canbus.NewStandardID(id)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := canbus.NewFrame(identifier, p[:])
	if err != nil {
		t.Fatal(err)
	}
	return s, CX05PCSInput{CX01LINKInput: CX01LINKInput{Evidence: Evidence{Interface: iface, Frame: frame, Sequence: 7, Monotonic: 9 * time.Second}, Epoch: 1, Direction: spec.Direction, Scope: spec.Scope, Bank: spec.Bank, Endpoint: spec.Endpoint, Group: spec.Group, Slot: spec.Slot, FieldAge: time.Second}, PublisherResolved: true, MeasurementValid: true, TemperatureValid: true, CountValid: true, Count: 2, IndexScope: "local", IndexMinimum: 0, IndexMaximum: 15, IndexValid: true}
}

func TestCX05CoreArithmetic(t *testing.T) {
	s, i := cx05Fixture(t, 0x313, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 100, 0xe4})
	r := CX05DecodePCSStandard(s, i)
	for name, want := range map[string]struct {
		n, d int64
		unit string
	}{"voltage": {5310, 100, "V"}, "current": {-10, 10, "A"}, "temperature": {-10, 10, "C"}, "soc": {100, 1, "percent"}, "soh": {100, 1, "percent"}, "soh_high_bit": {1, 1, ""}} {
		f := r.Fields[name]
		if f.Numerator != want.n || f.Denominator != want.d || f.Unit != want.unit || f.Status != "accepted" || f.PhysicalQualified || r.PhysicalQualified {
			t.Fatalf("%s: %#v", name, f)
		}
		if f.Raw != i.Evidence.Frame.Bytes() || f.Evidence != i.Evidence || f.Epoch != i.Epoch || f.Age != i.FieldAge || f.Scope != i.Scope || f.Bank != i.Bank || f.Endpoint != i.Endpoint || f.Group != i.Group || f.Slot != i.Slot {
			t.Fatalf("evidence %s: %#v", name, f)
		}
	}
	s, i = cx05Fixture(t, 0x314, [8]byte{4, 0xd2, 0, 0, 0, 8, 0, 9})
	r = CX05DecodePCSStandard(s, i)
	if r.Fields["remaining_capacity"].Numerator != 1234 || r.Fields["remaining_capacity"].Denominator != 100 || r.Fields["full_capacity"].Numerator != 0 || r.Fields["full_capacity"].Status != "accepted" || r.Fields["spread"].Unit != "mV" || r.Fields["cycles"].Numerator != 9 {
		t.Fatal(r)
	}
}

func cx05Reselect(t *testing.T, s CXSelection, i CX05PCSInput, change func(*CX01SelectionSpec)) (CXSelection, CX05PCSInput) {
	t.Helper()
	c := s.CX01Spec()
	change(&c)
	next, err := NewCXSelection(c)
	if err != nil {
		t.Fatal(err)
	}
	i.Direction, i.Scope, i.Bank, i.Endpoint, i.Group, i.Slot, i.Epoch = c.Direction, c.Scope, c.Bank, c.Endpoint, c.Group, c.Slot, c.Epoch
	i.Evidence.Interface = c.Interface
	return next, i
}

func TestCX05AdmissionKeys(t *testing.T) {
	for _, key := range []string{"interface-name", "interface-index", "epoch", "direction", "scope", "bank", "endpoint", "group", "slot", "publisher", "ide", "dlc", "raw-dlc", "negative-age"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x313, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 44, 60})
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
				i.Group = "other"
			case "slot":
				i.Slot = "other"
			case "publisher":
				i.PublisherResolved = false
			case "negative-age":
				i.FieldAge = -1
			case "ide":
				id, _ := canbus.NewExtendedID(0x313)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data())
			case "dlc":
				id, _ := canbus.NewStandardID(0x313)
				i.Evidence.Frame, _ = canbus.NewFrame(id, i.Evidence.Frame.Data()[:7])
			case "raw-dlc":
				var record [16]byte
				binary.NativeEndian.PutUint32(record[:4], 0x313)
				record[4], record[7] = 8, 9
				p := i.Evidence.Frame.Bytes()
				copy(record[8:], p[:])
				var err error
				i.Evidence.Frame, err = canbus.DecodeSocketCANRecord(record[:])
				if err != nil {
					t.Fatal(err)
				}
			}
			r := CX05DecodePCSStandard(s, i)
			if len(r.Fields) != 0 || r.Raw != i.Evidence.Frame.Bytes() || r.Evidence != i.Evidence || r.PhysicalQualified {
				t.Fatal(r)
			}
		})
	}
}

func TestCX05NativeDiagnostics(t *testing.T) {
	for _, kind := range []string{"malformed", "rtr", "error", "fd", "record-frame-conflict", "evidence-record-rtr"} {
		t.Run(kind, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x313, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
			record := make([]byte, 16)
			binary.NativeEndian.PutUint32(record[:4], 0x313)
			record[4] = 8
			p := i.Evidence.Frame.Bytes()
			copy(record[8:], p[:])
			switch kind {
			case "malformed":
				record = record[:15]
			case "rtr", "evidence-record-rtr":
				binary.NativeEndian.PutUint32(record[:4], 0x40000313)
			case "error":
				binary.NativeEndian.PutUint32(record[:4], 0x20000313)
			case "fd":
				record = append(record, make([]byte, 56)...)
			case "record-frame-conflict":
				record[8]++
			}
			if kind == "evidence-record-rtr" {
				copy(i.Evidence.RawRecord[:], record)
			} else {
				i.NativeRecord = record
			}
			r := CX05DecodePCSStandard(s, i)
			if len(r.Fields) != 0 || r.Status != "invalid" || r.Evidence != i.Evidence {
				t.Fatal(r)
			}
			if kind != "evidence-record-rtr" {
				if string(r.Diagnostics) != string(record) {
					t.Fatal("diagnostics lost")
				}
				record[0]++
				if string(r.Diagnostics) == string(record) {
					t.Fatal("diagnostic alias")
				}
			}
		})
	}
	s, i := cx05Fixture(t, 0x313, [8]byte{})
	i.NativeRecord = make([]byte, 16)
	binary.NativeEndian.PutUint32(i.NativeRecord[:4], 0x313)
	i.NativeRecord[4] = 8
	if CX05DecodePCSStandard(s, i).Fields["voltage"].Status != "accepted" {
		t.Fatal("valid record rejected")
	}
}

func TestCX05SelectorAndScopeGates(t *testing.T) {
	for _, key := range []string{"project", "case", "release", "flavor", "direction", "aggregate", "ambiguous", "legacy-choice"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x313, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 44, 60})
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) {
				switch key {
				case "project":
					c.ProjectCode = "QAAB"
				case "case":
					c.ProjectCode = "qAAA"
				case "release":
					c.Release = "0007"
				case "flavor":
					c.Flavor = "pcs-extended19"
				case "direction":
					c.Direction = "pcs-to-bms"
				case "aggregate":
					c.Scope = "aggregate"
				case "ambiguous":
					c.ProjectCode, c.Release = "QAAa", "9011"
				case "legacy-choice":
					c.ProjectCode, c.Release, c.Choice = "QAAa", "9011", "legacy19"
				}
			})
			r := CX05DecodePCSStandard(s, i)
			for _, f := range r.Fields {
				if f.Status == "accepted" || f.Unit != "" || f.Meaning != "" {
					t.Fatal(key, f)
				}
			}
			if r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal("raw lost")
			}
		})
	}
}

func TestCX05StatusHypothesisNegatives(t *testing.T) {
	cases := []struct {
		name, field string
		mask        uint16
		change      func(*CX01SelectionSpec)
	}{
		{"raw-state-aggregate", "raw_state", 3, func(c *CX01SelectionSpec) { c.Scope = "aggregate" }},
		{"fault-wrong-project", "fault_hint", 4, func(c *CX01SelectionSpec) { c.ProjectCode = "QAAB" }},
		{"sleep-wrong-case", "sleep_hint", 0x10, func(c *CX01SelectionSpec) { c.ProjectCode = "qAAA" }},
		{"discharge-wrong-flavor", "discharge_hint", 0x20, func(c *CX01SelectionSpec) { c.Flavor = "pcs-parallel" }},
		{"charge-wrong-endpoint", "charge_hint", 0x40, nil},
		{"force-wrong-release", "force_hint", 0x400, func(c *CX01SelectionSpec) { c.Release = "0007" }},
	}
	for _, v := range cases {
		t.Run(v.name, func(t *testing.T) {
			p := [8]byte{}
			binary.BigEndian.PutUint16(p[6:], v.mask)
			s, i := cx05Fixture(t, 0x311, p)
			good := CX05DecodePCSStandard(s, i)
			if good.Fields[v.field].Status != "accepted" {
				t.Fatal(good)
			}
			if v.change == nil {
				i.Endpoint = "other"
			} else {
				s, i = cx05Reselect(t, s, i, v.change)
			}
			r := CX05DecodePCSStandard(s, i)
			if r.Fields[v.field].Status == "accepted" || r.Raw != p {
				t.Fatal(r)
			}
		})
	}
	for bit := uint(0); bit < 16; bit++ {
		t.Run("toggle-"+string(rune('a'+bit)), func(t *testing.T) {
			for _, word := range []uint16{0, 1 << bit} {
				p := [8]byte{}
				binary.BigEndian.PutUint16(p[6:], word)
				s, i := cx05Fixture(t, 0x311, p)
				r := CX05DecodePCSStandard(s, i)
				if r.Fields["status"].Numerator != int64(word) || r.Raw != p {
					t.Fatal(r)
				}
				for _, h := range []struct {
					name string
					mask uint16
				}{{"raw_state", 3}, {"fault_hint", 4}, {"sleep_hint", 0x10}, {"discharge_hint", 0x20}, {"charge_hint", 0x40}, {"force_hint", 0x400}} {
					want := int64(word & h.mask)
					if h.mask != 3 && want != 0 {
						want = 1
					}
					if r.Fields[h.name].Numerator != want || r.Fields[h.name].Grade != "Hypothesis" {
						t.Fatal(h.name, r)
					}
				}
				if _, ok := r.Fields["balancing"]; ok {
					t.Fatal("invented balance state")
				}
			}
		})
	}
}

func TestCX05TemperatureHypothesisNegatives(t *testing.T) {
	for _, key := range []string{"local-measurement", "local-temperature", "local-scope", "local-release", "release11-temperature", "release11-count-valid", "release11-count-zero", "release11-measurement"} {
		t.Run(key, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x313, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 40, 60})
			if len(key) >= 9 && key[:9] == "release11" {
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0011" })
			}
			if CX05DecodePCSStandard(s, i).Fields["temperature"].Status != "accepted" {
				t.Fatal("positive control")
			}
			switch key {
			case "local-measurement", "release11-measurement":
				i.MeasurementValid = false
			case "local-temperature", "release11-temperature":
				i.TemperatureValid = false
			case "local-scope":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Scope = "aggregate" })
			case "local-release":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0007" })
			case "release11-count-valid":
				i.CountValid = false
			case "release11-count-zero":
				i.Count = 0
			}
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["temperature"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	for _, release := range []string{"0006", "0011"} {
		for _, code := range []uint16{0, 1, 0x7fff, 0x8000, 0xfff6, 0xffff} {
			p := [8]byte{}
			binary.BigEndian.PutUint16(p[4:], code)
			s, i := cx05Fixture(t, 0x313, p)
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = release })
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["temperature"].Numerator != int64(int16(code)) || r.Fields["temperature"].Denominator != 10 {
				t.Fatal(r)
			}
		}
	}
	for _, count := range []uint16{0, 1, 2, 65535} {
		s, i := cx05Fixture(t, 0x313, [8]byte{})
		s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0011" })
		i.Count = count
		if (CX05DecodePCSStandard(s, i).Fields["temperature"].Status == "accepted") != (count > 0) {
			t.Fatal(count)
		}
	}
}

func TestCX05Release11SOHHypothesis(t *testing.T) {
	for _, v := range []struct {
		name       string
		tenths     int
		diagnostic bool
	}{{"below", 600, false}, {"boundary", 601, false}, {"independent", 601, true}} {
		t.Run(v.name, func(t *testing.T) {
			b := byte((v.tenths + 9) / 10)
			if v.tenths < 601 || v.diagnostic {
				b |= 128
			}
			s, i := cx05Fixture(t, 0x313, [8]byte{0, 0, 0, 0, 0, 0, 100, b})
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0011" })
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["soh_high_bit"].Numerator != int64(b>>7) || r.Fields["soh_high_bit"].Grade != "Hypothesis" || r.Fields["soh"].Numerator != int64(b&127) || r.Fields["voltage"].Meaning != "output-port voltage" {
				t.Fatal(r)
			}
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0006" })
			r = CX05DecodePCSStandard(s, i)
			if r.Fields["soh_high_bit"].Grade == "Hypothesis" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal("legacy inherited OR rule")
			}
		})
	}
	for _, gate := range []string{"measurement", "field-stale"} {
		t.Run(gate, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x313, [8]byte{0, 0, 0, 0, 0, 0, 100, 188})
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0011" })
			if gate == "measurement" {
				i.MeasurementValid = false
			} else {
				i.FieldStates = map[string]string{"soh_high_bit": "stale"}
			}
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["soh_high_bit"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
}

func TestCX05GatewaySOCHypothesis(t *testing.T) {
	for _, gate := range []string{"project", "release", "scope", "measurement", "extra6-invalid", "extra7-invalid"} {
		t.Run(gate, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x322, [8]byte{0, 1, 0, 2, 3, 4, 100, 0})
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QCAA", "0001", "aggregate" })
			good := CX05DecodePCSStandard(s, i)
			if good.Fields["extra_6"].Status != "accepted" || good.Fields["extra_7"].Status != "accepted" {
				t.Fatal(good)
			}
			switch gate {
			case "project":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode = "QAAA" })
			case "release":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0002" })
			case "scope":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Scope = "local" })
			case "measurement":
				i.MeasurementValid = false
			case "extra6-invalid":
				i.FieldStates = map[string]string{"extra_6": "invalid"}
			case "extra7-invalid":
				i.FieldStates = map[string]string{"extra_7": "invalid"}
			}
			r := CX05DecodePCSStandard(s, i)
			name := "extra_6"
			if gate == "extra7-invalid" {
				name = "extra_7"
			}
			if r.Fields[name].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
			if gate == "extra6-invalid" && r.Fields["extra_7"].Status != "accepted" {
				t.Fatal("sibling lost")
			}
		})
	}
	for _, n := range []byte{0, 1, 99, 100, 101, 255} {
		s, i := cx05Fixture(t, 0x322, [8]byte{0, 0, 0, 0, 0, 0, n, n})
		s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QCAA", "0001", "aggregate" })
		r := CX05DecodePCSStandard(s, i)
		for _, name := range []string{"extra_6", "extra_7"} {
			if (r.Fields[name].Status == "accepted") != (n <= 100) {
				t.Fatal(n, r)
			}
		}
	}
}

func TestCX05ExtremaAndLocationHypothesis(t *testing.T) {
	for _, id := range []uint32{0x319, 0x322, 0x330} {
		s, i := cx05Fixture(t, id, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
		r := CX05DecodePCSStandard(s, i)
		switch id {
		case 0x319:
			if r.Fields["maximum_cell"].Numerator != 515 || r.Fields["minimum_cell"].Numerator != 1029 || r.Fields["maximum_index"].Numerator != 6 || r.Fields["minimum_index"].Numerator != 7 || r.Fields["state_0"].Numerator != 1 || r.Fields["state_7"].Numerator != 8 {
				t.Fatal(r)
			}
		case 0x322:
			if r.Fields["temperature_maximum"].Numerator != 258 || r.Fields["temperature_minimum"].Numerator != 772 || r.Fields["maximum_temperature_index"].Numerator != 5 || r.Fields["maximum_index"].Status != "" || r.Fields["extra_6"].Status != "raw" {
				t.Fatal(r)
			}
		case 0x330:
			if r.Fields["maximum_cell"].Numerator != 1286 || r.Fields["minimum_cell"].Numerator != 1800 || r.Fields["maximum_index"].Numerator != 2 || r.Fields["minimum_index"].Numerator != 4 || r.Fields["maximum_location"].Numerator != 1 {
				t.Fatal(r)
			}
		}
		i.IndexScope = "aggregate"
		r = CX05DecodePCSStandard(s, i)
		idx := "maximum_index"
		if id == 0x322 {
			idx = "maximum_temperature_index"
		}
		if r.Fields[idx].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
			t.Fatal("index scope")
		}
		if id == 0x322 && r.Fields["temperature_maximum"].Status != "accepted" {
			t.Fatal("index erased measurement")
		}
	}
	for _, gate := range []string{"project", "release", "scope", "index-scope", "index-valid", "location-state"} {
		t.Run("fixed-location-"+gate, func(t *testing.T) {
			s, i := cx05Fixture(t, 0x330, [8]byte{1, 2, 1, 4, 5, 6, 7, 8})
			s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QAAB", "0004", "aggregate" })
			i.IndexScope = "aggregate"
			if CX05DecodePCSStandard(s, i).Fields["maximum_location_fixed_aggregate"].Status != "accepted" {
				t.Fatal("positive")
			}
			switch gate {
			case "project":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode = "QAAA" })
			case "release":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Release = "0099" })
			case "scope":
				s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.Scope = "local" })
			case "index-scope":
				i.IndexScope = "local"
			case "index-valid":
				i.IndexValid = false
			case "location-state":
				i.FieldStates = map[string]string{"maximum_location_fixed_aggregate": "unassigned"}
			}
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["maximum_location_fixed_aggregate"].Status == "accepted" || r.Raw != i.Evidence.Frame.Bytes() {
				t.Fatal(r)
			}
		})
	}
	for _, location := range []byte{0, 1, 2, 255} {
		s, i := cx05Fixture(t, 0x330, [8]byte{location, 2, location, 4})
		s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QAAB", "0004", "aggregate" })
		i.IndexScope = "aggregate"
		r := CX05DecodePCSStandard(s, i)
		if (r.Fields["maximum_location_fixed_aggregate"].Status == "accepted") != (location == 1) {
			t.Fatal(r)
		}
	}
	for _, id := range []uint32{0x319, 0x322, 0x330} {
		s, i := cx05Fixture(t, id, [8]byte{1, 2, 1, 4, 5, 6, 7, 8})
		s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QAAB", "0099", "aggregate" })
		i.IndexScope = "aggregate"
		i.IndexValid = true
		r := CX05DecodePCSStandard(s, i)
		for name, f := range r.Fields {
			if f.Status == "accepted" && (strings.HasSuffix(name, "_index") || strings.HasSuffix(name, "_fixed_aggregate")) {
				t.Fatalf("unknown QAAB release accepted %s: %v", name, r)
			}
		}
	}
	for _, n := range []byte{0, 1, 14, 15, 16, 255} {
		s, i := cx05Fixture(t, 0x319, [8]byte{0, 0, 0, 0, 0, n, n})
		r := CX05DecodePCSStandard(s, i)
		if (r.Fields["maximum_index"].Status == "accepted") != (n <= 15) {
			t.Fatal(r)
		}
	}
}

func TestCX05PartialStatesAndLastKnownGood(t *testing.T) {
	s, i := cx05Fixture(t, 0x313, [8]byte{0x14, 0xbe, 0xff, 0xf6, 0xff, 0xf6, 40, 60})
	previous := CX05DecodePCSStandard(s, i).CXNativeResult
	for _, state := range []string{"absent", "invalid", "unsupported", "stale", "conflict", "unassigned", "unrecognized"} {
		t.Run(state, func(t *testing.T) {
			i.FieldStates = map[string]string{"temperature": state}
			i.FieldAges = map[string]time.Duration{"temperature": 5 * time.Second, "voltage": 2 * time.Second}
			r := CX05DecodePCSStandard(s, i)
			if r.Fields["temperature"].Status == "accepted" || r.Fields["voltage"].Status != "accepted" || r.Fields["voltage"].Age != 2*time.Second {
				t.Fatal(r)
			}
			merged := CX05RetainAccepted(previous, r.CXNativeResult)
			if merged.Fields["temperature"] != previous.Fields["temperature"] || merged.Fields["voltage"].Age != 2*time.Second {
				t.Fatal("retention/age")
			}
			r.Fields["voltage"] = CXNativeField{}
			if previous.Fields["voltage"].Numerator != 5310 {
				t.Fatal("alias")
			}
		})
	}
	i.FieldStates = nil
	i.FieldAges = map[string]time.Duration{"temperature": -1}
	if CX05DecodePCSStandard(s, i).Fields["temperature"].Status != "invalid" {
		t.Fatal("negative field age")
	}
	for _, key := range []string{"interface-name", "interface-index", "flavor", "project", "release", "choice", "direction", "scope", "bank", "endpoint", "group", "slot", "epoch", "pcs-flavor", "route-set", "destination", "source"} {
		t.Run("retention-key-"+key, func(t *testing.T) {
			c := s.CX01Spec()
			switch key {
			case "interface-name":
				c.Interface, _ = canbus.NewInterfaceIdentity("other", 1)
			case "interface-index":
				c.Interface, _ = canbus.NewInterfaceIdentity("synthetic", 2)
			case "flavor":
				c.Flavor = "pcs-extended19"
			case "project":
				c.ProjectCode = "QAAB"
			case "release":
				c.Release = "0007"
			case "choice":
				c.Choice = "legacy19"
			case "direction":
				c.Direction = "other"
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
			case "pcs-flavor":
				c.PCSFlavor = "pcs-parallel"
			case "route-set":
				c.RouteSet = true
			case "destination":
				c.Destination++
			case "source":
				c.Source++
			}
			next, err := NewCXSelection(c)
			if err != nil {
				t.Fatal(err)
			}
			update := CXNativeResult{Selection: next, Fields: map[string]CXNativeField{}}
			if len(CX05RetainAccepted(previous, update).Fields) != 0 {
				t.Fatal("cross-key retention")
			}
		})
	}
}

func TestCX05SummaryLimitsAndRanges(t *testing.T) {
	s, i := cx05Fixture(t, 0x312, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	r := CX05DecodePCSStandard(s, i)
	for name, want := range map[string]int64{"status_a": 513, "status_b": 1027, "reported_count": 5, "padding_5": 6, "padding_6": 7, "padding_7": 8} {
		if r.Fields[name].Numerator != want || r.Fields[name].Status != "raw" {
			t.Fatal(name, r)
		}
	}
	if len(r.Fields) != 6 {
		t.Fatal("invented cell/manufacturer fields")
	}
	s, i = cx05Fixture(t, 0x311, [8]byte{0x12, 0x34, 0x23, 0x45, 0x34, 0x56, 0x45, 0x67})
	r = CX05DecodePCSStandard(s, i)
	if r.Fields["voltage_limit"].Numerator != 0x1234 || r.Fields["voltage_limit"].Denominator != 10 || r.Fields["charge_limit"].Numerator != 0x2345 || r.Fields["discharge_limit"].Numerator != 0x3456 {
		t.Fatal(r)
	}
	s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release, c.Scope = "QCAA", "0001", "aggregate" })
	r = CX05DecodePCSStandard(s, i)
	if r.Fields["charge_limit"].Numerator != 0x3456 || r.Fields["discharge_limit"].Numerator != 0x2345 || r.Fields["charge_limit"].Unit != "" {
		t.Fatal("gateway mapping/scale", r)
	}
	for _, n := range []byte{0, 1, 99, 100, 101, 127, 128, 255} {
		s, i = cx05Fixture(t, 0x313, [8]byte{0, 0, 0, 0, 0, 0, n, n})
		r = CX05DecodePCSStandard(s, i)
		if (r.Fields["soc"].Status == "accepted") != (n <= 100) || (r.Fields["soh"].Status == "accepted") != (n&127 <= 100) {
			t.Fatal(n, r)
		}
	}
	for _, n := range []uint16{0, 1, 32767, 32768, 65534, 65535} {
		p := [8]byte{}
		for at := 0; at < 8; at += 2 {
			binary.BigEndian.PutUint16(p[at:], n)
		}
		s, i = cx05Fixture(t, 0x314, p)
		r = CX05DecodePCSStandard(s, i)
		for _, name := range []string{"remaining_capacity", "full_capacity", "spread", "cycles"} {
			if r.Fields[name].Numerator != int64(n) {
				t.Fatal(name, r)
			}
		}
		s, i = cx05Fixture(t, 0x313, p)
		r = CX05DecodePCSStandard(s, i)
		if r.Fields["voltage"].Numerator != int64(n) || r.Fields["current"].Numerator != int64(int16(n)) {
			t.Fatal(r)
		}
	}
	s, i = cx05Fixture(t, 0x301, [8]byte{})
	if CX05DecodePCSStandard(s, i).Status != "unsupported" {
		t.Fatal("input PID decoded")
	}
}

func TestCX05InvalidCodesAndConflictingRecords(t *testing.T) {
	s, i := cx05Fixture(t, 0x313, [8]byte{0, 0, 0, 0, 0, 0, 101, 101})
	r := CX05DecodePCSStandard(s, i)
	if r.Fields["soc"].Status != "invalid" || r.Fields["soh"].Status != "invalid" || r.Fields["voltage"].Status != "accepted" {
		t.Fatal("invalid codes must remain distinct from unknown units", r)
	}
	s, i = cx05Fixture(t, 0x313, [8]byte{})
	i.NativeRecord = make([]byte, 16)
	binary.NativeEndian.PutUint32(i.NativeRecord[:4], 0x313)
	i.NativeRecord[4] = 8
	copy(i.Evidence.RawRecord[:], i.NativeRecord)
	i.Evidence.RawRecord[8] = 1
	r = CX05DecodePCSStandard(s, i)
	if len(r.Fields) != 0 || r.Evidence != i.Evidence || string(r.Diagnostics) != string(i.NativeRecord) {
		t.Fatal("contradictory raw record admitted", r)
	}
}

func TestCX05ExtremaIndependentTemperatureGate(t *testing.T) {
	s, i := cx05Fixture(t, 0x322, [8]byte{0xff, 0xf6, 0x80, 0, 0, 15, 9, 10})
	r := CX05DecodePCSStandard(s, i)
	if r.Fields["temperature_maximum"].Numerator != -10 || r.Fields["temperature_minimum"].Numerator != -32768 || r.Fields["temperature_minimum"].Denominator != 10 {
		t.Fatal(r)
	}
	i.FieldStates = map[string]string{"temperature_maximum": "invalid"}
	r = CX05DecodePCSStandard(s, i)
	if r.Fields["temperature_maximum"].Status != "invalid" || r.Fields["temperature_minimum"].Status != "accepted" || r.Fields["minimum_temperature_index"].Status != "accepted" {
		t.Fatal("invalid maximum erased sibling", r)
	}
	i.FieldStates = nil
	i.TemperatureValid = false
	r = CX05DecodePCSStandard(s, i)
	if r.Fields["temperature_maximum"].Status == "accepted" || r.Fields["temperature_minimum"].Status == "accepted" || r.Fields["maximum_temperature_index"].Status != "accepted" {
		t.Fatal("temperature gate affected indices", r)
	}
}

func TestCX05IndexRequiresSupportedContract(t *testing.T) {
	for _, id := range []uint32{0x319, 0x322, 0x330} {
		s, i := cx05Fixture(t, id, [8]byte{0, 0, 0, 0, 1, 2, 3, 4})
		s, i = cx05Reselect(t, s, i, func(c *CX01SelectionSpec) { c.ProjectCode, c.Release = "QZZZ", "9999" })
		r := CX05DecodePCSStandard(s, i)
		for name, f := range r.Fields {
			if (name == "maximum_index" || name == "minimum_index" || name == "maximum_temperature_index" || name == "minimum_temperature_index") && (f.Status == "accepted" || f.Grade == "Proven") {
				t.Fatal(id, name, f)
			}
		}
	}
}

func TestCX05RetentionKeepsCellAndTemperatureIndicesApart(t *testing.T) {
	s, i := cx05Fixture(t, 0x319, [8]byte{0, 0, 0, 0, 0, 6, 7, 0})
	cell := CX05DecodePCSStandard(s, i)
	_, i2 := cx05Fixture(t, 0x322, [8]byte{0, 0, 0, 0, 5, 4, 0, 0})
	temp := CX05DecodePCSStandard(s, i2)
	merged := CX05RetainAccepted(cell.CXNativeResult, temp.CXNativeResult)
	if merged.Fields["maximum_index"].Numerator != 6 || merged.Fields["minimum_index"].Numerator != 7 || merged.Fields["maximum_temperature_index"].Numerator != 5 || merged.Fields["minimum_temperature_index"].Numerator != 4 {
		t.Fatal(merged.Fields)
	}
}
